package vm

import (
	"context"
	"fmt"
	"time"

	"github.com/inoio/agents-sandbox/internal/hostlock"

	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sysinfo"
	"github.com/inoio/agents-sandbox/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// projectPortBindings returns the port bindings to publish on the host for the
// project VM. Serve-only exposes the agent serve port on the host loopback.
func projectPortBindings(serveOnly bool, hostPort int) []msbSdk.PortBinding {
	if !serveOnly {
		return nil
	}
	return options.ServeOnlyBindings(hostPort)
}

type vmAction int

const (
	vmActionCreate vmAction = iota
	vmActionConnect
	vmActionStart
	vmActionWait
)

// vmBoot records how the project VM entered the running state this run.
type vmBoot int

const (
	// vmBootConnected means the VM was already running and was merely attached to.
	vmBootConnected vmBoot = iota
	// vmBootStarted means this invocation booted the VM from a created, stopped,
	// crashed, or failed-start state.
	vmBootStarted
	// vmBootCreated means the VM was freshly created (first boot or recreation).
	vmBootCreated
)

// booted reports whether the VM transitioned to running this run (started or
// created) rather than being already-running when attached to.
func (b vmBoot) booted() bool {
	return b == vmBootStarted || b == vmBootCreated
}

// defaultVMIdleTimeout moved to internal/sandbox/options.

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
	case msbSdk.SandboxStatusCreated, msbSdk.SandboxStatusStopped, msbSdk.SandboxStatusCrashed:
		return vmActionStart, nil
	case msbSdk.SandboxStatusStarting:
		return vmActionWait, nil
	case msbSdk.SandboxStatusRunning:
		return vmActionConnect, nil
	case msbSdk.SandboxStatusDraining, msbSdk.SandboxStatusPaused:
		return vmActionCreate, fmt.Errorf("sandbox %q is not available for a new client: status is %q", status, status)
	default:
		return vmActionCreate, fmt.Errorf("unexpected sandbox status: %q", status)
	}
}

// acquireProjectVMLock takes the per-(slug, agent) creation lock that serializes
// a project VM's create and destroy. It is a behavior seam so tests can observe
// the lock boundary; production uses the shared host flock.
var acquireProjectVMLock = func(k state.Key) (func(), error) {
	return hostlock.AcquireExclusive(state.ProjectVMLockDir(k), state.ProjectVMLockName)
}

// ensureProjectVM returns a live *msb.Sandbox for the project VM and how it
// entered the running state this run: vmBootCreated on first boot or
// recreation, vmBootStarted when this invocation booted an existing VM, or
// vmBootConnected when an already-running VM was merely attached to (and when
// skipVM leaves the lifecycle alone). A per-project host-side flock guards the
// first-boot race and the recreate destroy between concurrent invocations.
func ensureProjectVM(
	ctx context.Context,
	opts options.RunOptions,
	info image.ImageInfo, homeVol, repoPath string,
	k state.Key,
	repoSlug string,
	skipVM bool,
	env envsecret.EnvSecrets,
	ui termio.UI,
) (msb.Sandbox, vmBoot, error) {
	if skipVM {
		ui.Verbosef("dry-run: VM lifecycle skipped")
		return nil, vmBootConnected, nil
	}

	client := msb.Get()

	name := projectVMName(k)

	spin := ui.Spinner("Checking project VM")

	handle, found, err := findProjectVM(ctx, client, k)
	if err != nil {
		spin.StopError(err)
		return nil, vmBootConnected, fmt.Errorf("check sandbox %q: %w", name, err)
	}
	if found {
		name = handle.Name()
	}

	// Non-mutating fast path: attach to, wait for, or start an existing VM
	// without the flock. A recreate is mutating, so it always takes the lock.
	if found && !opts.Recreate {
		return resolveExistingVM(ctx, handle, opts, name, ui, spin)
	}

	spin.Stop()

	// Ensure runtime is available before acquiring the flock to reduce contention.
	if ensureErr := client.EnsureInstalled(ctx); ensureErr != nil {
		return nil, vmBootConnected, fmt.Errorf("microsandbox runtime: %w", ensureErr)
	}

	// Slow path: hold the per-project flock so concurrent invocations don't both
	// create (and clobber via WithReplace), and so a recreate destroy cannot race
	// a VM another invocation just created.
	release, lockErr := acquireProjectVMLock(k)
	if lockErr != nil {
		return nil, vmBootConnected, fmt.Errorf("acquire project flock: %w", lockErr)
	}
	defer release()

	// Recreate: destroy the handle this session held, under the lock. The
	// destroy keeps context.Background() — a cancelled run must not leave a
	// half-replaced VM — and the pre-lock handle: per specs/sandbox-identity.md
	// the launcher never re-looks-up the name to adopt a replacement.
	if found {
		alreadyGone, destroyErr := destroyProjectVMHandle(context.Background(), handle, name, "rerun to reconfigure it")
		if destroyErr != nil {
			// gomutants:disable-next-line RETURN_ZERO reason="vmBootConnected is the vmBoot zero value (it is the first iota constant), so a RETURN_ZERO replacement of vmBootConnected is the identical constant"
			return nil, vmBootConnected, destroyErr
		}
		if alreadyGone {
			ui.Verbosef("project VM %s was already gone; recreating from new config", name)
		} else {
			ui.Verbosef("replaced project VM %s; recreating from new config", name)
		}
		// The replaced VM may have carried a legacy name; recreate under the
		// prefix-free scheme so the rename takes effect.
		name = projectVMName(k)
	}

	return createOrAttachAfterLock(ctx, client, name, k, repoSlug, info, homeVol, repoPath, opts, env, ui)
}

