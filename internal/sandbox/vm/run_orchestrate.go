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
	key           state.Key
	homeVolume    string
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

// HomeVolume returns the name of the home volume mounted at /home/dev.
func (s *Session) HomeVolume() string {
	return s.homeVolume
}

// Key returns the state key the session was prepared with: the checkout-scoped
// project slug and the agent. Callers key the notify dedup, the client lease and
// the attach reap off it instead of re-resolving identity from the cwd.
func (s *Session) Key() state.Key {
	return s.key
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
//
//deadcode:ignore reason="test seam replacing the runtime migration offer"
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
//
// skipVM prepares everything except the VM lifecycle (the --dry-run-vm
// contract), leaving Session.Sandbox nil. A --dry-run preview never reaches
// here: session.Run and session.Shell render the preview and return before this
// call, so the hub has no preview mode of its own.
func PrepareSandbox(
	ctx context.Context,
	opts options.RunOptions,
	skipVM bool,
	ui termio.UI,
) (*Session, error) {
	cwd, err := resolveWorkspaceDir()
	if err != nil {
		return nil, err
	}
	a, identity, adoption, err := resolveSessionIdentity(ctx, cwd, opts.Agent, skipVM, ui)
	if err != nil {
		return nil, err
	}
	k := state.Key{Slug: adoption.Slug, Agent: a.Name()}
	opts.Mounts = withSharedGitDirMount(opts.Mounts, identity, ui)
	guestCWD := guestWorkspacePath(identity.BindRoot, cwd)
	// Decide the agent version to bake before touching the image: this may offer
	// an upgrade up front, so a normal run never rebuilds the image twice. See
	// resolveBuildVersion for the decision rules.
	agentVersion, shallUpgrade, offeredVersion, err := determineAgentVersion(ctx, k, a, ui, opts)
	if err != nil {
		return nil, err
	}

	buildVersion, userProvided := ResolveAgentVersionForBuild(k, agentVersion)
	imageInfo, err := image.EnsureImage(
		ctx,
		a,
		k.Slug,
		buildOptions(opts, buildVersion, opts.Rebuild, userProvided),
		ui,
	)
	if err != nil {
		return nil, fmt.Errorf("ensuring image failed: %w", err)
	}
	if shallUpgrade {
		ui.Verbosef("runner image rebuilt with a newer %s version", a.Name())
	}

	ui.Verbosef("Using image '%s' (digest=%s)", imageInfo.Ref, imageInfo.Digest)

	env, fingerprints := loadEnvFingerprints(opts, ui)
	client, vm, homeVol, vs, err := prepareHomeVolume(
		ctx, k, imageInfo, skipVM, fingerprints, ui,
	)
	if err != nil {
		return nil, err
	}
	// The home volume is resolved (state exists), so a deferred offered mark can
	// now be recorded.
	persistOfferedVersion(k, offeredVersion, ui)
	persistAgentVersion(k, imageInfo.AgentVersion, ui)

	// Load the merged agent config and home files exactly once per startup;
	// the result is shared by the reconfig decision and the provisioning step.
	cfs, err := reprovision.LoadConfigFiles(a, ui, provisionHostConfig(opts))
	if err != nil {
		return nil, err
	}

	decision, err := resolveReconfigDecision(ctx, client, vm, k, opts, imageInfo, homeVol, vs, cfs, env, ui)
	if err != nil {
		return nil, err
	}
	homeVol, err = applyReconfigHomeAction(ctx, client, vm, k, homeVol, imageInfo, skipVM, decision, ui)
	if err != nil {
		return nil, err
	}
	ui.Verbosef("recreate: %v, restart: %v", decision.Recreate, decision.Restart)
	opts.Recreate = decision.Recreate
	opts.ServeHostPort = resolveServeHostPort(opts, decision.ServeHostPort)

	sb, boot, err := ensureProjectVM(
		ctx, opts, imageInfo, homeVol, identity.BindRoot, k, identity.RepoSlug, skipVM, env, ui,
	)
	if err != nil {
		return nil, err
	}
	if boot == vmBootCreated {
		persistConfigHashes(k, fingerprints, ui)
	}
	var sandboxTarget string
	var sandboxErr error
	if sb == nil {
		ui.Infof("VM lifecycle skipped (--dry-run-vm)")
		sandboxTarget = guestCWD
	} else {
		sandboxTarget, sandboxErr = setUpSandbox(ctx, k, sb, opts, cfs, ui, decision.Restart, boot, guestCWD)
		if sandboxErr != nil {
			return nil, sandboxErr
		}
	}

	ui.Verbosef("attach target: %s", sandboxTarget)

	return &Session{
		sb:            sb,
		name:          projectVMName(k),
		target:        sandboxTarget,
		cwd:           cwd,
		guestCWD:      guestCWD,
		serveHostPort: opts.ServeHostPort,
		key:           k,
		homeVolume:    homeVol,
	}, nil
}

// resolveSessionIdentity looks up the agent and derives the workspace identity
// for cwd, then adopts a legacy origin-keyed identity (#144). Adoption runs
// before any other work so its notice explains the pause and the runner image
// builds under the effective (checkout-scoped) tag; a skipVM prepare makes it
// read-only, so the rest of the run keeps resolving against the legacy location.
// It returns the adoption so the caller can use the effective slug; a home
// volume a VM-only adoption preserved is recorded in the checkout state, not in
// the adoption value.
func resolveSessionIdentity(
	ctx context.Context,
	cwd, agentName string,
	skipVM bool,
	ui termio.UI,
) (agent.Agent, git.Identity, IdentityAdoption, error) {
	a, ok := agent.Lookup(agentName)
	if !ok {
		return nil, git.Identity{}, IdentityAdoption{}, fmt.Errorf("unknown agent %q", agentName)
	}
	identity, err := git.ResolveIdentity(cwd)
	if err != nil {
		return nil, git.Identity{}, IdentityAdoption{}, err
	}
	adoption, err := AdoptLegacyIdentity(ctx, msb.Get(), identity, a.Name(), !skipVM, ui)
	if err != nil {
		return nil, git.Identity{}, IdentityAdoption{}, err
	}
	return a, identity, adoption, nil
}

// determineAgentVersion decides the agent version for the run: the upgrade
// decision, which may offer a rebuild up front and fold the legacy updater
// state.
func determineAgentVersion(
	ctx context.Context,
	k state.Key,
	a agent.Agent,
	ui termio.UI,
	opts options.RunOptions,
) (string, bool, string, error) {
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

// prepareHomeVolume offers the opt-in legacy home-volume copy, then resolves the
// project's home volume and its state. It returns the msb client and volume
// manager for the caller's subsequent reconfig/VM steps.
//
// A home volume preserved by a VM-only identity adoption (#144) is not passed
// here: adoption records it in the checkout state's RecoveredHomeVolume, which
// ResolveHomeVolume recovers, so the reference survives any caller that discards
// the adoption result or any step that fails before home resolution.
func prepareHomeVolume(
	ctx context.Context,
	k state.Key,
	imageInfo image.ImageInfo,
	skipVM bool,
	fingerprints state.Fingerprints,
	ui termio.UI,
) (msb.Client, *volume.Manager, string, state.HomeState, error) {
	client := msb.Get()
	if err := offerRuntimeHomeMigration(ctx, client, ui); err != nil {
		// A pending migration is a deliberate block, not an offer failure:
		// return it as-is so the user sees the guidance, not a wrapper.
		if errors.Is(err, runtimemigration.ErrMigrationPending) {
			return nil, nil, "", state.HomeState{}, err
		}
		return nil, nil, "", state.HomeState{}, fmt.Errorf("runtime home migration offer failed: %w", err)
	}

	vm := volume.NewManager(ui)
	recovered := liveHomeVolume(ctx, client, k, ui)
	homeVol, vs, err := vm.ResolveHomeVolume(
		ctx, client, k, imageInfo, skipVM, fingerprints, recovered, ui,
	)
	if err != nil {
		return nil, nil, "", state.HomeState{}, fmt.Errorf("volume setup failed: %w", err)
	}
	ui.Verbosef("home volume: %s", homeVol)
	return client, vm, homeVol, vs, nil
}

// persistAgentVersion records the release baked into the runner image as this
// project's upgrade baseline, once the home volume is resolved so a fresh state
// exists. It is the writer of record: unlike boot-time provenance it does not
// depend on the profile implementing a VersionProvider. A failure only warns;
// the next run reconciles from the image label.
func persistAgentVersion(k state.Key, version string, ui termio.UI) {
	if err := state.RecordAgentVersion(k, version); err != nil {
		ui.Warnf("could not persist the agent version: %v (continuing)", err)
	}
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

// projectVMHomeVolume reports whether the project VM for k exists and the home
// volume it has mounted. A nil handle counts as "no VM". A mounts read failure
// returns exists=true together with the error, so a caller that would destroy the
// VM on the strength of this result can fail closed instead of dropping its
// volume; the best-effort home-recovery caller swallows it.
func projectVMHomeVolume(ctx context.Context, client msb.Client, k state.Key) (string, bool, error) {
	handle, found, err := findProjectVM(ctx, client, k)
	if err != nil {
		return "", false, err
	}
	if !found {
		return "", false, nil
	}
	if handle == nil {
		return "", false, nil
	}
	liveMounts, err := handle.ConfigMounts()
	if err != nil {
		return "", true, err
	}
	return liveMounts[mounts.VMHomeDir].Named, true, nil
}

// liveHomeVolume returns the home volume currently mounted by the project VM,
// or "" when the project has no VM or its mounts cannot be read. It lets home
// resolution recover the volume when the state file was lost (issue #219). The
// mount is read from the VM's persisted config, because the SDK's Config()
// leaves cfg.Volumes empty on a read-back (specs/live-mount-readback.md). A
// not-found VM is expected (first run); any other lookup error is logged so a
// transient failure is visible rather than silently orphaning the volume.
func liveHomeVolume(ctx context.Context, client msb.Client, k state.Key, ui termio.UI) string {
	volume, _, err := projectVMHomeVolume(ctx, client, k)
	if err != nil {
		ui.Verbosef("project VM lookup for home-volume recovery failed: %v (continuing)", err)
		return ""
	}
	return volume
}

// loadEnvFingerprints reads the host env and secret files once per run and
// derives the VM-config fingerprints from that single read, so the persisted
// fingerprints and the env/secrets baked into a newly created VM cannot
// disagree (#342).
func loadEnvFingerprints(opts options.RunOptions, ui termio.UI) (envsecret.EnvSecrets, state.Fingerprints) {
	env := envsecret.LoadEnvAndSecrets(ui)
	return env, desiredFingerprints(opts, env)
}

// desiredFingerprints builds the VM-config fingerprints for the current run:
// the one computation that seeds a fresh home volume's state (avoiding a
// fingerprint-less state beside a VM) and that persistConfigHashes records
// after a VM is created. Building it once keeps the two in lock-step; the env
// and secrets are the caller's single load, so the host files are not re-read
// (nor re-warned about) per consumer.
func desiredFingerprints(opts options.RunOptions, env envsecret.EnvSecrets) state.Fingerprints {
	return state.Fingerprints{
		Env:     state.BuildEnvState(env.Vars),
		Secret:  state.BuildSecretState(env.Secrets),
		Network: state.BuildNetworkState(opts.Network),
		Mount:   state.BuildMountState(opts.Mounts),
	}
}

// persistConfigHashes records the desired env/secret/network/mount
// fingerprints when a project VM is freshly created (or recreated), so
// subsequent runs can detect changes. It persists the fingerprints computed by
// desiredFingerprints, so the seeded and persisted sets cannot diverge.
func persistConfigHashes(k state.Key, fingerprints state.Fingerprints, ui termio.UI) {
	if err := persistFingerprints(k, fingerprints); err != nil {
		ui.Warnf("persisting config fingerprints on VM creation: %v (continuing)", err)
	}
}

// provisionHostConfig reports whether the agent's host config files should be
// copied into the VM. A nil option disables it (the launcher default).
func provisionHostConfig(opts options.RunOptions) bool {
	if opts.ProvisionHostConfig == nil {
		return false
	}
	return *opts.ProvisionHostConfig
}
