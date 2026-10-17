package vm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/sandbox/runtimemigration"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// Session is a prepared, ready-to-attach sandbox: the VM, its name, and
// the agent attach target.
type Session struct {
	sb            msb.Sandbox
	name          string
	target        string
	cwd           string
	guestCWD      string
	serveHostPort int
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

// Target returns the agent attach target directory.
func (s *Session) Target() string {
	return s.target
}

// GuestCWD returns the guest directory the agent should start in, derived from
// where the session was launched relative to the workspace bind root.
func (s *Session) GuestCWD() string {
	return s.guestCWD
}

// ServeHostPort returns the published host port the agent server is reachable
// at in serve-only mode (0 when not serving).
func (s *Session) ServeHostPort() int {
	return s.serveHostPort
}

// offerRuntimeHomeMigration is the session-start hook for the opt-in legacy
// home-volume copy. It is a variable so tests can stub the offer without a real
// legacy home; production always uses runtimemigration.Offer.
var offerRuntimeHomeMigration = runtimemigration.Offer

// SetRuntimeHomeMigrationOffer replaces the session-start migration offer used
// by PrepareSandbox and returns the previous value. Tests in any package use it
// to seal PrepareSandbox off from the developer's real legacy home:
//
//	orig := vm.SetRuntimeHomeMigrationOffer(func(context.Context, msb.Client, termio.UI) error {
//		return nil
//	})
//	t.Cleanup(func() { vm.SetRuntimeHomeMigrationOffer(orig) })
func SetRuntimeHomeMigrationOffer(
	f func(ctx context.Context, client msb.Client, ui termio.UI) error,
) func(ctx context.Context, client msb.Client, ui termio.UI) error {
	orig := offerRuntimeHomeMigration
	offerRuntimeHomeMigration = f
	return orig
}

// PrepareSandbox builds (or reuses) the project VM, provisions config, and
// returns a ready-to-attach Session. It is the setup half of a run:
// Run and Shell in the session package call it and then attach to the result.
func PrepareSandbox(
	ctx context.Context,
	opts options.RunOptions,
	ui termio.UI,
) (*Session, error) {
	cwd, err := resolveWorkspaceDir()
	if err != nil {
		return nil, err
	}

	layout, guestCWD := resolveBindLayout(cwd, ui)

	projectSlug := git.ProjectSlug()

	a, ok := agent.Lookup(opts.Agent)
	if !ok {
		return nil, fmt.Errorf("unknown agent %q", opts.Agent)
	}
	k := state.Key{Slug: projectSlug, Agent: a.Name()}
	// Known --dry-run gates: determineAgentVersion (skips the upgrade check and
	// legacy fold) and prepareHomeVolume (skips the migration offer) here, plus
	// volume.ApplyHomeAction (skips the migrate/reset action) and
	// session.Run/Shell (skip the attach). Other steps — notably image build,
	// home-volume creation, and state writes — still run under --dry-run. Making
	// --dry-run fully side-effect-free is tracked in issue #228. See
	// .operator-shared/specs/project-state-transactions.md.
	//
	// Decide the agent version to bake before touching the image: this may offer
	// an upgrade up front, so a normal run never rebuilds the image twice. See
	// resolveBuildVersion for the decision rules.
	agentVersion, shallUpgrade, offeredVersion, err := determineAgentVersion(ctx, k, a, ui, opts)
	if err != nil {
		return nil, err
	}

	imageInfo, err := image.EnsureImage(
		ctx,
		a,
		projectSlug,
		buildOptions(opts, agentVersion, opts.Rebuild, currentAgentSource(k) == agentSourceUser),
		ui,
	)
	if err != nil {
		return nil, fmt.Errorf("ensuring image failed: %w", err)
	}
	if shallUpgrade {
		ui.Verbosef("runner image rebuilt with a newer %s version", a.Name())
	}

	ui.Verbosef("Using image '%s' (digest=%s)", imageInfo.Tag, imageInfo.Digest)

	// Compute the desired config fingerprints once: they seed a fresh home
	// volume's state (issue #183) and are what persistConfigHashes records.
	fingerprints := desiredFingerprints(opts, ui)
	client, vm, homeVol, vs, err := prepareHomeVolume(ctx, k, imageInfo, opts, fingerprints, ui)
	if err != nil {
		return nil, err
	}
	// The home volume is resolved (state exists), so a deferred offered mark can
	// now be recorded.
	persistOfferedVersion(k, offeredVersion, ui)

	// Load the merged agent config and home files exactly once per startup;
	// the result is shared by the reconfig decision and the provisioning step.
	cfs, err := reprovision.LoadConfigFiles(a, ui, provisionHostConfig(opts))
	if err != nil {
		return nil, err
	}

	recreate, restart, homeVol, serveHostPort, err := decideReconfig(
		ctx,
		client,
		vm,
		k,
		opts,
		imageInfo,
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
	opts.ServeHostPort = resolveServeHostPort(opts, serveHostPort)

	sb, boot, err := ensureProjectVM(ctx, opts, imageInfo, homeVol, layout.BindRoot, k, ui)
	if err != nil {
		return nil, err
	}
	if boot == vmBootCreated {
		persistConfigHashes(k, fingerprints, ui)
	}
	name := projectVMName(k)
	var sandboxTarget string
	var sandboxErr error
	if sb == nil {
		ui.Infof("VM lifecycle skipped (--dry-run-vm)")
		sandboxTarget = guestCWD
	} else {
		sandboxTarget, sandboxErr = setUpSandbox(ctx, k, sb, opts, cfs, ui, restart, boot, guestCWD)
		if sandboxErr != nil {
			return nil, sandboxErr
		}
	}

	ui.Verbosef("attach target: %s", sandboxTarget)

	return &Session{
		sb:            sb,
		name:          name,
		target:        sandboxTarget,
		cwd:           cwd,
		guestCWD:      guestCWD,
		serveHostPort: opts.ServeHostPort,
	}, nil
}

// determineAgentVersion decides the agent version for the run. A side-effect-free
// --dry-run never prompts or hits the network: it uses the explicit pin, falling
// back to this project's recorded version. Otherwise the full upgrade decision
// runs.
func determineAgentVersion(
	ctx context.Context,
	k state.Key,
	a agent.Agent,
	ui termio.UI,
	opts options.RunOptions,
) (string, bool, string, error) {
	if opts.DryRun {
		ui.Verbosef("dry-run: would resolve build version")
		version := opts.AgentVersion
		if version == "" {
			version = currentAgentVersion(k)
		}
		return version, false, "", nil
	}
	migrateLegacyUpdaterState(ui)
	return resolveBuildVersion(ctx, k, a, ui, opts)
}

func buildOptions(opts options.RunOptions, agentVersion string, force, userProvided bool) image.BuildOptions {
	return image.BuildOptions{
		Force:        force,
		AgentVersion: agentVersion,
		UserProvided: userProvided,
		Docker:       opts.Docker,
	}
}

// resolveWorkspaceDir returns the current working directory with all symlinks
// resolved to their physical path. It is used as the workspace bind-mount
// source. microsandbox's virtiofs bind mount rejects a host source whose path
// contains a symlink with a cryptic "Not a directory" error (e.g. macOS
// resolves /tmp to /private/tmp), so the physical path must be passed.
func resolveWorkspaceDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get current directory: %w", err)
	}
	return resolveWorkspacePath(cwd)
}