// destroyProjectVMHandle destroys a project VM handle identity-safely and
// classifies the outcome for every VM teardown path, reporting whether the name
// was already free before the request and returning the failure otherwise.
//
// A handle whose identity was replaced fails closed: its name now refers to a
// different persisted sandbox than the one this handle was resolved for, so
// destroying it would stop-and-remove a stranger's VM. The error says so and
// carries onReplaced, the caller's own instruction for what to do next, because
// replacing a reconfigured VM and completing a legacy identity adoption ask the
// user different things. An already-gone name is a success reported through the
// boolean so callers can log it distinctly; any other destroy failure is
// returned as-is.
func destroyProjectVMHandle(
	ctx context.Context,
	handle msb.SandboxHandle,
	name, onReplaced string,
) (bool, error) {
	switch destroyErr := handle.Destroy(ctx); {
	case msb.IsReplaced(destroyErr):
		return false, fmt.Errorf(
			"project VM %q was replaced by a different sandbox since it was checked; %s: %w",
			name,
			onReplaced,
			destroyErr,
		)
	case msb.IsNotFound(destroyErr):
		return true, nil
	case destroyErr != nil:
		return false, fmt.Errorf("destroy old project VM %q: %w", name, destroyErr)
	default:
		return false, nil
	}
}

// resolveExistingVM brings an already-existing VM to a connected state: it
// attaches to a running VM, converges a VM another caller is starting, or starts
// a created/stopped/crashed one. A connect failure is always recovered through
// refresh-and-resume (connectRunningVM), whichever caller resolved the handle.
//
// The handle is resolved by the caller — before the flock on the fast path, or
// under it after the post-lock re-check. spin is the lookup's active spinner,
// which only the pre-lock path has; the post-lock re-check passes nil, and the
// transition reports through ui alone.
func resolveExistingVM(
	ctx context.Context,
	handle msb.SandboxHandle,
	opts options.RunOptions,
	name string,
	ui termio.UI,
	spin termio.Spinner,
) (msb.Sandbox, vmBoot, error) {
	action, actionErr := decideVMAction(nil, handle.Status())
	if actionErr != nil {
		stopSpinnerError(spin, actionErr)
		return nil, vmBootConnected, actionErr
	}
	if action == vmActionConnect {
		return connectRunningVM(ctx, handle, opts, name, ui, spin)
	}
	if action == vmActionWait {
		sb, boot, waitErr := convergeExistingVM(ctx, handle)
		if waitErr != nil {
			stopSpinnerError(spin, waitErr)
			return nil, vmBootConnected, fmt.Errorf("wait for sandbox %q to start: %w", name, waitErr)
		}
		stopSpinner(spin)
		reconcileResources(ctx, handle, opts, ui)
		return sb, boot, nil
	}
	stopSpinner(spin)
	// Created/stopped/crashed → start. The helper converges if another caller
	// wins the start transition.
	sb, boot, startErr := startExistingVM(ctx, handle)
	if startErr != nil {
		stopSpinnerError(spin, startErr)
		return nil, vmBootConnected, fmt.Errorf("start sandbox %q: %w", name, startErr)
	}
	ui.Infof("started existing project VM: %s", name)
	reconcileResources(ctx, handle, opts, ui)
	return sb, boot, nil
}

