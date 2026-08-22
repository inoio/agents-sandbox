package session

import (
	"context"
	"fmt"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/git"
	"github.com/inoio/opencode-sandbox/internal/homeconfig"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	"github.com/inoio/opencode-sandbox/internal/sandbox/volume"
	"github.com/inoio/opencode-sandbox/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// defaultSandboxUser is the user hooks run as when no user is configured.
const defaultSandboxUser = "dev"

// runStartupHooks runs each configured startup hook inside the VM via an
// interactive TTY, so the script can prompt for credentials. Each script is
// responsible for daemonizing its own long-running process so it outlives the
// attach. Failures are logged, not fatal.
func runStartupHooks(ctx context.Context, sb msb.Sandbox, hooks []homeconfig.HookSpec, ui termio.UI) {
	for _, h := range hooks {
		user := h.User
		if user == "" {
			user = defaultSandboxUser
		}
		ui.Infof("running startup hook %s (as %s)", h.Target, user)
		if _, err := sb.AttachWith(
			ctx,
			"/bin/bash",
			[]string{"-l", "-c", h.Target},
			msbSdk.WithAttachUser(user),
		); err != nil {
			ui.Warnf("startup hook %s failed: %v", h.Target, err)
		}
	}
}

func setUpSandbox(
	ctx context.Context,
	sb msb.Sandbox,
	opts options.RunOptions,
	cfs *reprovision.ConfigFiles,
	ui termio.UI,
	restart bool,
) (string, error) {
	ui.Verbosef("expected config files: %v", cfs.Keys)

	if restart {
		restartDaemons(ctx, sb, cfs, opts.ServeOnly, ui)
		return ResolveTarget(ctx, sb, opts.Worktree, ui)
	}

	// Provisioning (writing files) is idempotent and non-disruptive, so it is
	// always performed when there is config to write. Whether the daemon is
	// restarted to pick the config up is decided separately (the restart flag):
	// on a "keep" decision the files are still updated on disk so the next
	// daemon start sees them, without disturbing the running instance.
	if cfs.HasSnippets || len(cfs.HomeFiles) > 0 {
		if provErr := reprovision.Provision(ctx, sb.FS(), cfs); provErr != nil {
			ui.Warnf("provision failed: %v (continuing)", provErr)
		}
	}

	if dockerErr := startDockerdIfPresent(ctx, sb, ui); dockerErr != nil {
		return "", fmt.Errorf("docker startup: %w", dockerErr)
	}

	if len(cfs.Hooks) > 0 {
		runStartupHooks(ctx, sb, cfs.Hooks, ui)
	}

	if daemonErr := ensureDaemon(ctx, opts.ServeOnly, sb, ui); daemonErr != nil {
		return "", daemonErr
	}

	return ResolveTarget(ctx, sb, opts.Worktree, ui)
}

// decideReconfig centralizes all reconfiguration decisions: the image-change
// home-volume prompt, the VM recreate decision, the daemon-restart decision,
// and cpu/memory staging. It re-fetches the existing sandbox handle to read
// live VM files (no passed-in msb.Sandbox is available at the call site).
//
// Note: the plan's literal signature included an sb msb.Sandbox parameter, but
// this is never available to decideReconfig in prepareSandbox (it runs
// before ensureProjectVM, before any sandbox exists). The function re-fetches
// and Connect()s its own sandbox for file reads.
func decideReconfig(
	ctx context.Context,
	client msb.Client,
	vm *volume.Manager,
	opts options.RunOptions,
	imageRef, imageDigest, homeVol string, hs state.HomeState,
	cfs *reprovision.ConfigFiles,
	ui termio.UI,
) (bool, bool, string, error) {
	slug := git.ProjectSlug(ui)
	handle, _ := client.GetSandbox(ctx, projectVMName(slug))

	var curCfg *msbSdk.SandboxConfig
	var liveSb msb.Sandbox
	if handle != nil {
		var cfgErr error
		curCfg, cfgErr = handle.Config()
		if cfgErr != nil {
			ui.Verbosef("reading existing VM config failed: %v (continuing)", cfgErr)
		}
		var connectErr error
		liveSb, connectErr = handle.Connect(ctx)
		if connectErr != nil {
			ui.Verbosef("connecting to existing VM failed: %v (continuing)", connectErr)
		}
	}

	// Detect whether the project's image digest changed since the last run. The
	// home-volume prompt is deferred until the rebuild decision below confirms
	// we are actually switching to the new image.
	imageChanged := hs.ImageDigest != imageDigest

	var opencfgChanged bool
	if liveSb != nil {
		vmData := reprovision.ReadVMConfig(ctx, liveSb, cfs.Keys, ui)
		opencfgChanged = len(vmData) > 0 && !reprovision.OpenCodeConfigEqual(cfs, vmData)
		if detachErr := liveSb.Detach(context.Background()); detachErr != nil {
			ui.Verbosef("failed to detach live sandbox handle: %v", detachErr)
		}
	}

	desiredEnv := reprovision.MergeEnvMaps(
		reprovision.BuildEnvMap(configpaths.Get().UserEnvFile()),
		reprovision.BuildEnvMap(configpaths.Get().ProjectEnvFile()),
	)
	desiredSecrets := reprovision.BuildSecretsFromSpecs(reprovision.MergeSecretSpecs(
		reprovision.ParseSecretSpecLegacy(configpaths.Get().UserEnvSecretFile(), ui),
		reprovision.ParseSecretSpecLegacy(configpaths.Get().ProjectEnvSecretFile(), ui),
		reprovision.ParseSecretSpecYAML(configpaths.Get().UserEnvSecretYAMLFile(), ui),
		reprovision.ParseSecretSpecYAML(configpaths.Get().ProjectEnvSecretYAMLFile(), ui),
	), ui)
	envHasChanged := reprovision.EnvChanged(hs.EnvState, desiredEnv)
	secretsHasChanged := reprovision.SecretsChanged(hs.SecretState, desiredSecrets)

	plan := reprovision.PlanReconfig(curCfg, imageRef, opts, envHasChanged, secretsHasChanged, opencfgChanged)
	otherClients := state.CountActiveClients(slug)
	applyRecreate, applyRestart, err := reprovision.ResolveReconfig(ctx, ui, plan, otherClients, plan.Changes)
	if err != nil {
		return false, false, homeVol, err
	}
	recreate := applyRecreate
	restart := applyRestart && !recreate && !plan.Recreate

	// The home-volume question only matters when we are actually switching to
	// the new image, so it is asked after the rebuild decision instead of up
	// front. When the rebuild is deferred (keep current VM) or no state exists
	// yet (fresh home already created), the prompt is skipped.
	if imageChanged && recreate {
		action := vm.ResolveHomeAction(ui, hs.ImageDigest, imageDigest)
		if action == volume.ActionQuit {
			ui.Infof("exiting as requested by user")
			return false, false, homeVol, &ExitError{Code: 1}
		}
		newVol, err := vm.ApplyHomeAction(ctx, client, slug, homeVol, imageRef, imageDigest, action, opts, ui)
		if err != nil {
			return false, false, homeVol, fmt.Errorf("apply home action: %w", err)
		}
		homeVol = newVol
	}
	return recreate, restart, homeVol, nil
}

// restartDaemons provisions config files and restarts the opencode daemon so
// an opencode-config change is picked up. Env/secret changes are never routed
// here: they require a VM rebuild and are handled by the recreate path instead.
func restartDaemons(ctx context.Context, sb msb.Sandbox, files *reprovision.ConfigFiles, serveOnly bool, ui termio.UI) {
	if err := reprovision.Provision(ctx, sb.FS(), files); err != nil {
		ui.Warnf("provision failed: %v (keeping existing daemon)", err)
		return
	}
	ui.Infof("opencode serve restarting…")
	if _, _, err := daemonShellFunc(ctx, sb, daemonKillCmd); err != nil {
		ui.Warnf("kill stale daemon failed (continuing): %v", err)
	}
	if err := ensureDaemon(ctx, serveOnly, sb, ui); err != nil {
		ui.Warnf("daemon restart failed: %v (using existing)", err)
	}
}
