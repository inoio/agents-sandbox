package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gitlab.inoio.de/inoio/opencode-msb/internal/git"
	"gitlab.inoio.de/inoio/opencode-msb/internal/output"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sysinfo"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

const projectVMPrefix = "opencode-msb-vm-"

// projectVMName generates the VM name from the project slug.
// Note: truncation by bytes is safe because ProjectSlug sanitizes to ASCII.
func projectVMName(slug string) string {
	name := projectVMPrefix + slug
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

func buildProjectVMEnv(envMap map[string]string) {
	envMap["OPENCODE_EXPERIMENTAL_WORKSPACES"] = "true"
}

// decideVMAction maps a GetSandbox result to the lifecycle action.
// notFound=true means the sandbox doesn't exist → create.
// Otherwise the status determines connect (running) vs start (stopped/crashed).
//
//nolint:nilerr // Returning nil error for notFoundErr is expected behavior - not found means create
func decideVMAction(notFoundErr error, status msb.SandboxStatus) (vmAction, error) {
	if notFoundErr != nil {
		return vmActionCreate, nil
	}
	switch status {
	case msb.SandboxStatusRunning, msb.SandboxStatusDraining, msb.SandboxStatusPaused:
		return vmActionConnect, nil
	case msb.SandboxStatusStopped, msb.SandboxStatusCrashed:
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
	logger *output.Printer,
) (*msb.Sandbox, bool, error) {
	slug := git.ProjectSlug(logger)
	name := projectVMName(slug)

	flockPath := filepath.Join(cfg.StateDir, "vm-ensure", slug+".lock")
	if err := os.MkdirAll(filepath.Dir(flockPath), 0o750); err != nil {
		return nil, false, fmt.Errorf("create flock dir: %w", err)
	}

	spin := output.NewSpinner(logger)
	spin.Start("Checking project VM")

	handle, err := msb.GetSandbox(ctx, name)
	notFound := err != nil && msb.IsKind(err, msb.ErrSandboxNotFound)
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
				logger.Debugf("connect failed (%v), retrying via Start", connErr)
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
			logger.Debugf("connected to existing project VM: %s", name)
			return sb, false, nil
		}
		spin.Stop()
		// Stopped/crashed → start (no flock needed, Start is idempotent enough).
		sb, startErr := handle.Start(ctx)
		if startErr != nil {
			spin.StopError(startErr)
			return nil, false, fmt.Errorf("start sandbox %q: %w", name, startErr)
		}
		logger.Debugf("started existing project VM: %s", name)
		return sb, false, nil
	}

	spin.Stop()

	// Ensure runtime is available before acquiring the flock to reduce contention.
	if ensureErr := msb.EnsureInstalled(ctx); ensureErr != nil {
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
	handle, err = msb.GetSandbox(ctx, name)
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
				logger.Debugf("post-lock connect failed: %v", connErr)
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
	if !msb.IsKind(err, msb.ErrSandboxNotFound) {
		return nil, false, fmt.Errorf("re-check sandbox %q: %w", name, err)
	}

	sb, created, err := createProjectVM(ctx, name, imageRef, homeVol, repoPath, opts, cfg, logger)
	if err != nil {
		return nil, false, err
	}
	return sb, created, nil
}

func createProjectVM(
	ctx context.Context,
	name, imageRef, homeVol, repoPath string,
	opts RunOptions,
	cfg Config,
	logger *output.Printer,
) (*msb.Sandbox, bool, error) {
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
		buildEnvMap(".opencode-msb/env"),
	)
	buildProjectVMEnv(envMap)

	secrets := BuildSecrets(mergeEnvMaps(
		buildEnvMap(filepath.Join(cfg.UserLauncherDir, "env.secret")),
		buildEnvMap(".opencode-msb/env.secret"),
	), logger)

	mounts := buildMounts(homeVol, repoPath, resolveTmpSizeMiB(opts.TmpSize))

	spin := output.NewSpinner(logger)
	spin.Start("Starting project VM")
	sb, err := msb.CreateSandbox(ctx, name,
		msb.WithImage(imageRef),
		msb.WithMounts(mounts),
		msb.WithSecrets(secrets...),
		msb.WithEnv(envMap),
		msb.WithUser(user),
		msb.WithWorkdir("/workspace"),
		msb.WithCPUs(cpus),
		msb.WithMaxCPUs(numCPUs),
		msb.WithMemory(parseMemory(opts.Memory)),
		//nolint:gosec // G115: maxMemoryGiB is physical RAM in GiB, cannot overflow uint32
		msb.WithMaxMemory(uint32(maxMemoryGiB)*mibPerGib),
		msb.WithDetached(),
		msb.WithIdleTimeout(defaultVMIdleTimeout),
		msb.WithReplace(),
	)
	if err != nil {
		spin.StopError(err)
		return nil, false, fmt.Errorf("create sandbox: %w", err)
	}
	spin.Stop()
	logger.Debugf("created new project VM: %s", name)
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
	logger *output.Printer,
	action, actionVerb string,
	stopFn func(*msb.SandboxHandle, context.Context) error,
) error {
	slug := git.ProjectSlug(logger)
	name := projectVMName(slug)

	handle, err := msb.GetSandbox(ctx, name)
	if err != nil {
		if msb.IsKind(err, msb.ErrSandboxNotFound) {
			logger.Infof("no project VM found: %s", name)
			return nil
		}
		return fmt.Errorf("get sandbox %q: %w", name, err)
	}

	spin := output.NewSpinner(logger)
	spin.Start(actionVerb + " project VM")
	if err := stopFn(handle, ctx); err != nil {
		spin.StopError(err)
		return fmt.Errorf("%s sandbox %q: %w", action, name, err)
	}
	spin.Stop()
	logger.Infof("%s project VM: %s", action+"ed", name)

	if remove {
		if err := handle.Remove(ctx); err != nil {
			logger.Warnf("failed to remove sandbox state: %v", err)
		}
	}
	return nil
}

// StopProjectVM gracefully stops the project VM for the current directory.
// If remove is true, it also removes the VM's persisted state after stopping.
func StopProjectVM(ctx context.Context, remove bool, logger *output.Printer) error {
	return stopOrKillProjectVM(ctx, remove, logger, "stop", "Stopping",
		func(h *msb.SandboxHandle, c context.Context) error { return h.Stop(c) })
}

// KillProjectVM force-kills the project VM for the current directory.
// If remove is true, it also removes the VM's persisted state after killing.
func KillProjectVM(ctx context.Context, remove bool, logger *output.Printer) error {
	return stopOrKillProjectVM(ctx, remove, logger, "kill", "Force-killing",
		func(h *msb.SandboxHandle, c context.Context) error { return h.Kill(c) })
}