// resolveWorkspacePath resolves all symlinks in dir to their physical path.
// It is split out from resolveWorkspaceDir so the symlink-resolution step and
// its error handling can be unit-tested without touching the process cwd.
func resolveWorkspacePath(dir string) (string, error) {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("resolve symlinks in %q: %w", dir, err)
	}
	return resolved, nil
}

// resolveBindLayout decides which host directory to bind at /workspace and the
// guest directory the agent should start in. A worktree outside its enclosing
// checkout is bound as-is (keeping today's binding) with a warning, since its
// shared git dir would not be reachable in the VM.
func resolveBindLayout(cwd string, ui termio.UI) (git.WorkspaceLayout, string) {
	layout := git.ResolveWorkspaceLayout(cwd)
	if layout.OutsideWorktree {
		ui.Warnf(
			"worktree %q is outside its checkout %q; git may not resolve inside the VM — keep worktrees inside the checkout",
			cwd,
			layout.CheckoutRoot,
		)
	}
	return layout, guestWorkspacePath(layout.BindRoot, cwd)
}

// guestWorkspacePath maps the host cwd to the directory the agent should start
// in inside the VM, given the host directory bound at /workspace. When cwd is
// the bind root (or cannot be related to it) the workspace mount path itself is
// returned; otherwise the relative path is appended using POSIX separators.
func guestWorkspacePath(bindRoot, cwd string) string {
	rel, err := filepath.Rel(bindRoot, cwd)
	if err != nil || rel == "." {
		return mounts.WorkspaceMountPath
	}
	return path.Join(mounts.WorkspaceMountPath, filepath.ToSlash(rel))
}

// resolveServeHostPort returns the host port to publish in serve-only mode. When
// the reconfig plan produced one (an existing VM reuses its published port) it
// is used; otherwise a free host port is probed so the VM-creation port (read
// from opts.ServeHostPort) and the serve-message port (read from the Session)
// agree on the very first run.
func resolveServeHostPort(opts options.RunOptions, planned int) int {
	if opts.ServeOnly && planned == 0 {
		return options.FirstFreeHostPort(options.ServeOnlyBasePort)
	}
	return planned
}