// stopSpinner finishes a lookup spinner when one is active. A nil spinner means
// the handle was resolved without one (the post-lock re-check).
func stopSpinner(spin termio.Spinner) {
	if spin != nil {
		spin.Stop()
	}
}

// stopSpinnerError finishes a lookup spinner with err when one is active.
func stopSpinnerError(spin termio.Spinner, err error) {
	if spin != nil {
		spin.StopError(err)
	}
}

// connectRunningVM connects to a running VM, retrying once via Start when the
// VM auto-stopped between the status check and Connect.
func connectRunningVM(
	ctx context.Context,
	handle msb.SandboxHandle,
	opts options.RunOptions,
	name string,
	ui termio.UI,
	spin termio.Spinner,
) (msb.Sandbox, vmBoot, error) {
	sb, connErr := handle.Connect(ctx)
	if connErr != nil {
		return resumeAfterConnectFailure(ctx, handle, opts, name, ui, spin, connErr)
	}
	stopSpinner(spin)
	ui.Infof("connected to existing project VM: %s", name)
	reconcileResources(ctx, handle, opts, ui)
	return sb, vmBootConnected, nil
}

// resumeAfterConnectFailure handles the idle-timeout race where the VM
// auto-stopped between the status check and Connect.
func resumeAfterConnectFailure(
	ctx context.Context,
	handle msb.SandboxHandle,
	opts options.RunOptions,
	name string,
	ui termio.UI,
	spin termio.Spinner,
	connErr error,
) (msb.Sandbox, vmBoot, error) {
	ui.Verbosef("connect failed (%v), retrying via Start", connErr)
	handle2, refreshErr := handle.Refresh(ctx)
	if refreshErr != nil {
		stopSpinnerError(spin, refreshErr)
		return nil, vmBootConnected, fmt.Errorf(
			"connect sandbox %q (refresh after connect failure): %w",
			name,
			refreshErr,
		)
	}
	retryStatus := handle2.Status()
	if _, retryActionErr := decideVMAction(nil, retryStatus); retryActionErr != nil {
		stopSpinnerError(spin, retryActionErr)
		return nil, vmBootConnected, retryActionErr
	}
	sb2, retryBoot, resumeErr := resumeSandboxByStatus(ctx, handle2, retryStatus, name)
	if resumeErr != nil {
		stopSpinnerError(spin, resumeErr)
		return nil, vmBootConnected, fmt.Errorf("resume sandbox %q: %w", name, resumeErr)
	}
	stopSpinner(spin)
	reconcileResources(ctx, handle2, opts, ui)
	return sb2, retryBoot, nil
}

// resumeSandboxByStatus brings a sandbox to a connected state based on its
// current lifecycle status.
func resumeSandboxByStatus(
	ctx context.Context,
	handle msb.SandboxHandle,
	status msbSdk.SandboxStatus,
	name string,
) (msb.Sandbox, vmBoot, error) {
	switch status {
	case msbSdk.SandboxStatusRunning:
		sb, err := handle.Connect(ctx)
		return sb, vmBootConnected, err
	case msbSdk.SandboxStatusStarting:
		return convergeExistingVM(ctx, handle)
	case msbSdk.SandboxStatusCreated, msbSdk.SandboxStatusStopped, msbSdk.SandboxStatusCrashed:
		return startExistingVM(ctx, handle)
	case msbSdk.SandboxStatusDraining, msbSdk.SandboxStatusPaused:
		return nil, vmBootConnected, fmt.Errorf("resume sandbox %q: status %q is not available", name, status)
	}
	return nil, vmBootConnected, fmt.Errorf("resume sandbox %q: unexpected status %q", name, status)
}

