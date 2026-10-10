package builder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// Ensure returns the published host port of a ready shared BuildKit builder VM,
// creating and starting it if absent. A host-wide flock serializes concurrent
// callers so only one creates the VM; readiness is probed after the lock is
// released so a slow buildkitd does not serialize every caller.
func (v *VM) Ensure(ctx context.Context) (int, error) {
	hostPort, err := v.resolveBuilderPort(ctx, builderSandboxName())
	if err != nil {
		return 0, err
	}
	if err := v.ready(ctx, hostPort); err != nil {
		return 0, err
	}
	return hostPort, nil
}

// resolveBuilderPort returns the published host port of the shared builder VM,
// creating and starting it if absent. It holds the host-wide flock for the
// whole discovery/create coordination, so callers must not block on readiness
// while holding it.
func (v *VM) resolveBuilderPort(ctx context.Context, name string) (int, error) {
	release, err := acquireBuilderFlock()
	if err != nil {
		return 0, err
	}
	defer release()

	handle, getErr := v.Msb.GetSandbox(ctx, name)
	if getErr == nil {
		if hostPort := publishedHostPort(handle); hostPort > 0 {
			return hostPort, nil
		}
	} else if !msb.IsNotFound(getErr) {
		return 0, fmt.Errorf("check builder sandbox %q: %w", name, getErr)
	}

	if volumeErr := v.ensureCacheVolume(ctx); volumeErr != nil {
		return 0, volumeErr
	}

	hostPort := options.FirstFreeHostPort(builderHostPortBase)
	sb, createErr := v.Msb.CreateSandbox(ctx, name, builderSandboxOptions(hostPort)...)
	if createErr != nil {
		return 0, fmt.Errorf("create builder sandbox: %w", createErr)
	}

	if startErr := startBuildkitd(ctx, sb); startErr != nil {
		return 0, startErr
	}
	return hostPort, nil
}

// ready runs the readiness seam when configured. A nil Ready treats the VM as
// ready; Task 2 constructs the real client with a default implementation.
func (v *VM) ready(ctx context.Context, hostPort int) error {
	if v.Ready == nil {
		return nil
	}
	return v.Ready(ctx, hostPort)
}

// PruneCache reclaims stale BuildKit cache inside the shared builder VM. When no
// builder VM exists there is nothing to reclaim and it is a no-op.
func (v *VM) PruneCache(ctx context.Context) error {
	handle, err := v.Msb.GetSandbox(ctx, builderSandboxName())
	if err != nil {
		if msb.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("find builder sandbox: %w", err)
	}
	sb, err := handle.Connect(ctx)
	if err != nil {
		return fmt.Errorf("connect builder sandbox: %w", err)
	}
	result, err := sb.Shell(ctx, buildctlPruneCommand)
	if err != nil {
		return fmt.Errorf("prune buildkit cache: %w", err)
	}
	if !result.Success() {
		return fmt.Errorf("prune buildkit cache: buildctl exited with code %d", result.ExitCode())
	}
	return nil
}

// publishedHostPort reads the first published host port from an existing VM's
// config, or 0 when none is published.
func publishedHostPort(handle msb.SandboxHandle) int {
	cfg, err := handle.Config()
	if err != nil || cfg == nil {
		return 0
	}
	for _, binding := range cfg.PortBindings {
		if binding.HostPort != 0 {
			return int(binding.HostPort)
		}
	}
	return 0
}

// ensureCacheVolume creates the shared BuildKit cache volume, tolerating an
// already-exists result.
func (v *VM) ensureCacheVolume(ctx context.Context) error {
	_, err := v.Msb.CreateVolume(ctx, cacheVolumeName)
	if err == nil || msbSdk.IsKind(err, msbSdk.ErrVolumeAlreadyExists) {
		return nil
	}
	if strings.Contains(err.Error(), "exists") {
		return nil
	}
	return fmt.Errorf("create buildkit cache volume: %w", err)
}

// startBuildkitd launches buildkitd inside the sandbox and drains its output so
// the stream is not left unread.
func startBuildkitd(ctx context.Context, sb msb.Sandbox) error {
	stream, err := sb.ShellStream(ctx, buildkitdCommand)
	if err != nil {
		return fmt.Errorf("start buildkitd: %w", err)
	}
	go drainStream(ctx, stream)
	return nil
}

func drainStream(ctx context.Context, stream msb.StreamHandle) {
	defer func() { _ = stream.Close() }()
	for {
		if _, err := stream.Recv(ctx); err != nil {
			return
		}
	}
}

// acquireBuilderFlock takes an exclusive host-wide flock guarding builder VM
// creation. It returns a release function.
func acquireBuilderFlock() (func(), error) {
	dir := configpaths.Get().UserStateDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create builder state dir: %w", err)
	}
	path := filepath.Join(dir, "builder-vm.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open builder flock %s: %w", path, err)
	}
	if err := state.FlockExclusive(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock builder flock %s: %w", path, err)
	}
	return func() { _ = f.Close() }, nil
}
