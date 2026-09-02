package vm

import (
	"context"
	"fmt"
	"os"

	"github.com/inoio/opencode-sandbox/internal/agent"
	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/git"
	"github.com/inoio/opencode-sandbox/internal/sandbox/image"
	"github.com/inoio/opencode-sandbox/internal/sandbox/mounts"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/network"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	"github.com/inoio/opencode-sandbox/internal/sandbox/volume"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// Session is a prepared, ready-to-attach sandbox: the VM, its name, and
// the opencode attach target.
type Session struct {
	sb     msb.Sandbox
	name   string
	target string
	cwd    string
}

// Cleanup releases the sandbox handle. It is safe to call on a nil session.
func (s *Session) Cleanup() {
	if s.sb != nil {
		_ = s.sb.Detach(context.Background())
	}
}

// Sandbox returns the underlying sandbox VM handle. It is nil when the VM was
// not created (e.g. --dry-run-vm).
func (s *Session) Sandbox() msb.Sandbox {
	return s.sb
}

// Target returns the opencode attach target directory.
func (s *Session) Target() string {
	return s.target
}

// PrepareSandbox builds (or reuses) the project VM, provisions config, and
// returns a ready-to-attach Session. It is the setup half of a run:
// Run and Shell in the session package call it and then attach to the result.
func PrepareSandbox(
	ctx context.Context,
	opts options.RunOptions,
	ui termio.UI,
) (*Session, error) {
	projectSlug := git.ProjectSlug()

	a, ok := agent.Lookup(opts.Agent)
	if !ok {
		return nil, fmt.Errorf("unknown agent %q", opts.Agent)
	}

	// Decide the opencode version to bake before touching the image. Without an
	// explicit pin, this checks for (and may offer) an upgrade up front, so a
	// normal run never rebuilds the image twice (once for the current version,
	// once for an upgrade). When unpinned and nothing newer is chosen, the
	// version already baked into the runner image is reused instead of
	// re-resolving "latest" from the network on every run; re-resolving would
	// change the version build arg (and thus the image identity), causing
	// sporadic rebuilds and fresh loads into microsandbox.
	agentVersion, shallUpgrade, err := resolveBuildVersion(ctx, a, ui, opts)
	if err != nil {
		return nil, err
	}

	imageInfo, err := image.EnsureImage(
		ctx,
		a,
		projectSlug,
		image.BuildOptions{Force: opts.Rebuild || shallUpgrade, AgentVersion: agentVersion, Dind: opts.Dind},
		ui,
	)
	if err != nil {
		return nil, fmt.Errorf("ensuring image failed: %w", err)
	}
	if shallUpgrade {
		ui.Verbosef("runner image rebuilt with a newer opencode version")
	}

	ui.Verbosef("Using image '%s' (digest=%s)", imageInfo.Tag, imageInfo.Digest)

	client := msb.Get()
	vm := volume.NewManager(ui)
	homeVol, vs, err := resolveHomeVolume(ctx, vm, client, projectSlug, imageInfo, opts.DryRunVM, ui)
	if err != nil {
		return nil, fmt.Errorf("volume setup failed: %w", err)
	}
	ui.Verbosef("home volume: %s", homeVol)

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get current directory: %w", err)
	}

	// Load the merged opencode config and home files exactly once per startup;
	// the result is shared by the reconfig decision and the provisioning step.
	cfs, err := reprovision.LoadConfigFiles(a, configpaths.Get().UserOpencodeConfigDir(), ui, provisionHostConfig(opts))
	if err != nil {
		return nil, err
	}

	recreate, restart, homeVol, err := decideReconfig(
		ctx,
		client,
		vm,
		opts,

		imageInfo.Tag,
		imageInfo.Digest,
		homeVol,
		vs,
		cfs,
		ui,
	)
	if err != nil {
		return nil, err
	}
	ui.Verbosef("recreate: %v, restart: %v", recreate, restart)
	opts.Recreate = recreate
	sb, boot, err := ensureProjectVM(ctx, opts, imageInfo.Tag, homeVol, cwd, imageInfo.Env, ui)
	if err != nil {
		return nil, err
	}
	if boot == vmBootCreated {
		persistConfigHashes(projectSlug, opts.Network, opts.Mounts, ui)
	}
	name := projectVMName(projectSlug)

	var sandboxTarget string
	var sandboxErr error
	if sb == nil {
		ui.Infof("VM lifecycle skipped (--dry-run-vm)")
		sandboxTarget = resolveTargetNoBranch()
	} else {
		sandboxTarget, sandboxErr = setUpSandbox(ctx, sb, opts, cfs, ui, restart, boot)
		if sandboxErr != nil {
			return nil, sandboxErr
		}
	}

	ui.Verbosef("attach target: %s", sandboxTarget)

	return &Session{
		sb:     sb,
		name:   name,
		target: sandboxTarget,
		cwd:    cwd,
	}, nil
}

// resolveHomeVolume resolves the project home volume, reusing the existing one
// from state or creating a fresh prefill when none exists.
func resolveHomeVolume(
	ctx context.Context,
	vm *volume.Manager,
	client msb.Client,
	projectSlug string,
	info image.ImageInfo,
	dryRunVM bool,
	ui termio.UI,
) (string, state.HomeState, error) {
	return vm.ResolveHomeVolume(ctx, client, projectSlug, info.Digest, info.Tag, dryRunVM, ui)
}

// persistConfigHashes records the desired env/secret/network/mount
// fingerprints when a project VM is freshly created (or recreated), so
// subsequent runs can detect changes.
func persistConfigHashes(
	projectSlug string,
	networkPolicy network.Policy,
	mounts mounts.Mounts,
	ui termio.UI,
) {
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
	if err := persistEnvSecrets(
		projectSlug,
		reprovision.BuildEnvState(desiredEnv),
		reprovision.BuildSecretState(desiredSecrets),
	); err != nil {
		ui.Warnf("persisting env/secret fingerprints on VM creation: %v (continuing)", err)
	}
	if err := persistNetworkState(projectSlug, networkPolicy); err != nil {
		ui.Warnf("persisting network fingerprint on VM creation: %v (continuing)", err)
	}
	if err := persistMountState(projectSlug, mounts); err != nil {
		ui.Warnf("persisting mount fingerprint on VM creation: %v (continuing)", err)
	}
}

// provisionHostConfig reports whether the agent's host config files should be
// copied into the VM. A nil option enables it (the launcher default).
func provisionHostConfig(opts options.RunOptions) bool {
	if opts.ProvisionHostConfig == nil {
		return true
	}
	return *opts.ProvisionHostConfig
}
