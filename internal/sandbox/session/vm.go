package session

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"gitlab.inoio.de/inoio/opencode-msb/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-msb/internal/git"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/naming"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/options"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/reprovision"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/state"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sysinfo"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

const experimentalWorkspacesValue = "true"

// projectVMName generates the VM name from the project slug.
func projectVMName(slug string) string {
	name := naming.VmPrefix + slug
	if len(name) > options.MaxSandboxNameLen {
		name = name[:options.MaxSandboxNameLen]
	}
	return name
}

// projectPortBindings returns the port bindings to publish on the host for the
// project VM. Serve-only exposes the opencode serve port on the host loopback.
func projectPortBindings(serveOnly bool) []msbSdk.PortBinding {
	if !serveOnly {
		return nil
	}
	return options.ServeOnlyBindings()
}

type vmAction int

const (
	vmActionCreate vmAction = iota
	vmActionConnect
	vmActionStart
)

// defaultVMIdleTimeout moved to internal/sandbox/options.

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

// reconcileResourceConfig applies requested CPUs/memory changes to an existing
// (reused) VM via the SDK Modify API. Requests above the boot-time maximum are
// clamped to that maximum with a warning. Changes within the maximum apply live
// (policy NoRestart); CPU = 0 means "all CPUs" (left unchanged).
//
//nolint:nilerr // Returning nil when config read fails is intentional — treat as no-op
func reconcileResourceConfig(
	ctx context.Context,
	handle msb.SandboxHandle,
	opts options.RunOptions,
	ui termio.UI,
) error {
	cfg, err := handle.Config()
	if err != nil || cfg == nil {
		return nil
	}
	var mo msbSdk.ModifyOptions
	if opts.CPUs != 0 {
		want := opts.CPUs
		if cfg.MaxCPUs > 0 && want > cfg.MaxCPUs {
			ui.Warnf("cpus limited to %d (boot maximum); requested %d", cfg.MaxCPUs, opts.CPUs)
			want = cfg.MaxCPUs
		}
		if want != cfg.CPUs {
			mo.CPUs = want
		}
	}
	if opts.Memory != "" {
		want := options.ParseMemory(opts.Memory)
		if cfg.MaxMemoryMiB > 0 && want > cfg.MaxMemoryMiB {
			ui.Warnf("memory limited to %d MiB (boot maximum); requested %d MiB", cfg.MaxMemoryMiB, want)
			want = cfg.MaxMemoryMiB
		}
		if want != cfg.MemoryMiB {
			mo.MemoryMiB = want
		}
	}
	if mo.CPUs == 0 && mo.MemoryMiB == 0 {
		return nil
	}
	mo.Policy = msbSdk.ModificationPolicyNoRestart
	plan, err := handle.Modify(ctx, mo)
	if err != nil {
		return fmt.Errorf("modify VM resources: %w", err)
	}
	if len(plan.Conflicts) > 0 {
		return fmt.Errorf("resource change not applied: %s", summarizeConflicts(plan.Conflicts))
	}
	return nil
}

func summarizeConflicts(cs []msbSdk.ModificationConflict) string {
	var b []string
	for _, c := range cs {
		b = append(b, c.Field+": "+c.Message)
	}
	return strings.Join(b, "; ")
}

// ensureProjectVM returns a live *msb.Sandbox for the project VM. The boolean
// return is true when the VM was created fresh (first boot or recreation
// after an image change); false when an existing VM was reused (connect or
// start). A per-project host-side flock guards the first-boot race between
// concurrent invocations.
//
//nolint:gocognit,funlen,gocyclo,cyclop // Complex lifecycle logic with multiple paths (connect, start, create) is inherently complex
func ensureProjectVM(
	ctx context.Context,
	opts options.RunOptions,
	imageRef, homeVol, repoPath string,
	imageEnvs map[string]string,
	ui termio.UI,
) (msb.Sandbox, bool, error) {
	if opts.DryRunVM {
		ui.Verbosef("dry-run: VM lifecycle skipped")
		return nil, false, nil
	}

	client := msb.Get()

	slug := git.ProjectSlug(ui)
	name := projectVMName(slug)

	flockPath := filepath.Join(configpaths.GetConfigPaths().UserStateDir(), "vm-ensure", slug+".lock")
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
		if opts.Recreate {
			ui.Verbosef("config changed; replacing project VM %s", name)
			if stopErr := handle.Stop(context.Background()); stopErr != nil {
				ui.Verbosef("stop old VM on reconfig failed (continuing): %v", stopErr)
			}
			if removeErr := handle.Remove(context.Background()); removeErr != nil {
				spin.StopError(removeErr)
				return nil, false, fmt.Errorf("remove old project VM %q: %w", name, removeErr)
			}
			ui.Verbosef("replaced project VM %s; recreating from new config", name)
			notFound = true
			handle = nil
		}
	}

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
				if msb.IsSandboxActive(handle2.Status()) {
					sb2, connErr2 := handle2.Connect(ctx)
					if connErr2 != nil {
						spin.StopError(connErr2)
						return nil, false, fmt.Errorf("connect sandbox %q: %w", name, connErr2)
					}
					spin.Stop()
					if recErr := reconcileResourceConfig(ctx, handle2, opts, ui); recErr != nil {
						ui.Warnf("could not reconcile VM resources: %v", recErr)
					}
					return sb2, false, nil
				}
				sb2, startErr := handle2.Start(ctx)
				if startErr != nil {
					spin.StopError(startErr)
					return nil, false, fmt.Errorf("start sandbox %q: %w", name, startErr)
				}
				spin.Stop()
				if recErr := reconcileResourceConfig(ctx, handle2, opts, ui); recErr != nil {
					ui.Warnf("could not reconcile VM resources: %v", recErr)
				}
				return sb2, false, nil
			}
			spin.Stop()
			ui.Verbosef("connected to existing project VM: %s", name)
			if recErr := reconcileResourceConfig(ctx, handle, opts, ui); recErr != nil {
				ui.Warnf("could not reconcile VM resources: %v", recErr)
			}
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
		if recErr := reconcileResourceConfig(ctx, handle, opts, ui); recErr != nil {
			ui.Warnf("could not reconcile VM resources: %v", recErr)
		}
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
				if recErr := reconcileResourceConfig(ctx, handle, opts, ui); recErr != nil {
					ui.Warnf("could not reconcile VM resources: %v", recErr)
				}
				return sb, false, nil
			}
		}
		sb, startErr := handle.Start(ctx)
		if startErr != nil {
			return nil, false, fmt.Errorf("start sandbox %q: %w", name, startErr)
		}
		if recErr := reconcileResourceConfig(ctx, handle, opts, ui); recErr != nil {
			ui.Warnf("could not reconcile VM resources: %v", recErr)
		}
		return sb, false, nil
	}
	if !msbSdk.IsKind(err, msbSdk.ErrSandboxNotFound) {
		return nil, false, fmt.Errorf("re-check sandbox %q: %w", name, err)
	}

	sb, created, err := createProjectVM(ctx, client, name, imageRef, homeVol, repoPath, opts, imageEnvs, ui)
	if err != nil {
		return nil, false, err
	}
	return sb, created, nil
}

