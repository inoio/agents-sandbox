package sandbox

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gitlab.inoio.de/inoio/opencode-msb/internal/git"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sysinfo"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

const experimentalWorkspacesValue = "true"

// projectVMName generates the VM name from the project slug.
// Note: truncation by bytes is safe because ProjectSlug sanitizes to ASCII.
func projectVMName(slug string) string {
	name := vmPrefix + slug
	if len(name) > maxSandboxNameLen {
		name = name[:maxSandboxNameLen]
	}
	return name
}

type vmAction int

const (
	vmActionCreate vmAction = iota
	vmActionConnect
	vmActionStart
)

const defaultVMIdleTimeout = 30 * time.Second

func buildProjectVMEnv(envMap map[string]string, imageEnvs map[string]string) {
	// Merge env vars baked into the Docker image (set via Dockerfile ENV directives).
	// These are parsed by docker.ImageInspect at build time and include everything
	// from base images (debian defaults) through custom Dockerfile ENVs.
	maps.Copy(envMap, imageEnvs)
	if _, ok := envMap["PATH"]; !ok {
		// Fallback: if image env does not provide a PATH, inherit from the
		// host. This covers the case where the Dockerfile has no ENV
		// directives OR the image was pruned with no stored metadata.
		for _, e := range os.Environ() {
			if i := strings.Index(e, "="); i > 0 {
				key := e[:i]
				if _, exist := envMap[key]; !exist {
					envMap[key] = e[i+1:]
				}
			}
		}
	}
	envMap["OPENCODE_EXPERIMENTAL_WORKSPACES"] = experimentalWorkspacesValue
}

// decideVMAction maps a GetSandbox result to the lifecycle action.
// notFound=true means the sandbox doesn't exist → create.
// Otherwise the status determines connect (running) vs start (stopped/crashed).
//
//nolint:nilerr // Returning nil error for notFoundErr is expected behavior - not found means create
func decideVMAction(notFoundErr error, status msbSdk.SandboxStatus) (vmAction, error) {
	if notFoundErr != nil {
		return vmActionCreate, nil
	}
	switch status {
	case msbSdk.SandboxStatusRunning, msbSdk.SandboxStatusDraining, msbSdk.SandboxStatusPaused:
		return vmActionConnect, nil
	case msbSdk.SandboxStatusStopped, msbSdk.SandboxStatusCrashed:
		return vmActionStart, nil
	}
	return vmActionCreate, fmt.Errorf("unexpected sandbox status: %s", status)
}

