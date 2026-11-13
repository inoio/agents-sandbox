package prepare

import (
	"context"
	"fmt"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/ports"
	"github.com/inoio/agents-sandbox/internal/sandbox/reconfig"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/sandbox/resourceproposal"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/sysinfo"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// unreadableVMConfigChangeLabel names the forced recreate when an existing VM's
// config cannot be read: the change detectors have nothing to compare against,
// so the VM must be recreated rather than reused with possibly stale image,
// env, or network settings (specs/vm/reconfig-decision.md).
const unreadableVMConfigChangeLabel = "unreadable VM config"

// reconfigDecision is the resolved reconfiguration plan for a project VM. Named
// fields replace the former positional (recreate, restart, homeVol,
// serveHostPort) return, so a caller reads which decision it is holding.
//
//exhaustruct:ignore
type reconfigDecision struct {
	Recreate bool
	Restart  bool
	// PortsChanged reports whether the requested user ports differ from the
	// bindings the existing VM carries. When the VM is kept despite a ports
	// change, the change is deferred to the next recreate.
	PortsChanged bool
	// ServePortDeferred reports whether a kept VM in serve-only mode has no
	// reachable serve binding — it publishes none, or its config could not be
	// read — so the agent server is unreachable until the VM is recreated.
	ServePortDeferred bool
	// HomeAction is the resolved home-volume disposition when the VM is
	// recreated for a changed image digest; nil when no home action applies.
	// applyReconfigHomeAction applies it.
	HomeAction    *volume.VolumeAction
	ServeHostPort int
}

// existingServeHostPort returns the host port the running VM's config
// publishes for the agent guest port, or fallback when it carries no such
// binding (a freshly created VM, or a read-back that lost the guest port).
func existingServeHostPort(cfg *msb.SandboxConfig, fallback int) int {
	if cfg == nil {
		return fallback
	}
	if port, ok := ports.ServeHostBinding(cfg.PortBindings); ok {
		return port
	}
	return fallback
}

// serveBindingReachable reports whether the kept live VM's config publishes a
// serve-only binding, so existingServeHostPort resolves the port the agent
// server is actually reachable at. An unreadable config leaves the bindings
// unknown, and a VM with no serve binding (e.g. one created by a shell run) has
// none published: either way the agent server is unreachable until the VM is
// recreated. A reachable binding stays reachable even when this run's desired
// host port differs — existingServeHostPort keeps the live port.
func serveBindingReachable(live liveProjectVM) bool {
	if live.configUnreadable || live.config == nil {
		return false
	}
	_, ok := ports.ServeHostBinding(live.config.PortBindings)
	return ok
}

// liveProjectVM is what the launcher reads from the existing project VM before
// planning its reconfiguration. The zero value means no project VM exists.
//
//exhaustruct:ignore
type liveProjectVM struct {
	// handle is the project VM the decision is read from; vm.EnsureProjectVM
	// reuses it rather than looking the VM up again, so a recreate destroys
	// exactly this VM (specs/identity/sandbox-identity.md).
	handle           msb.SandboxHandle
	config           *msb.SandboxConfig
	configUnreadable bool
	// agentConfigFiles holds the agent config files read from the running VM,
	// keyed by guest path; nil when the VM could not be connected.
	agentConfigFiles map[string][]byte
}

// readLiveProjectVM reads the facts the reconfiguration decision compares
// against: the project VM's handle, its persisted config, and the agent config
// files currently in the VM. Reading the files attaches to the VM once and
// detaches before returning. A lookup error that cannot confirm the VM absent
// is returned, so it is never mistaken for "no VM".
func readLiveProjectVM(
	ctx context.Context,
	client msb.Client,
	k state.Key,
	agentConfigPaths []string,
	ui termio.Logger,
) (liveProjectVM, error) {
	handle, found, lookupErr := vm.FindProjectVM(ctx, client, k)
	if lookupErr != nil {
		return liveProjectVM{}, fmt.Errorf("locate project VM: %w", lookupErr)
	}
	if !found {
		return liveProjectVM{}, nil
	}
	config, configUnreadable := readExistingVMConfig(handle, k, ui)
	return liveProjectVM{
		handle:           handle,
		config:           config,
		configUnreadable: configUnreadable,
		agentConfigFiles: readAgentConfigFiles(ctx, handle, agentConfigPaths, ui),
	}, nil
}

// readAgentConfigFiles connects to the VM, reads the agent config files at
// paths, and detaches. A VM that cannot be connected (e.g. a stopped one)
// yields nil. The detach runs under the caller's context without its
// cancellation, so a cancelled run still releases the attach.
func readAgentConfigFiles(
	ctx context.Context,
	handle msb.SandboxHandle,
	paths []string,
	ui termio.Logger,
) map[string][]byte {
	sb, connectErr := handle.Connect(ctx)
	if connectErr != nil {
		ui.Verbosef("connecting to existing VM failed: %v (continuing)", connectErr)
		return nil
	}
	defer func() {
		if detachErr := sb.Detach(context.WithoutCancel(ctx)); detachErr != nil {
			ui.Verbosef("failed to detach live sandbox handle: %v", detachErr)
		}
	}()
	return reprovision.ReadVMConfig(ctx, sb, paths, ui)
}

// resolveReconfigDecision computes the reconfiguration decision from the live
// VM facts readLiveProjectVM read: it compares the persisted fingerprints,
// resolves the recreate/restart choice against any attached clients, and —
// when the VM will be recreated for a changed image — resolves the home-volume
// action. It neither touches the VM nor has home-volume side effects;
// applyReconfigHomeAction applies the resolved action. The desired env and
// secrets are the caller's single load (envsecret.LoadEnvAndSecrets), so the
// change comparison and the VM creation it leads to agree on one reading of
// the host files.
func resolveReconfigDecision(
	ctx context.Context,
	volumes *volume.Manager,
	k state.Key,
	opts options.RunOptions,
	imageInfo image.ImageInfo, homeVol string, hs state.HomeState,
	cfs *reprovision.ConfigFiles,
	env envsecret.EnvSecrets,
	live liveProjectVM,
	ui termio.UI,
) (reconfigDecision, error) {
	// Detect whether the project's image digest changed since the last run. The
	// home-volume prompt is deferred until the rebuild decision below confirms
	// we are actually switching to the new image.
	imageChanged := hs.ImageDigest != imageInfo.Digest
	if imageChanged {
		ui.Verbosef("image digest changed: %s -> %s", hs.ImageDigest, imageInfo.Digest)
	}

	var changedAgentConfig []string
	if len(live.agentConfigFiles) > 0 {
		changedAgentConfig = reprovision.ChangedAgentConfigPaths(cfs, live.agentConfigFiles)
	}

	desired := desiredFingerprints(opts, env)
	plan := reconfig.PlanReconfig(
		live.config,
		imageInfo.Identity,
		opts,
		reconfig.ChangeFlags{
			Image: imageChanged,
			Env:   settingDiff(state.EnvChanged(hs.EnvState, env.Vars), hs.EnvState, desired.Env),
			Secrets: settingDiff(
				state.SecretsChanged(hs.SecretState, env.Secrets),
				hs.SecretState,
				desired.Secret,
			),
			Network: settingDiff(
				state.NetworkChanged(hs.NetworkState, opts.Network),
				hs.NetworkState,
				desired.Network,
			),
			Mounts: settingDiff(
				state.MountsChanged(hs.MountState, opts.Mounts),
				hs.MountState,
				desired.Mount,
			),
			AgentConfig:       changedAgentConfig,
			BootMaxMemoryGrew: bootMaxMemoryGrew(live),
		},
		homeVol,
	)

	// An unreadable config leaves PlanReconfig with nothing to compare, so its
	// change detectors are all skipped. Force a recreate through the normal
	// resolution path (plan.Recreate + a change label), which still prompts when
	// other clients are attached, instead of silently reusing a VM whose image,
	// env, or network may differ.
	if live.configUnreadable {
		plan.Recreate = true
		plan.Changes = append([]reconfig.Change{{Label: unreadableVMConfigChangeLabel}}, plan.Changes...)
	}

	otherClients := state.CountActiveClients(k)
	actions, err := reconfig.ResolveReconfig(ctx, ui, plan, otherClients, plan.Changes)
	if err != nil {
		return reconfigDecision{}, err
	}

	// ResolveReconfig reports a daemon restart only when the plan does not
	// recreate, so actions.Restart already implies !actions.Recreate && !plan.Recreate.
	portsChanged := plan.PortsChanged
	serveHostPort := plan.ServeHostPort
	servePortDeferred := false
	if !actions.Recreate && portsChanged {
		// A kept VM defers the ports change and keeps serving on its existing
		// agent binding; the freshly probed port applies at the next recreate.
		serveHostPort = existingServeHostPort(live.config, serveHostPort)
	}
	// In serve-only mode a kept VM is unreachable whenever it has no live serve
	// binding to keep: the ports change was deferred away from a VM that never
	// published one, or the config could not be read at all. A differing — but
	// published — host port stays reachable through existingServeHostPort.
	if !actions.Recreate && opts.ServeOnly && live.handle != nil && !serveBindingReachable(live) {
		servePortDeferred = true
	}
	decision := reconfigDecision{
		Recreate:          actions.Recreate,
		Restart:           actions.Restart,
		PortsChanged:      portsChanged,
		ServePortDeferred: servePortDeferred,
		ServeHostPort:     serveHostPort,
	}

	// The home-volume question only matters when we are actually switching to
	// the new image, so it is asked after the rebuild decision instead of up
	// front. When the rebuild is deferred (keep current VM) or no state exists
	// yet (fresh home already created), the prompt is skipped.
	if decision.Recreate && imageChanged {
		action := volumes.ResolveHomeAction(ui, hs.ImageDigest, imageInfo.Digest)
		decision.HomeAction = &action
	}

	return decision, nil
}

// settingDiff pairs a fingerprinted setting's change detection with the entries
// recorded for the running VM and those of this run. Absent (nil) entries are a
// state written before entries were recorded.
func settingDiff(changed bool, applied, desired state.FingerprintState) reconfig.SettingDiff {
	return reconfig.SettingDiff{
		Changed:         changed,
		AppliedRecorded: applied.Entries != nil,
		Applied:         applied.Entries,
		Desired:         desired.Entries,
	}
}

// detectAndOfferMemoryRaise detects a memory-limit hit from the live project
// VM's system log and metrics and offers a raise. It reads what only the
// prepare path knows (the VM's configured and boot-maximum memory) and
// delegates the detection, dedup, prompt, and persistence to the shared
// resourceproposal core, so the next-launch and post-attach paths share one
// anti-nag state machine (specs/resources/resource-limit-proposal.md). It
// returns the memory setting the user accepted and persisted, or "".
func detectAndOfferMemoryRaise(
	ctx context.Context,
	live liveProjectVM,
	k state.Key,
	opts options.RunOptions,
	hs state.HomeState,
	paths configpaths.ConfigPaths,
	ui termio.UI,
) string {
	if live.handle == nil {
		return ""
	}
	currentMiB := uint32(0)
	if live.config != nil {
		currentMiB = live.config.MemoryMiB
	}
	hostAvailableMiB, hostAvailableKnown := sysinfo.AvailableMemoryMiB()
	return resourceproposal.DetectAndOfferMemoryRaise(
		ctx, k, live.handle, hs.ResourcePressure,
		resourceproposal.MemoryLimits{
			CurrentMiB:         currentMiB,
			BootMaxMiB:         bootMaxMemoryMiB(live),
			HostAvailableMiB:   hostAvailableMiB,
			HostAvailableKnown: hostAvailableKnown,
		},
		false, opts, paths, ui,
	)
}

// bootMaxMemoryGrew reports whether a freshly created VM would boot with a
// higher memory maximum than the live one.
func bootMaxMemoryGrew(live liveProjectVM) bool {
	return live.config != nil && vm.BootMaxMemoryMiB() > live.config.MaxMemoryMiB
}

// bootMaxMemoryMiB is the live VM's boot maximum (host RAM at creation), the
// ceiling for a suggested raise. It is 0 when unknown, which SuggestRaise reads
// as "no clamp".
func bootMaxMemoryMiB(live liveProjectVM) uint32 {
	if live.config == nil {
		return 0
	}
	return live.config.MaxMemoryMiB
}

// applyReconfigHomeAction applies the home-volume action the decision resolved
// and returns the home volume to mount. A nil HomeAction means no home-volume
// change applies, so the current volume is returned unchanged. A "quit" action
// aborts the run with a vm.ExitError. A skipVM prepare leaves the action
// uncommitted, because applying it would spawn a throwaway VM.
func applyReconfigHomeAction(
	ctx context.Context,
	client msb.Client,
	volumes *volume.Manager,
	k state.Key,
	homeVol string,
	imageInfo image.ImageInfo,
	skipVM bool,
	decision reconfigDecision,
	ui termio.UI,
) (string, error) {
	if decision.HomeAction == nil {
		return homeVol, nil
	}
	if *decision.HomeAction == volume.ActionQuit {
		ui.Infof("exiting as requested by user")
		return "", &vm.ExitError{Code: 1}
	}
	newVol, err := volumes.ApplyHomeAction(ctx, client, k, homeVol, imageInfo, *decision.HomeAction, skipVM, ui)
	if err != nil {
		return "", fmt.Errorf("apply home action: %w", err)
	}
	return newVol, nil
}

// readExistingVMConfig reads the existing project VM's config, including its
// live mounts. A failed Config read is surfaced as a warning and reported as
// unreadable, so the caller can force a recreate instead of silently reusing a
// VM whose image, env, or network may differ (specs/vm/reconfig-decision.md).
func readExistingVMConfig(
	handle msb.SandboxHandle,
	k state.Key,
	ui termio.Logger,
) (*msb.SandboxConfig, bool) {
	cfg, configReadErr := handle.Config()
	configUnreadable := configReadErr != nil
	if configUnreadable {
		ui.Warnf(
			"reading the existing VM config for %s failed: %v; treating the VM as changed (a recreate is required)",
			vm.ProjectVMName(k), configReadErr,
		)
	}
	applyLiveMounts(cfg, handle, ui)
	applyLivePorts(cfg, handle, ui)
	return cfg, configUnreadable
}

// applyLiveMounts fills cfg.Volumes with the mounts the persisted config
// records, which the SDK's Config() drops (specs/vm/live-mount-readback.md). Until
// it runs, cfg.Volumes is empty and the size/quota/home detectors see nothing. A
// read failure is reported at verbose level and leaves cfg.Volumes untouched, so
// the decision proceeds without the mount-based detectors instead of aborting
// the run.
func applyLiveMounts(cfg *msb.SandboxConfig, handle msb.SandboxHandle, ui termio.Logger) {
	if cfg == nil {
		return
	}
	liveMounts, err := handle.ConfigMounts()
	if err != nil {
		ui.Verbosef("reading VM mounts failed: %v (continuing)", err)
		return
	}
	cfg.Volumes = liveMounts
}

// applyLivePorts fills cfg.PortBindings with the published ports the persisted
// config records, which the SDK's Config() drops just like mounts
// (specs/vm/live-mount-readback.md). Until it runs, cfg.PortBindings is empty and
// the ports detectors would see every run as changed. A read failure is
// reported at verbose level and leaves the bindings untouched, so the decision
// degrades instead of aborting the run.
func applyLivePorts(cfg *msb.SandboxConfig, handle msb.SandboxHandle, ui termio.Logger) {
	if cfg == nil {
		return
	}
	livePorts, err := handle.ConfigPorts()
	if err != nil {
		ui.Verbosef("reading VM ports failed: %v (continuing)", err)
		return
	}
	cfg.PortBindings = livePorts
}

// detectAndOfferDiskRaise offers the project's recorded, unanswered disk hits
// (the next launch does not probe; the VM may be stopped) and returns opts with
// any accepted size, so this run applies it (specs/resources/resource-limit-proposal.md
// §1e, §5).
func detectAndOfferDiskRaise(
	k state.Key,
	opts options.RunOptions,
	hs state.HomeState,
	paths configpaths.ConfigPaths,
	ui termio.UI,
) options.RunOptions {
	raised := resourceproposal.DetectAndOfferDiskRaise(
		k,
		nil,
		hs.DiskPressure,
		resourceproposal.WithHostFreeSpace(resourceproposal.RunDiskLimits(opts)),
		opts,
		paths,
		ui,
	)
	if setting, ok := raised["disk-size"]; ok {
		opts.DiskSize = setting
	}
	if setting, ok := raised["workspace-quota"]; ok {
		opts.WorkspaceQuota = setting
	}
	if setting, ok := raised["tmp-size"]; ok {
		opts.TmpSize = setting
	}
	return opts
}