// createOrAttachAfterLock re-checks for the VM after the flock was acquired,
// attaching to one another invocation created, or creating it.
func createOrAttachAfterLock(
	ctx context.Context,
	client msb.Client,
	name string,
	key state.Key,
	repoSlug string,
	info image.ImageInfo, homeVol, repoPath string,
	opts options.RunOptions,
	env envsecret.EnvSecrets,
	ui termio.UI,
) (msb.Sandbox, vmBoot, error) {
	handle, err := client.GetSandbox(ctx, name)
	if err == nil {
		return resolveExistingVM(ctx, handle, opts, name, ui, nil)
	}
	if !msb.IsNotFound(err) {
		return nil, vmBootConnected, fmt.Errorf("re-check sandbox %q: %w", name, err)
	}

	sb, created, err := createProjectVM(ctx, client, name, key, repoSlug, info, homeVol, repoPath, opts, env, ui)
	if err != nil {
		return nil, vmBootConnected, err
	}
	boot := vmBootConnected
	if created {
		boot = vmBootCreated
	}
	return sb, boot, nil
}

// reconcileResources best-effort reconciles live VM resources with opts.
func reconcileResources(ctx context.Context, handle msb.SandboxHandle, opts options.RunOptions, ui termio.UI) {
	if recErr := reconcileResourceConfig(ctx, handle, opts, ui); recErr != nil {
		ui.Warnf("could not reconcile VM resources: %v", recErr)
	}
}

// sandboxLifecyclePollInterval is the delay between status refreshes while a
// sandbox is starting. It is a variable so tests can shrink it instead of
// waiting out the production interval.
var sandboxLifecyclePollInterval = 100 * time.Millisecond

func startExistingVM(ctx context.Context, handle msb.SandboxHandle) (msb.Sandbox, vmBoot, error) {
	sb, err := handle.Start(ctx)
	if err == nil {
		return sb, vmBootStarted, nil
	}
	if !msbSdk.IsKind(err, msbSdk.ErrSandboxStillRunning) {
		return nil, vmBootConnected, err
	}
	current, refreshErr := handle.Refresh(ctx)
	if refreshErr != nil {
		return nil, vmBootConnected, refreshErr
	}
	if current.Status() != msbSdk.SandboxStatusStarting && current.Status() != msbSdk.SandboxStatusRunning {
		return nil, vmBootConnected, err
	}
	return convergeExistingVM(ctx, current)
}

func convergeExistingVM(ctx context.Context, handle msb.SandboxHandle) (msb.Sandbox, vmBoot, error) {
	for {
		current, err := handle.Refresh(ctx)
		if err != nil {
			return nil, vmBootConnected, err
		}
		switch current.Status() {
		case msbSdk.SandboxStatusRunning:
			sb, connectErr := current.Connect(ctx)
			if connectErr != nil {
				return nil, vmBootConnected, connectErr
			}
			return sb, vmBootConnected, nil
		case msbSdk.SandboxStatusCreated, msbSdk.SandboxStatusStopped, msbSdk.SandboxStatusCrashed:
			return startExistingVM(ctx, current)
		case msbSdk.SandboxStatusStarting:
			if err := waitForSandboxLifecycle(ctx); err != nil {
				return nil, vmBootConnected, err
			}
			continue
		case msbSdk.SandboxStatusDraining, msbSdk.SandboxStatusPaused:
			return nil, vmBootConnected, fmt.Errorf(
				"sandbox is not available for a new client: status is %q",
				current.Status(),
			)
		default:
			return nil, vmBootConnected, fmt.Errorf("unexpected sandbox status: %q", current.Status())
		}
	}
}

