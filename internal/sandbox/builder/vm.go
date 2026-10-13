package builder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	name := builderSandboxName()
	hostPort, err := v.resolveBuilderPort(ctx, name)
	if err != nil {
		return 0, err
	}
	if err := v.ready(ctx, hostPort); err != nil {
		builderTracef("builder readiness failed on port %d: %v; recreating", hostPort, err)
		_ = clearBuilderRecord()
		recreatedPort, recreateErr := v.recreateBuilder(ctx, name)
		if recreateErr != nil {
			return 0, recreateErr
		}
		if retryErr := v.ready(ctx, recreatedPort); retryErr != nil {
			return 0, retryErr
		}
		return recreatedPort, nil
	}
	return hostPort, nil
}

// recreateBuilder replaces a builder VM whose buildkitd failed its readiness
// probe. It re-enters the creation flock so a concurrent caller observes the
// same single VM, and rewrites the port record before returning.
func (v *VM) recreateBuilder(ctx context.Context, name string) (int, error) {
	release, err := acquireBuilderFlock()
	if err != nil {
		return 0, err
	}
	defer release()

	if cacheErr := v.ensureCacheStorage(ctx); cacheErr != nil {
		return 0, cacheErr
	}
	hostPort := options.FirstFreeHostPort(builderHostPortBase)
	builderTracef("recreate builder sandbox %q port %d", name, hostPort)
	sb, createErr := v.Msb.CreateSandbox(ctx, name, builderSandboxOptions(hostPort)...)
	if createErr != nil {
		return 0, fmt.Errorf("recreate builder sandbox: %w", createErr)
	}
	if startErr := startBuildkitd(ctx, sb); startErr != nil {
		return 0, startErr
	}
	if recordErr := writeBuilderRecord(name, hostPort); recordErr != nil {
		return 0, recordErr
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
		if hostPort, reused, reuseErr := v.reusedBuilderPort(name, handle); reuseErr != nil {
			return 0, reuseErr
		} else if reused {
			return hostPort, nil
		}
	} else {
		builderTracef("GetSandbox %q error: %v (notFound=%v)", name, getErr, msb.IsNotFound(getErr))
		if !msb.IsNotFound(getErr) {
			return 0, fmt.Errorf("check builder sandbox %q: %w", name, getErr)
		}
	}

	if cacheErr := v.ensureCacheStorage(ctx); cacheErr != nil {
		return 0, cacheErr
	}

	hostPort := options.FirstFreeHostPort(builderHostPortBase)
	builderTracef("create builder sandbox %q port %d", name, hostPort)
	sb, createErr := v.Msb.CreateSandbox(ctx, name, builderSandboxOptions(hostPort)...)
	if createErr != nil {
		return 0, fmt.Errorf("create builder sandbox: %w", createErr)
	}

	if startErr := startBuildkitd(ctx, sb); startErr != nil {
		return 0, startErr
	}
	if recordErr := writeBuilderRecord(name, hostPort); recordErr != nil {
		return 0, recordErr
	}
	return hostPort, nil
}

// ready runs the readiness seam when configured. A nil Ready treats the VM as
// ready.
func (v *VM) ready(ctx context.Context, hostPort int) error {
	if v.Ready == nil {
		return nil
	}
	return v.Ready(ctx, hostPort)
}

// reusedBuilderPort reports whether an existing sandbox can be reused and, if
// so, its host port. The port comes from the sandbox config when available,
// else from the persisted record written at creation (the SDK drops port
// bindings from SandboxHandle.Config()). A stale record for a different
// sandbox is cleared so the caller recreates the VM.
func (v *VM) reusedBuilderPort(name string, handle msb.SandboxHandle) (int, bool, error) {
	hostPort := publishedHostPort(handle)
	builderTracef("found builder sandbox %q port %d", name, hostPort)
	if hostPort > 0 {
		return hostPort, true, nil
	}
	if record, ok := readBuilderRecord(); ok && record.Name == name && record.HostPort > 0 {
		builderTracef("using recorded builder port %d for %q", record.HostPort, name)
		return record.HostPort, true, nil
	}
	if err := clearBuilderRecord(); err != nil {
		return 0, false, fmt.Errorf("clear stale builder record: %w", err)
	}
	return 0, false, nil
}

// KeepAlive refreshes the shared builder VM's idle-activity timer on an
// interval until the returned stop function is called or ctx is done. A short
// idle timeout would otherwise reap the VM mid-build, since builds talk to
// buildkitd over the forwarded port rather than through a tracked exec session.
// The stop function blocks until the loop has exited, so no touch occurs after
// it returns; it is safe to call more than once.
func (v *VM) KeepAlive(ctx context.Context) func() {
	done := make(chan struct{})
	exited := make(chan struct{})
	ticker := time.NewTicker(builderKeepAliveInterval)
	go func() {
		defer close(exited)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				v.touch(ctx)
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			<-exited
		})
	}
}

// touch refreshes the builder VM's idle timer, ignoring errors: a missed touch
// is only a risk if it persists, and the next tick retries.
func (v *VM) touch(ctx context.Context) {
	handle, err := v.Msb.GetSandbox(ctx, builderSandboxName())
	if err != nil {
		return
	}
	_ = handle.Touch(ctx)
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
	defer func() { _ = sb.Close() }()
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

// ensureCacheStorage prepares the BuildKit cache backing for the selected mode:
// the named msb volume, or the host directory a bind cache mounts.
func (v *VM) ensureCacheStorage(ctx context.Context) error {
	switch builderCacheMode() {
	case cacheModeBind:
		if err := os.MkdirAll(builderCacheBindPath(), 0o750); err != nil {
			return fmt.Errorf("create buildkit cache bind dir: %w", err)
		}
		return nil
	case cacheModeTmpfs, cacheModeTmpfsExport, cacheModeRegistry:
		return nil
	default:
		return v.ensureCacheVolume(ctx)
	}
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
	builderTracef("start buildkitd: %s", buildkitdCommand())
	stream, err := sb.ShellStream(ctx, buildkitdCommand())
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