func createProjectVM(
	ctx context.Context,
	client msb.Client,
	name, imageRef, homeVol, repoPath string,
	opts options.RunOptions,
	imageEnvs map[string]string,
	ui termio.UI,
) (msb.Sandbox, bool, error) {
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

	envMap := reprovision.MergeEnvMaps(
		reprovision.BuildEnvMap(configpaths.GetConfigPaths().UserEnvFile()),
		reprovision.BuildEnvMap(configpaths.GetConfigPaths().ProjectEnvFile()),
	)
	ui.Verbosef("adding docker env definitions to project VM environment: %s", imageEnvs)
	buildProjectVMEnv(envMap, imageEnvs)

	secrets := reprovision.BuildSecretsFromSpecs(reprovision.MergeSecretSpecs(
		reprovision.ParseSecretSpecLegacy(configpaths.GetConfigPaths().UserEnvSecretFile(), ui),
		reprovision.ParseSecretSpecLegacy(configpaths.GetConfigPaths().ProjectEnvSecretFile(), ui),
		reprovision.ParseSecretSpecYAML(configpaths.GetConfigPaths().UserEnvSecretYAMLFile(), ui),
		reprovision.ParseSecretSpecYAML(configpaths.GetConfigPaths().ProjectEnvSecretYAMLFile(), ui),
	), ui)

	mounts := buildMounts(homeVol, repoPath, options.ResolveTmpSizeMiB(opts.TmpSize))

	spin := ui.Spinner("Starting project VM")
	idleTimeout := opts.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = options.DefaultVMIdleTimeout
	}
	optsList := []msbSdk.SandboxOption{
		msbSdk.WithImage(imageRef),
		msbSdk.WithMounts(mounts),
		msbSdk.WithSecrets(secrets...),
		msbSdk.WithEnv(envMap),
		msbSdk.WithUser(user),
		msbSdk.WithWorkdir(defaultTargetDir),
		msbSdk.WithCPUs(cpus),
		msbSdk.WithMaxCPUs(numCPUs),
		msbSdk.WithMemory(options.ParseMemory(opts.Memory)),
		//nolint:gosec // G115: maxMemoryGiB is physical RAM in GiB, cannot overflow uint32
		msbSdk.WithMaxMemory(options.ParseMemoryGiB(uint32(maxMemoryGiB))),
		msbSdk.WithDetached(),
		msbSdk.WithIdleTimeout(idleTimeout),
		msbSdk.WithReplace(),
	}
	if opts.DiskSize != "" {
		optsList = append(optsList, msbSdk.WithRootDisk(msbSdk.RootDisk.Managed(options.ParseMemory(opts.DiskSize))))
	}
	if opts.ServeOnly {
		optsList = append(optsList, msbSdk.WithPortBindings(projectPortBindings(true)...))
	}
	sb, err := client.CreateSandbox(ctx, name, optsList...)
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
	if err := state.FlockExclusive(f); err != nil {
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
	client msb.Client,
	stopFn func(msb.SandboxHandle, context.Context) error,
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
		pastTense = "stopped"
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
		func(h msb.SandboxHandle, c context.Context) error { return h.Stop(c) })
}

// KillProjectVM force-kills the project VM for the current directory.
// If remove is true, it also removes the VM's persisted state after killing.
func KillProjectVM(ctx context.Context, remove, dryRun bool, ui termio.UI) error {
	return stopOrKillProjectVM(ctx, remove, dryRun, ui, "kill", "Force-killing", msb.Get(),
		func(h msb.SandboxHandle, c context.Context) error { return h.Kill(c) })
}