func waitForSandboxLifecycle(ctx context.Context) error {
	timer := time.NewTimer(sandboxLifecyclePollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func createProjectVM(
	ctx context.Context,
	client msb.Client,
	name string,
	key state.Key,
	repoSlug string,
	info image.ImageInfo, homeVol, repoPath string,
	opts options.RunOptions,
	env envsecret.EnvSecrets,
	ui termio.UI,
) (msb.Sandbox, bool, error) {
	info, err := image.EnsureLoaded(ctx, client, info, ui)
	if err != nil {
		return nil, false, fmt.Errorf("load runner image: %w", err)
	}
	cpus := opts.CPUs
	numCPUs := sysinfo.NumCPUs()
	if cpus == 0 {
		cpus = numCPUs
	}
	maxMemoryGiB := sysinfo.TotalMemoryGiB()

	vmEnv := buildProjectVMEnv(env.Vars, info.Env)
	ui.Verbosef("adding docker env definitions to project VM environment: %s", info.Env)

	mountConfigs := buildMounts(
		homeVol,
		repoPath,
		options.ResolveTmpSizeMiB(opts.TmpSize),
		options.ResolveWorkspaceQuotaMiB(opts.WorkspaceQuota),
		opts.Mounts,
	)

	spin := ui.Spinner("Starting project VM")
	idleTimeout := opts.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = options.DefaultVMIdleTimeout
	}
	optsList := []msbSdk.SandboxOption{
		msbSdk.WithImage(info.Ref),
		// The labels carry the artifact's identity and kind authoritatively; the
		// name cannot distinguish a folder that itself begins with a legacy
		// prefix, and it may have been truncated. The grouping label carries the
		// repo slug so list --label project=<repo> groups every clone of one
		// origin.
		msbSdk.WithLabels(naming.ProjectSandboxLabels(key.Slug, key.Agent, repoSlug, info.Ref)),
		msbSdk.WithMounts(mountConfigs),
		msbSdk.WithSecrets(env.Secrets...),
		msbSdk.WithEnv(vmEnv),
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
		hostPort := opts.ServeHostPort
		if hostPort == 0 {
			hostPort = options.FirstFreeHostPort(options.ServeOnlyBasePort)
		}
		optsList = append(optsList, msbSdk.WithPortBindings(projectPortBindings(true, hostPort)...))
	}
	netCfg, err := opts.Network.Config()
	if err != nil {
		spin.StopError(err)
		return nil, false, fmt.Errorf("network config: %w", err)
	}
	optsList = append(optsList, msbSdk.WithNetwork(netCfg))
	sb, err := client.CreateSandbox(ctx, name, optsList...)
	if err != nil {
		spin.StopError(err)
		return nil, false, fmt.Errorf("create sandbox: %w", err)
	}
	spin.Stop()
	ui.Infof("created new project VM: %s", name)
	return sb, true, nil
}

// tmpMountPath is the mount point used for the sandbox tmpfs.
const tmpMountPath = mounts.TmpMountPath

// workspaceHostPermissions propagates guest rwx bits onto the real host
// /workspace inodes. The microsandbox default ("private") keeps guest chmod
// virtualized, so an in-VM git write drops the host file's exec bit while the
// guest still reports the tree clean. Mirror keeps host git working trees in
// sync; setuid/setgid and uid/gid are never propagated.
const workspaceHostPermissions = msbSdk.HostPermissionsMirror

func buildMounts(
	homeVol, repoPath string,
	tmpSizeMiB, workspaceQuotaMiB uint32,
	extra mounts.Mounts,
) map[string]msbSdk.MountConfig {
	built := map[string]msbSdk.MountConfig{
		mounts.VMHomeDir: msbSdk.Mount.Named(homeVol, msbSdk.MountOptions{}),
		defaultTargetDir: msbSdk.Mount.Bind(repoPath, msbSdk.MountOptions{
			QuotaMiB:        workspaceQuotaMiB,
			HostPermissions: workspaceHostPermissions,
		}),
		tmpMountPath: msbSdk.Mount.Tmpfs(msbSdk.TmpfsOptions{
			SizeMiB:  tmpSizeMiB,
			Readonly: false,
			Noexec:   false,
			Nosuid:   false,
			Nodev:    false,
		}),
	}
	for target, mount := range extra {
		built[target] = msbSdk.Mount.Bind(mount.Source, msbSdk.MountOptions{Readonly: mount.Readonly})
	}
	return built
}
