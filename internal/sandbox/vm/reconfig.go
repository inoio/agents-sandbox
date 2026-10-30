package vm

import (
	"context"
	"fmt"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/reconfig"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// DefaultSandboxUser is the user hooks run as unless the hook opts into root.
const DefaultSandboxUser = "dev"

// RootUser is the VM root user, used to run privileged startup hooks.
const RootUser = "root"

// defaultHookInterpreter is the interpreter used for a startup hook that does
// not declare a shebang.
const defaultHookInterpreter = "/bin/sh"

// unreadableVMConfigChangeLabel names the forced recreate when an existing VM's
// config cannot be read: the change detectors have nothing to compare against,
// so the VM must be recreated rather than reused with possibly stale image,
// env, or network settings (specs/reconfig-decision.md).
const unreadableVMConfigChangeLabel = "unreadable VM config"

// runStartupHooks runs each configured startup hook inside the VM via an
// interactive attach. A hook with a shebang is executed directly so the VM
// kernel resolves the interpreter; one without falls back to /bin/sh. Hook
// scripts are marked executable during provisioning, so no +x bit is required
// on the host source. The hook's HOME is set so scripts can rely on it. The
// agent waits for the attach to finish; a hook that must outlive the attach is
// responsible for daemonizing itself. Failures are logged, not fatal.
func runStartupHooks(ctx context.Context, sb msb.Sandbox, hooks []homeconfig.HookSpec, ui termio.UI) {
	for _, h := range hooks {
		user := DefaultSandboxUser
		home := "/home/dev"
		if h.Root {
			user = RootUser
			home = "/root"
		}
		command := h.Target
		var args []string
		if !h.HasShebang {
			command = defaultHookInterpreter
			args = []string{h.Target}
		}
		ui.Infof("running startup hook %s (as %s, via %s)", h.Target, user, command)
		if _, err := sb.AttachWith(
			ctx,
			command,
			args,
			msbSdk.WithAttachUser(user),
			msbSdk.WithAttachEnv(map[string]string{"HOME": home}),
		); err != nil {
			ui.Warnf("startup hook %s failed: %v", h.Target, err)
		}
	}
}

func setUpSandbox(
	ctx context.Context,
	k state.Key,
	sb msb.Sandbox,
	opts options.RunOptions,
	cfs *reprovision.ConfigFiles,
	ui termio.UI,
	restart bool,
	boot vmBoot,
	guestCWD string,
) (string, error) {
	a, ok := agent.Lookup(opts.Agent)
	if !ok {
		return "", fmt.Errorf("unknown agent %q", opts.Agent)
	}

	ui.Verbosef("expected config files: %v", cfs.Keys)

	// Provisioning (writing files) is idempotent and non-disruptive, so it is
	// always performed when there is config to write. Whether the daemon is
	// restarted to pick the config up is decided separately (the restart flag):
	// on a "keep" decision the files are still updated on disk so the next
	// daemon start sees them, without disturbing the running instance.
	provisioned := true
	if cfs.HasSnippets || len(cfs.HomeFiles) > 0 || len(cfs.Provisioned) > 0 || len(cfs.Mirror) > 0 ||
		len(cfs.Remove) > 0 || len(cfs.RemoveHostCopies) > 0 {
		if provErr := reprovision.Provision(ctx, sb, cfs); provErr != nil {
			ui.Warnf("provision failed: %v (continuing)", provErr)
			provisioned = false
		}
	}

	// Record image provenance and the agent version baseline on a fresh boot.
	if boot.booted() {
		recordImageProvenance(ctx, k, a, sb, ui)
	}

	if restart {
		if provisioned {
			restartDaemons(ctx, a, sb, opts.ServeOnly, ui)
		}
		return ResolveTarget(ctx, a, sb, opts.Worktree, guestCWD, ui)
	}

	if len(cfs.Hooks) > 0 && boot.booted() {
		runStartupHooks(ctx, sb, cfs.Hooks, ui)
	}

	if dockerErr := startDockerdIfPresent(ctx, sb, ui); dockerErr != nil {
		return "", fmt.Errorf("docker startup: %w", dockerErr)
	}

	if daemonErr := ensureDaemon(ctx, a, opts.ServeOnly, false, sb, ui); daemonErr != nil {
		return "", daemonErr
	}

	return ResolveTarget(ctx, a, sb, opts.Worktree, guestCWD, ui)
}

// reconfigDecision is the resolved reconfiguration plan for a project VM. Named
// fields replace the former positional (recreate, restart, homeVol,
// serveHostPort) return, so a caller reads which decision it is holding.
//
//exhaustruct:ignore
type reconfigDecision struct {
	Recreate bool
	Restart  bool
	// HomeAction is the resolved home-volume disposition when the VM is
	// recreated for a changed image digest; nil when no home action applies.
	// applyReconfigHomeAction applies it.
	HomeAction    *volume.VolumeAction
	ServeHostPort int
}

// resolveReconfigDecision computes the reconfiguration decision: it reads the
// live VM, compares the persisted fingerprints, resolves the recreate/restart
// choice against any attached clients, and — when the VM will be recreated for
// a changed image — resolves the home-volume action. It performs no
// home-volume side effects; applyReconfigHomeAction applies the resolved
// action. The desired env and secrets are the caller's single load
// (envsecret.LoadEnvAndSecrets), so the change comparison and the VM creation
// it leads to agree on one reading of the host files.
//
// It re-fetches the existing sandbox handle to read live VM files: no
// passed-in msb.Sandbox is available at the call site, which runs before
// ensureProjectVM creates one.
func resolveReconfigDecision(
	ctx context.Context,
	client msb.Client,
	vm *volume.Manager,
	k state.Key,
	opts options.RunOptions,
	imageInfo image.ImageInfo, homeVol string, hs state.HomeState,
	cfs *reprovision.ConfigFiles,
	env envsecret.EnvSecrets,
	ui termio.UI,
) (reconfigDecision, error) {
	handle, _, lookupErr := findProjectVM(ctx, client, k)
	if lookupErr != nil {
		return reconfigDecision{}, fmt.Errorf("locate project VM: %w", lookupErr)
	}
	curCfg, liveSb, configUnreadable := readExistingVM(ctx, handle, k, ui)

	// Detect whether the project's image digest changed since the last run. The
	// home-volume prompt is deferred until the rebuild decision below confirms
	// we are actually switching to the new image.
	imageChanged := hs.ImageDigest != imageInfo.Digest
	if imageChanged {
		ui.Verbosef("image digest changed: %s -> %s", hs.ImageDigest, imageInfo.Digest)
	}

	var agentCfgChanged bool
	if liveSb != nil {
		vmData := reprovision.ReadVMConfig(ctx, liveSb, cfs.Keys, ui)
		agentCfgChanged = !reprovision.AgentConfigEqual(cfs, vmData)
		if detachErr := liveSb.Detach(context.Background()); detachErr != nil {
			ui.Verbosef("failed to detach live sandbox handle: %v", detachErr)
		}
	}

	envHasChanged := state.EnvChanged(hs.EnvState, env.Vars)
	secretsHasChanged := state.SecretsChanged(hs.SecretState, env.Secrets)
	networkHasChanged := state.NetworkChanged(hs.NetworkState, opts.Network)
	mountsHaveChanged := state.MountsChanged(hs.MountState, opts.Mounts)

	plan := reconfig.PlanReconfig(
		curCfg,
		imageInfo.Identity,
		opts,
		reconfig.ChangeFlags{
			Image:       imageChanged,
			Env:         envHasChanged,
			Secrets:     secretsHasChanged,
			Network:     networkHasChanged,
			Mounts:      mountsHaveChanged,
			AgentConfig: agentCfgChanged,
		},
		homeVol,
	)

	// An unreadable config leaves PlanReconfig with nothing to compare, so its
	// change detectors are all skipped. Force a recreate through the normal
	// resolution path (plan.Recreate + a change label), which still prompts when
	// other clients are attached, instead of silently reusing a VM whose image,
	// env, or network may differ.
	if configUnreadable {
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
	decision := reconfigDecision{
		Recreate:      actions.Recreate,
		Restart:       actions.Restart,
		ServeHostPort: plan.ServeHostPort,
	}

	// The home-volume question only matters when we are actually switching to
	// the new image, so it is asked after the rebuild decision instead of up
	// front. When the rebuild is deferred (keep current VM) or no state exists
	// yet (fresh home already created), the prompt is skipped.
	if decision.Recreate && imageChanged {
		action := vm.ResolveHomeAction(ui, hs.ImageDigest, imageInfo.Digest)
		decision.HomeAction = &action
	}
	return decision, nil
}

// applyReconfigHomeAction applies the home-volume action the decision resolved
// and returns the home volume to mount. A nil HomeAction means no home-volume
// change applies, so the current volume is returned unchanged. A "quit" action
// aborts the run with an ExitError. A skipVM prepare leaves the action
// uncommitted, because applying it would spawn a throwaway VM.
func applyReconfigHomeAction(
	ctx context.Context,
	client msb.Client,
	vm *volume.Manager,
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
		return "", &ExitError{Code: 1}
	}
	newVol, err := vm.ApplyHomeAction(ctx, client, k, homeVol, imageInfo, *decision.HomeAction, skipVM, ui)
	if err != nil {
		return "", fmt.Errorf("apply home action: %w", err)
	}
	return newVol, nil
}

// readExistingVM reads the existing project VM's config and live handle. A
// failed Config read is surfaced as a warning and reported as unreadable, so
// the caller can force a recreate instead of silently reusing a VM whose image,
// env, or network may differ (specs/reconfig-decision.md). handle may be nil
// when no VM exists, in which case all three results are zero.
func readExistingVM(
	ctx context.Context,
	handle msb.SandboxHandle,
	k state.Key,
	ui termio.UI,
) (*msbSdk.SandboxConfig, msb.Sandbox, bool) {
	if handle == nil {
		return nil, nil, false
	}
	cfg, configReadErr := handle.Config()
	configUnreadable := configReadErr != nil
	if configUnreadable {
		ui.Warnf(
			"reading the existing VM config for %s failed: %v; treating the VM as changed (a recreate is required)",
			projectVMName(k), configReadErr,
		)
	}
	applyLiveMounts(cfg, handle, ui)
	liveSb, connectErr := handle.Connect(ctx)
	if connectErr != nil {
		ui.Verbosef("connecting to existing VM failed: %v (continuing)", connectErr)
	}
	return cfg, liveSb, configUnreadable
}

// applyLiveMounts fills cfg.Volumes with the mounts the persisted config
// records, which the SDK's Config() drops (specs/live-mount-readback.md). Until
// it runs, cfg.Volumes is empty and the size/quota/home detectors see nothing. A
// read failure is reported at verbose level and leaves cfg.Volumes untouched, so
// the decision degrades to the pre-#223 behaviour instead of aborting the run.
func applyLiveMounts(cfg *msbSdk.SandboxConfig, handle msb.SandboxHandle, ui termio.UI) {
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

// restartDaemons restarts the agent's daemon so an already-provisioned
// agent-config change is picked up. Env/secret changes are never routed
// here: they require a VM rebuild and are handled by the recreate path instead.
func restartDaemons(ctx context.Context, a agent.Agent, sb msb.Sandbox, serveOnly bool, ui termio.UI) {
	ui.Infof("%s serve restarting…", a.Name())
	// ensureDaemon no-ops for an agent without a DaemonProvider.
	if err := ensureDaemon(ctx, a, serveOnly, true, sb, ui); err != nil {
		ui.Warnf("daemon restart failed: %v (using existing)", err)
	}
}