// prepareHomeVolume offers the opt-in legacy home-volume copy (unless this is a
// side-effect-free --dry-run), then resolves the project's home volume and its
// state. It returns the msb client and volume manager for the caller's
// subsequent reconfig/VM steps.
func prepareHomeVolume(
	ctx context.Context,
	k state.Key,
	imageInfo image.ImageInfo,
	opts options.RunOptions,
	fingerprints state.Fingerprints,
	ui termio.UI,
) (msb.Client, *volume.Manager, string, state.HomeState, error) {
	client := msb.Get()
	// --dry-run is documented as side-effect-free, so it must not prompt or
	// copy volumes. DryRunVM is unaffected: it still runs the offer.
	if !opts.DryRun {
		if err := offerRuntimeHomeMigration(ctx, client, ui); err != nil {
			// A pending migration is a deliberate block, not an offer failure:
			// return it as-is so the user sees the guidance, not a wrapper.
			if errors.Is(err, runtimemigration.ErrMigrationPending) {
				return nil, nil, "", state.HomeState{}, err
			}
			return nil, nil, "", state.HomeState{}, fmt.Errorf("runtime home migration offer failed: %w", err)
		}
	}

	vm := volume.NewManager(ui)
	recovered := liveHomeVolume(ctx, client, k, ui)
	homeVol, vs, err := resolveHomeVolume(
		ctx, vm, client, k, imageInfo, opts.DryRunVM, fingerprints, recovered, ui,
	)
	if err != nil {
		return nil, nil, "", state.HomeState{}, fmt.Errorf("volume setup failed: %w", err)
	}
	ui.Verbosef("home volume: %s", homeVol)
	return client, vm, homeVol, vs, nil
}

// persistOfferedVersion records that the given release's upgrade prompt has been
// shown, once the home volume is resolved. pendingUpgrade records it directly
// when a state file already exists; this deferred record covers the case where
// the state was recreated by home resolution in this same run, so a "don't
// remind again" choice still sticks.
func persistOfferedVersion(k state.Key, version string, ui termio.UI) {
	if version == "" {
		return
	}
	if err := state.WithHomeState(k, func(hs *state.HomeState, exists bool) (bool, error) {
		if !exists {
			return false, nil
		}
		hs.MarkOffered(version)
		return true, nil
	}); err != nil {
		ui.Warnf("could not persist project state: %v (continuing)", err)
	}
}

// liveHomeVolume returns the home volume currently mounted by the project VM,
// or "" when the project has no VM or its config cannot be read. It lets home
// resolution recover the volume when the state file was lost (issue #219). A
// not-found VM is expected (first run); any other lookup error is logged so a
// transient failure is visible rather than silently orphaning the volume.
func liveHomeVolume(ctx context.Context, client msb.Client, k state.Key, ui termio.UI) string {
	handle, err := client.GetSandbox(ctx, projectVMName(k))
	if err != nil {
		if !msb.IsNotFound(err) {
			ui.Verbosef("project VM lookup for home-volume recovery failed: %v (continuing)", err)
		}
		return ""
	}
	if handle == nil {
		return ""
	}
	cfg, err := handle.Config()
	if err != nil || cfg == nil {
		ui.Verbosef("reading existing VM config for home-volume recovery failed: %v (continuing)", err)
		return ""
	}
	return cfg.Volumes[mounts.VMHomeDir].Named
}

// resolveHomeVolume resolves the project home volume, reusing the existing one
// from state, recovering the live VM's volume when state was lost, or creating a
// fresh prefill when none exists.
func resolveHomeVolume(
	ctx context.Context,
	vm *volume.Manager,
	client msb.Client,
	k state.Key,
	info image.ImageInfo,
	dryRunVM bool,
	fingerprints state.Fingerprints,
	recoveredVolume string,
	ui termio.UI,
) (string, state.HomeState, error) {
	return vm.ResolveHomeVolume(ctx, client, k, info, dryRunVM, fingerprints, recoveredVolume, ui)
}

// desiredFingerprints builds the VM-config fingerprints for the current run:
// the one computation that seeds a fresh home volume's state (avoiding a
// fingerprint-less state beside a VM) and that persistConfigHashes records
// after a VM is created. Building it once keeps the two in lock-step and avoids
// re-reading (and re-warning on) the env/secret files.
func desiredFingerprints(opts options.RunOptions, ui termio.UI) state.Fingerprints {
	env, secrets := envsecret.LoadEnvAndSecrets(ui)
	return state.Fingerprints{
		Env:     state.BuildEnvState(env),
		Secret:  state.BuildSecretState(secrets),
		Network: state.BuildNetworkState(opts.Network),
		Mount:   state.BuildMountState(opts.Mounts),
	}
}

// persistConfigHashes records the desired env/secret/network/mount
// fingerprints when a project VM is freshly created (or recreated), so
// subsequent runs can detect changes. It persists the fingerprints computed by
// desiredFingerprints, so the seeded and persisted sets cannot diverge.
func persistConfigHashes(k state.Key, fingerprints state.Fingerprints, ui termio.UI) {
	if err := persistEnvSecrets(k, fingerprints.Env, fingerprints.Secret); err != nil {
		ui.Warnf("persisting env/secret fingerprints on VM creation: %v (continuing)", err)
	}
	if err := persistNetworkState(k, fingerprints.Network); err != nil {
		ui.Warnf("persisting network fingerprint on VM creation: %v (continuing)", err)
	}
	if err := persistMountState(k, fingerprints.Mount); err != nil {
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
