package builder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/builder/cacheregistry"
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
	return v.createBuilder(ctx, name)
}

// createBuilder creates the builder VM (replacing any same-name sandbox) and
// starts buildkitd. The caller must hold the builder creation flock.
func (v *VM) createBuilder(ctx context.Context, name string) (int, error) {
	hostPort := options.FirstFreeHostPort(builderHostPortBase)
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

// resumeBuilder restarts a stopped builder VM in place. Its owned worker volume
// survives stop/start, so buildkitd returns to a warm BuildKit cache instead of
// an empty one; the recreate path (WithReplace) would discard the volume and
// force the cache to be re-fetched from the registry. A resume failure is not
// fatal: the caller falls back to recreating. The caller must hold the builder
// creation flock.
func (v *VM) resumeBuilder(
	ctx context.Context,
	name string,
	handle msb.SandboxHandle,
	hostPort int,
) error {
	sb, startErr := handle.Start(ctx)
	if startErr != nil {
		return fmt.Errorf("start builder sandbox: %w", startErr)
	}
	if startErr := startBuildkitd(ctx, sb); startErr != nil {
		return startErr
	}
	if recordErr := writeBuilderRecord(name, hostPort); recordErr != nil {
		return recordErr
	}
	pruneResumedWorker(ctx, sb)
	return nil
}

// resumeBuilderPruneAttempts bounds how many times the post-resume prune retries
// while buildkitd finishes starting.
const resumeBuilderPruneAttempts = 20

// pruneResumedWorker bounds the resumed worker cache by size. A resumed worker
// keeps its cache across reaps, so without this it would grow past the volume
// capacity; the registry holds the authoritative persistent cache, so pruning
// the worker by size is safe. Best-effort: buildkitd may still be starting (the
// prune retries briefly), and a failure only defers the bound to a later resume.
func pruneResumedWorker(ctx context.Context, sb msb.Sandbox) {
	for range resumeBuilderPruneAttempts {
		result, err := sb.Shell(ctx, buildctlPruneCommand)
		if err == nil && result.Success() {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(builderDialBackoff):
		}
	}
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

	hostPort, reused, reuseErr := v.reuseBuilderPort(ctx, name)
	if reuseErr != nil {
		return 0, reuseErr
	}
	if reused {
		return hostPort, nil
	}
	return v.createBuilder(ctx, name)
}

// reuseBuilderPort returns a usable port for an existing builder VM, resuming a
// stopped one in place so its warm worker cache survives (a recreate would
// discard the owned volume and force a registry re-import). ok is false when
// there is no reusable builder — missing, no recorded port, or a resume that
// failed — signalling the caller to create a fresh one.
func (v *VM) reuseBuilderPort(ctx context.Context, name string) (int, bool, error) {
	handle, getErr := v.Msb.GetSandbox(ctx, name)
	if getErr != nil {
		if msb.IsNotFound(getErr) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("check builder sandbox %q: %w", name, getErr)
	}
	hostPort, reused, reuseErr := v.reusedBuilderPort(name, handle)
	if reuseErr != nil {
		return 0, false, reuseErr
	}
	if !reused {
		return 0, false, nil
	}
	if msb.IsSandboxActive(handle.Status()) {
		return hostPort, true, nil
	}
	if resumeErr := v.resumeBuilder(ctx, name, handle, hostPort); resumeErr == nil {
		return hostPort, true, nil
	}
	return 0, false, nil
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
	if hostPort > 0 {
		return hostPort, true, nil
	}
	if record, ok := readBuilderRecord(); ok && record.Name == name && record.HostPort > 0 {
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

// pruneCacheRegistry reclaims the embedded cache registry's agents-sandbox cache
// repositories. It is a variable so tests can exercise the prune without a live
// registry.
var pruneCacheRegistry = cacheregistry.Prune

// PruneCache reclaims the shared builder VM's stale cache: the buildkit worker
// cache inside the VM and the embedded registry's agents-sandbox cache repos.
// When no builder VM exists there is nothing to reclaim and it is a no-op.
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

	return errors.Join(pruneBuildkitWorker(ctx, sb), pruneCacheRegistryRepositories(ctx))
}

// pruneBuildkitWorker runs `buildctl prune` inside the builder VM to reclaim its
// worker cache.
func pruneBuildkitWorker(ctx context.Context, sb msb.Sandbox) error {
	result, err := sb.Shell(ctx, buildctlPruneCommand)
	if err != nil {
		return fmt.Errorf("prune buildkit cache: %w", err)
	}
	if !result.Success() {
		return fmt.Errorf("prune buildkit cache: buildctl exited with code %d", result.ExitCode())
	}
	return nil
}

// pruneCacheRegistryRepositories reclaims every agents-sandbox cache repository
// from the embedded registry, which holds the persistent BuildKit layer cache.
func pruneCacheRegistryRepositories(ctx context.Context) error {
	endpoint, err := ensureCacheRegistry(ctx)
	if err != nil {
		return fmt.Errorf("ensure cache registry: %w", err)
	}
	if err := pruneCacheRegistry(ctx, endpoint, isAgentSandboxCacheRepository); err != nil {
		return fmt.Errorf("prune cache registry: %w", err)
	}
	return nil
}

// isAgentSandboxCacheRepository scopes registry pruning to this tool's caches,
// leaving any unrelated repository in the shared registry untouched.
func isAgentSandboxCacheRepository(reference string) bool {
	return strings.HasPrefix(reference, "agents-sandbox/")
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

// startBuildkitd launches buildkitd inside the sandbox and drains its output so
// the stream is not left unread.
func startBuildkitd(ctx context.Context, sb msb.Sandbox) error {
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