// EnsureProjectVM returns a live *Sandbox for the project VM. The boolean
// return is true when the VM was created fresh (first boot); false when an
// existing VM was reused (connect or start). A per-project host-side flock
// guards the first-boot race between concurrent invocations.
//
//nolint:gocognit,funlen // Complex lifecycle logic with multiple paths (connect, start, create) is inherently complex
func EnsureProjectVM(
	ctx context.Context,
	opts RunOptions,
	cfg Config,
	imageRef, homeVol, repoPath string,
	imageEnvs map[string]string,
	ui termio.UI,
) (Sandbox, bool, error) {
	if opts.DryRunVM {
		ui.Verbosef("dry-run: VM lifecycle skipped")
		return nil, false, nil
	}

	client := msb.Get()

	slug := git.ProjectSlug(ui)
	name := projectVMName(slug)

	flockPath := filepath.Join(cfg.StateDir, "vm-ensure", slug+".lock")
	if err := os.MkdirAll(filepath.Dir(flockPath), 0o750); err != nil {
		return nil, false, fmt.Errorf("create flock dir: %w", err)
	}

	spin := ui.Spinner("Checking project VM")

	handle, err := client.GetSandbox(ctx, name)
	notFound := err != nil && msbSdk.IsKind(err, msbSdk.ErrSandboxNotFound)
	if err != nil && !notFound {
		spin.StopError(err)
		return nil, false, fmt.Errorf("check sandbox %q: %w", name, err)
	}

	// Fast path: VM is already running → connect without flock.
	//nolint:nestif // Complex nested logic for handling connect/start/retry is necessary for lifecycle management
	if !notFound {
		action, actionErr := decideVMAction(nil, handle.Status())
		if actionErr != nil {
			spin.StopError(actionErr)
			return nil, false, actionErr
		}
		if action == vmActionConnect {
			sb, connErr := handle.Connect(ctx)
			if connErr != nil {
				// Idle-timeout race: the VM may have auto-stopped between
				// GetSandbox and Connect. Retry once via Start.
				ui.Verbosef("connect failed (%v), retrying via Start", connErr)
				handle2, refreshErr := handle.Refresh(ctx)
				if refreshErr != nil {
					spin.StopError(refreshErr)
					return nil, false, fmt.Errorf(
						"connect sandbox %q (refresh after connect failure): %w",
						name,
						refreshErr,
					)
				}
				if isSandboxActive(handle2.Status()) {
					sb2, connErr2 := handle2.Connect(ctx)
					if connErr2 != nil {
						spin.StopError(connErr2)
						return nil, false, fmt.Errorf("connect sandbox %q: %w", name, connErr2)
					}
					spin.Stop()
					return sb2, false, nil
				}
				sb2, startErr := handle2.Start(ctx)
				if startErr != nil {
					spin.StopError(startErr)
					return nil, false, fmt.Errorf("start sandbox %q: %w", name, startErr)
				}
				spin.Stop()
				return sb2, false, nil
			}
			spin.Stop()
			ui.Verbosef("connected to existing project VM: %s", name)
			return sb, false, nil
		}
		spin.Stop()
		// Stopped/crashed → start (no flock needed, Start is idempotent enough).
		sb, startErr := handle.Start(ctx)
		if startErr != nil {
			spin.StopError(startErr)
			return nil, false, fmt.Errorf("start sandbox %q: %w", name, startErr)
		}
		ui.Verbosef("started existing project VM: %s", name)
		return sb, false, nil
	}

	spin.Stop()

	// Ensure runtime is available before acquiring the flock to reduce contention.
	if ensureErr := client.EnsureInstalled(ctx); ensureErr != nil {
		return nil, false, fmt.Errorf("microsandbox runtime: %w", ensureErr)
	}

	// Slow path: VM doesn't exist → create. Hold a flock so concurrent
	// invocations don't both create (and clobber via WithReplace).
	release, lockErr := acquireProjectFlock(flockPath)
	if lockErr != nil {
		return nil, false, fmt.Errorf("acquire project flock: %w", lockErr)
	}
	defer release()

	// Re-check after acquiring the flock — another invocation may have created it.
	handle, err = client.GetSandbox(ctx, name)
	//nolint:nestif // Nested logic for handling post-lock connect/start is necessary
	if err == nil {
		// Someone else created it while we waited for the lock.
		action, actionErr := decideVMAction(nil, handle.Status())
		if actionErr != nil {
			return nil, false, actionErr
		}
		if action == vmActionConnect {
			sb, connErr := handle.Connect(ctx)
			if connErr != nil {
				ui.Verbosef("post-lock connect failed: %v", connErr)
			}
			if sb != nil {
				return sb, false, nil
			}
		}
		sb, startErr := handle.Start(ctx)
		if startErr != nil {
			return nil, false, fmt.Errorf("start sandbox %q: %w", name, startErr)
		}
		return sb, false, nil
	}
	if !msbSdk.IsKind(err, msbSdk.ErrSandboxNotFound) {
		return nil, false, fmt.Errorf("re-check sandbox %q: %w", name, err)
	}

	sb, created, err := createProjectVM(ctx, client, name, imageRef, homeVol, repoPath, opts, cfg, imageEnvs, ui)
	if err != nil {
		return nil, false, err
	}
	return sb, created, nil
}

func createProjectVM(
	ctx context.Context,
	client MsbClient,
	name, imageRef, homeVol, repoPath string,
	opts RunOptions,
	cfg Config,
	imageEnvs map[string]string,
	ui termio.UI,
) (Sandbox, bool, error) {
	user := opts.User
	if user == "" {
		user = "dev"
	}
	cpus := opts.CPUs
	numCPUs := sysinfo.NumCPUs()
	if cpus == 0 {
		cpus = numCPUs
	}
	maxMemoryGiB := sysinfo.TotalMemoryGiB()

	envMap := mergeEnvMaps(
		buildEnvMap(filepath.Join(cfg.UserLauncherDir, "env")),
		buildEnvMap(projEnvFile),
	)
	ui.Verbosef("adding docker env definitions to project VM environment: %s", imageEnvs)
	buildProjectVMEnv(envMap, imageEnvs)

	secrets := BuildSecrets(mergeEnvMaps(
		buildEnvMap(filepath.Join(cfg.UserLauncherDir, "env.secret")),
		buildEnvMap(projEnvSecretFile),
	), ui)

	mounts := buildMounts(homeVol, repoPath, resolveTmpSizeMiB(opts.TmpSize))

	spin := ui.Spinner("Starting project VM")
	sb, err := client.CreateSandbox(ctx, name,
		msbSdk.WithImage(imageRef),
		msbSdk.WithMounts(mounts),
		msbSdk.WithSecrets(secrets...),
		msbSdk.WithEnv(envMap),
		msbSdk.WithUser(user),
		msbSdk.WithWorkdir("/workspace"),
		msbSdk.WithCPUs(cpus),
		msbSdk.WithMaxCPUs(numCPUs),
		msbSdk.WithMemory(parseMemory(opts.Memory)),
		//nolint:gosec // G115: maxMemoryGiB is physical RAM in GiB, cannot overflow uint32
		msbSdk.WithMaxMemory(uint32(maxMemoryGiB)*mibPerGib),
		msbSdk.WithDetached(),
		msbSdk.WithIdleTimeout(defaultVMIdleTimeout),
		msbSdk.WithReplace(),
	)
	if err != nil {
		spin.StopError(err)
		return nil, false, fmt.Errorf("create sandbox: %w", err)
	}
	spin.Stop()
	ui.Verbosef("created new project VM: %s", name)
	return sb, true, nil
}

// acquireProjectFlock takes an exclusive flock on the given path. It returns a
// release function. The flock prevents two concurrent invocations from both
// creating a project VM (which would clobber via WithReplace).
func acquireProjectFlock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open flock %s: %w", path, err)
	}
	if err := flockExclusive(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("flock %s: %w", path, err)
	}
	return func() {
		_ = f.Close()
	}, nil
}

// stopOrKillProjectVM is the shared implementation for StopProjectVM and KillProjectVM.
// action is "stop" or "kill", actionVerb is used in user-facing messages.
func stopOrKillProjectVM(
	ctx context.Context,
	remove bool,
	dryRun bool,
	ui termio.UI,
	action, actionVerb string,
	client MsbClient,
	stopFn func(SandboxHandle, context.Context) error,
) error {
	slug := git.ProjectSlug(ui)
	name := projectVMName(slug)

	handle, err := client.GetSandbox(ctx, name)
	if err != nil {
		if msbSdk.IsKind(err, msbSdk.ErrSandboxNotFound) {
			ui.Infof("no project VM found: %s", name)
			return nil
		}
		return fmt.Errorf("get sandbox %q: %w", name, err)
	}

	if dryRun {
		actionWord := "Would stop"
		if action == "kill" {
			actionWord = "Would kill"
		}
		if remove {
			ui.Infof("dry-run: %s project VM: %s (also would remove persisted state)", actionWord, name)
		} else {
			ui.Infof("dry-run: %s project VM: %s", actionWord, name)
		}
		return nil
	}

	spin := ui.Spinnerf("%s project VM", actionVerb)
	if err := stopFn(handle, ctx); err != nil {
		spin.StopError(err)
		return fmt.Errorf("%s sandbox %q: %w", action, name, err)
	}
	spin.Stop()
	pastTense := action + "ed"
	if action == "stop" {
		pastTense = "stopped" //nolint:goconst // singular English spelling fix
	}
	ui.Infof("%s project VM: %s", pastTense, name)

	if remove {
		if err := handle.Remove(ctx); err != nil {
			ui.Warnf("failed to remove sandbox state: %v", err)
		} else {
			ui.Verbosef("persisted state removed: %s", name)
		}
	}
	return nil
}

// StopProjectVM gracefully stops the project VM for the current directory.
// If remove is true, it also removes the VM's persisted state after stopping.
func StopProjectVM(ctx context.Context, remove, dryRun bool, ui termio.UI) error {
	return stopOrKillProjectVM(ctx, remove, dryRun, ui, "stop", "Stopping", msb.Get(),
		func(h SandboxHandle, c context.Context) error { return h.Stop(c) })
}

// KillProjectVM force-kills the project VM for the current directory.
// If remove is true, it also removes the VM's persisted state after killing.
func KillProjectVM(ctx context.Context, remove, dryRun bool, ui termio.UI) error {
	return stopOrKillProjectVM(ctx, remove, dryRun, ui, "kill", "Force-killing", msb.Get(),
		func(h SandboxHandle, c context.Context) error { return h.Kill(c) })
}
