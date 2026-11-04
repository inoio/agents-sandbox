// Package prepare runs the stages of a run/shell that ready the project VM,
// resolve-identity through setup-sandbox (specs/lifecycle.md): it decides the
// agent version, the home volume and the VM reconfiguration, and calls the
// capabilities in package vm to act on those decisions. PreviewSandbox is the
// read-only --dry-run variant of the same stages.
package prepare

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/lifecycle"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/ports"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/sandbox/runtimemigration"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/vm"
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
// home-volume copy: the production default a test overrides through
// options.Seams.RuntimeHomeMigrationOffer. Production always uses
// runtimemigration.Offer.
var offerRuntimeHomeMigration = runtimemigration.Offer

// runtimeHomeMigrationOffer returns the offer to use for this run: the injected
// seam when a test set one, else runtimemigration.Offer.
func runtimeHomeMigrationOffer(seams options.Seams) func(context.Context, msb.Client, termio.UI) error {
	if seams.RuntimeHomeMigrationOffer != nil {
		return seams.RuntimeHomeMigrationOffer
	}
	return offerRuntimeHomeMigration
}

// PrepareSandbox builds (or reuses) the project VM, provisions config, and
// returns a ready-to-attach Session. It is the setup half of a run:
// Run and Shell in the session package call it and then attach to the result.
//
// Its body is the ordered list of the vm-owned lifecycle stages
// (specs/lifecycle.md, resolve-identity through setup-sandbox), run by one loop
// that records each stage into opts.Trace. Each stage calls one step with the
// values it reads and keeps the values it produces for the stages after it; the
// locals below are that hand-off, not a state the steps reach into.
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
	client := msb.Get()
	dclient := docker.Get()
	paths := configpaths.Get()
	var (
		session       sessionIdentity
		version       versionDecision
		imageInfo     image.ImageInfo
		home          preparedHome
		plan          reconfigPlan
		sb            msb.Sandbox
		boot          vm.VMBoot
		sandboxTarget string
	)
	stages := []lifecycle.Stage{
		{ID: lifecycle.ResolveIdentity, Run: func() error {
			var err error
			session, err = resolveIdentityStage(ctx, client, paths, opts, skipVM, ui)
			if err != nil {
				return err
			}
			opts.Mounts = withSharedGitDirMount(opts.Mounts, session.identity, ui)
			return nil
		}},
		{ID: lifecycle.ResolveVersion, Run: func() error {
			var err error
			version, err = resolveVersionStage(ctx, paths, dclient, session, opts, ui)
			return err
		}},
		{ID: lifecycle.EnsureImage, Run: func() error {
			var err error
			imageInfo, err = ensureImageStage(ctx, dclient, paths, session, version, opts, ui)
			return err
		}},
		{ID: lifecycle.EnsureHome, Run: func() error {
			var err error
			home, err = ensureHomeStage(
				ctx,
				client,
				dclient,
				session.key,
				imageInfo,
				version.offeredVersion,
				skipVM,
				opts,
				ui,
			)
			return err
		}},
		{ID: lifecycle.PlanReconfig, Run: func() error {
			var err error
			plan, err = planReconfigStage(ctx, session, imageInfo, home, skipVM, opts, ui)
			if err != nil {
				return err
			}
			opts.Recreate = plan.decision.Recreate
			serveHostPort, err := resolveServeHostPort(opts, plan.decision.ServeHostPort)
			if err != nil {
				return err
			}
			opts.ServeHostPort = serveHostPort
			return nil
		}},
		{ID: lifecycle.EnsureVM, Run: func() error {
			var err error
			sb, boot, err = ensureVMStage(ctx, session, imageInfo, home, plan, skipVM, opts, ui)
			return err
		}},
		{ID: lifecycle.SetupSandbox, Run: func() error {
			var err error
			sandboxTarget, err = setupSandboxStage(ctx, session, sb, boot, plan, opts, ui)
			return err
		}},
	}
	if err := lifecycle.Run(opts.Trace, stages); err != nil {
		return nil, err
	}

	return &Session{
		sb:            sb,
		name:          vm.ProjectVMName(session.key),
		target:        sandboxTarget,
		cwd:           session.cwd,
		guestCWD:      session.guestCWD,
		serveHostPort: opts.ServeHostPort,
		key:           session.key,
		homeVolume:    plan.homeVolume,
	}, nil
}

// sessionIdentity is what resolve-identity hands to the later stages: the
// workspace directory, the resolved agent capabilities, the resolved identity
// and the effective key.
type sessionIdentity struct {
	cwd      string
	guestCWD string
	agent    agent.Resolved
	identity git.Identity
	key      state.Key
}

// resolveIdentityStage is the resolve-identity stage: it resolves the physical
// workspace directory, the agent and the workspace identity, adopting a legacy
// identity first, and derives the effective key and the guest start directory.
func resolveIdentityStage(
	ctx context.Context,
	client msb.Client,
	paths configpaths.ConfigPaths,
	opts options.RunOptions,
	skipVM bool,
	ui termio.Logger,
) (sessionIdentity, error) {
	if opts.ResolvedAgent.Profile == nil {
		return sessionIdentity{}, fmt.Errorf("unknown agent %q", opts.Agent)
	}
	cwd, err := resolveWorkspaceDir()
	if err != nil {
		return sessionIdentity{}, err
	}
	a, identity, adoption, err := resolveSessionIdentity(ctx, client, paths, cwd, opts.ResolvedAgent, skipVM, ui)
	if err != nil {
		return sessionIdentity{}, err
	}
	return sessionIdentity{
		cwd:      cwd,
		guestCWD: guestWorkspacePath(identity.BindRoot, cwd),
		agent:    a,
		identity: identity,
		key:      state.Key{Slug: adoption.Slug, Agent: a.Profile.Name()},
	}, nil
}

// versionDecision is what resolve-version hands to the later stages: the
// release to build, whether it is user-provided, whether an upgrade was
// accepted, and the release whose upgrade prompt was shown.
type versionDecision struct {
	buildVersion   string
	userProvided   bool
	shallUpgrade   bool
	offeredVersion string
}

// resolveVersionStage is the resolve-version stage. It decides the agent
// version to bake before the image is touched: this may offer an upgrade up
// front, so a normal run never rebuilds the image twice. See
// resolveBuildVersion for the decision rules.
func resolveVersionStage(
	ctx context.Context,
	paths configpaths.ConfigPaths,
	dclient docker.Client,
	session sessionIdentity,
	opts options.RunOptions,
	ui termio.UI,
) (versionDecision, error) {
	agentVersion, shallUpgrade, offeredVersion, err := determineAgentVersion(
		ctx,
		paths,
		dclient,
		session.key,
		session.agent,
		ui,
		opts,
	)
	if err != nil {
		return versionDecision{}, err
	}
	buildVersion, userProvided := ResolveAgentVersionForBuild(session.key, agentVersion)
	return versionDecision{
		buildVersion:   buildVersion,
		userProvided:   userProvided,
		shallUpgrade:   shallUpgrade,
		offeredVersion: offeredVersion,
	}, nil
}

// ensureImageStage is the ensure-image stage: it builds or reuses the runner
// image at the decided version and loads it into microsandbox.
func ensureImageStage(
	ctx context.Context,
	dclient docker.Client,
	paths configpaths.ConfigPaths,
	session sessionIdentity,
	version versionDecision,
	opts options.RunOptions,
	ui termio.UI,
) (image.ImageInfo, error) {
	imageInfo, err := image.EnsureImage(
		ctx,
		dclient,
		paths,
		session.agent.Profile,
		session.key.Slug,
		image.ReadProjectDockerfile(paths),
		buildOptions(opts, version.buildVersion, opts.Rebuild, version.userProvided),
		ui,
	)
	if err != nil {
		return image.ImageInfo{}, fmt.Errorf("ensuring image failed: %w", err)
	}
	if version.shallUpgrade {
		ui.Verbosef("runner image rebuilt with a newer %s version", session.agent.Profile.Name())
	}
	ui.Verbosef("Using image '%s' (digest=%s)", imageInfo.Ref, imageInfo.Digest)
	return imageInfo, nil
}

// preparedHome is what ensure-home hands to the later stages: the host
// env/secret files and their fingerprints (loaded once per startup), the msb
// client and volume manager, and the resolved home volume and its state.
type preparedHome struct {
	env          envsecret.EnvSecrets
	fingerprints state.Fingerprints
	client       msb.Client
	dclient      docker.Client
	volumes      *volume.Manager
	homeVolume   string
	homeState    state.HomeState
}

// ensureHomeStage is the ensure-home stage: it loads the host env/secret files,
// resolves the home volume and, once state exists, records the deferred
// offered-upgrade mark and the baked agent version.
func ensureHomeStage(
	ctx context.Context,
	client msb.Client,
	dclient docker.Client,
	k state.Key,
	imageInfo image.ImageInfo,
	offeredVersion string,
	skipVM bool,
	opts options.RunOptions,
	ui termio.UI,
) (preparedHome, error) {
	env, fingerprints := loadEnvFingerprints(opts, ui)
	volumes, homeVolume, homeState, err := prepareHomeVolume(
		ctx,
		client,
		dclient,
		k,
		imageInfo,
		skipVM,
		fingerprints,
		ui,
		runtimeHomeMigrationOffer(opts.Seams),
	)
	if err != nil {
		return preparedHome{}, err
	}
	persistOfferedVersion(k, offeredVersion, ui)
	persistAgentVersion(k, imageInfo.AgentVersion, ui)
	return preparedHome{
		env:          env,
		fingerprints: fingerprints,
		client:       client,
		dclient:      dclient,
		volumes:      volumes,
		homeVolume:   homeVolume,
		homeState:    homeState,
	}, nil
}

// reconfigPlan is what plan-reconfig hands to the later stages: the merged
// agent config files (loaded once per startup, shared with provisioning), the
// project VM the decision was read from, the reconfig decision, and the home
// volume after its home action.
type reconfigPlan struct {
	configFiles *reprovision.ConfigFiles
	existingVM  msb.SandboxHandle
	decision    reconfigDecision
	homeVolume  string
}

// planReconfigStage is the plan-reconfig stage: it loads the merged agent
// config, reads the live project VM, decides recreate vs restart vs nothing,
// and applies the resulting home-volume action.
func planReconfigStage(
	ctx context.Context,
	session sessionIdentity,
	imageInfo image.ImageInfo,
	home preparedHome,
	skipVM bool,
	opts options.RunOptions,
	ui termio.UI,
) (reconfigPlan, error) {
	configFiles, err := reprovision.LoadConfigFiles(session.agent, ui, provisionHostConfig(opts))
	if err != nil {
		return reconfigPlan{}, err
	}
	reprovision.WarnUnprovisionedDependencies(
		session.agent, configFiles, mounts.MountTargets(opts.Mounts), provisionHostConfig(opts), ui,
	)
	live, err := readLiveProjectVM(ctx, home.client, session.key, configFiles.Keys, ui)
	if err != nil {
		return reconfigPlan{}, err
	}
	decision, err := resolveReconfigDecision(
		ctx, home.volumes, session.key, opts, imageInfo, home.homeVolume, home.homeState, configFiles,
		home.env, live, ui,
	)
	if err != nil {
		return reconfigPlan{}, err
	}
	homeVolume, err := applyReconfigHomeAction(
		ctx, home.client, home.volumes, session.key, home.homeVolume, imageInfo, skipVM, decision, ui,
	)
	if err != nil {
		return reconfigPlan{}, err
	}
	ui.Verbosef("recreate: %v, restart: %v", decision.Recreate, decision.Restart)
	return reconfigPlan{
		configFiles: configFiles,
		existingVM:  live.handle,
		decision:    decision,
		homeVolume:  homeVolume,
	}, nil
}

// ensureVMStage is the ensure-vm stage: it creates, starts, recreates or
// reconnects the project VM and, on creation, persists the fingerprints.
func ensureVMStage(
	ctx context.Context,
	session sessionIdentity,
	imageInfo image.ImageInfo,
	home preparedHome,
	plan reconfigPlan,
	skipVM bool,
	opts options.RunOptions,
	ui termio.UI,
) (msb.Sandbox, vm.VMBoot, error) {
	sb, boot, err := vm.EnsureProjectVM(ctx, vm.ProjectVMCreation{
		Client:       home.client,
		DockerClient: home.dclient,
		Options:      opts,
		Image:        imageInfo,
		HomeVolume:   plan.homeVolume,
		RepoPath:     session.identity.BindRoot,
		Key:          session.key,
		Existing:     plan.existingVM,
		RepoSlug:     session.identity.RepoSlug,
		SkipVM:       skipVM,
		Env:          home.env,
		UI:           ui,
		Seams:        opts.Seams,
	})
	if err != nil {
		// gomutants:disable-next-line RETURN_ZERO reason="EnsureProjectVM returns vm.VMBootConnected (the vm.VMBoot zero value, the first iota constant) on every error path, so a RETURN_ZERO replacement of boot is the identical constant"
		return nil, boot, err
	}
	if boot == vm.VMBootCreated {
		persistConfigHashes(session.key, home.fingerprints, ui)
	}
	return sb, boot, nil
}

// setupSandboxStage is the setup-sandbox stage: it provisions the VM and
// resolves the attach target. Without a VM (--dry-run-vm) the target is the
// guest start directory.
func setupSandboxStage(
	ctx context.Context,
	session sessionIdentity,
	sb msb.Sandbox,
	boot vm.VMBoot,
	plan reconfigPlan,
	opts options.RunOptions,
	ui termio.Logger,
) (string, error) {
	sandboxTarget := session.guestCWD
	if sb == nil {
		ui.Infof("VM lifecycle skipped (--dry-run-vm)")
	} else {
		var err error
		sandboxTarget, err = vm.SetUpSandbox(
			ctx,
			session.key,
			sb,
			session.agent,
			opts,
			opts.Seams,
			plan.configFiles,
			ui,
			plan.decision.Restart,
			boot,
			session.guestCWD,
		)
		if err != nil {
			return "", err
		}
		reportActivePorts(opts, plan, ui)
	}
	ui.Verbosef("attach target: %s", sandboxTarget)
	return sandboxTarget, nil
}

// resolveSessionIdentity derives the workspace identity for cwd from the
// run's already-resolved agent, then adopts a legacy origin-keyed identity
// (#144). Adoption runs before any other work so its notice explains the pause
// and the runner image builds under the effective (checkout-scoped) tag; a
// skipVM prepare makes it read-only, so the rest of the run keeps resolving
// against the legacy location. It returns the adoption so the caller can use the
// effective slug; a home volume a VM-only adoption preserved is recorded in the
// checkout state, not in the adoption value.
func resolveSessionIdentity(
	ctx context.Context,
	client msb.Client,
	paths configpaths.ConfigPaths,
	cwd string,
	a agent.Resolved,
	skipVM bool,
	ui termio.Logger,
) (agent.Resolved, git.Identity, vm.IdentityAdoption, error) {
	identity, err := git.ResolveIdentity(cwd)
	if err != nil {
		return agent.Resolved{}, git.Identity{}, vm.IdentityAdoption{}, err
	}
	adoption, err := vm.AdoptLegacyIdentity(ctx, paths, client, identity, a.Profile.Name(), !skipVM, ui)
	if err != nil {
		return agent.Resolved{}, git.Identity{}, vm.IdentityAdoption{}, err
	}
	return a, identity, adoption, nil
}

// determineAgentVersion decides the agent version for the run: the upgrade
// decision, which may offer a rebuild up front and fold the legacy updater
// state.
func determineAgentVersion(
	ctx context.Context,
	paths configpaths.ConfigPaths,
	dclient docker.Client,
	k state.Key,
	a agent.Resolved,
	ui termio.UI,
	opts options.RunOptions,
) (string, bool, string, error) {
	migrateLegacyUpdaterState(paths, ui)
	return resolveBuildVersion(ctx, dclient, k, a, ui, opts)
}

func buildOptions(opts options.RunOptions, agentVersion string, force, userProvided bool) image.BuildOptions {
	return image.BuildOptions{
		Force:        force,
		NoRebuild:    opts.NoRebuild,
		AgentVersion: agentVersion,
		UserProvided: userProvided,
		Docker:       opts.Docker,
		// Route the image build's release resolution through the prepare seam
		// (buildVersionResolver by default, or the injected options.Seams
		// AgentLatestVersion), which defaults to the same agentLatestVersion
		// lookup the upgrade check uses.
		ResolveVersion: agentVersionResolverFor(opts.Seams),
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

// reportPublishedPorts prints the requested user mappings. Whether the running
// VM actually carries them — the gating — is decided by reportActivePorts.
func reportPublishedPorts(opts options.RunOptions, ui termio.Logger) {
	for _, mapping := range opts.Ports {
		ui.Infof("published port: 127.0.0.1:%d → guest port %d", mapping.HostPort, mapping.GuestPort)
	}
}

// reportActivePorts reports the requested user ports only when the running VM
// actually carries them: on a fresh create, a recreate, or a kept VM whose
// ports did not change. A kept VM defers a ports change to the next recreate
// and still runs the old bindings, so the deferral is warned about instead.
func reportActivePorts(opts options.RunOptions, plan reconfigPlan, ui termio.Logger) {
	if len(opts.Ports) == 0 {
		return
	}
	if plan.existingVM != nil && !plan.decision.Recreate && plan.decision.PortsChanged {
		ui.Warnf("ports change deferred: the running VM keeps its published ports until the next recreate")
		return
	}
	reportPublishedPorts(opts, ui)
}

// resolveServeHostPort returns the host port to publish in serve-only mode: the
// reconfig plan's port when it produced one (an existing VM reuses its
// published port), else a free probe, so the VM-creation binding (read from
// opts.ServeHostPort) and the serve message (read from the Session) agree on
// the very first run. It errors when no free host port remains in the probe
// range: publishing the agent server is then impossible, and a silent fallback
// would hand out a port that is already taken.
func resolveServeHostPort(opts options.RunOptions, planned int) (int, error) {
	if !opts.ServeOnly || planned != 0 {
		return planned, nil
	}
	probed := ports.ResolveServeHostPort(nil, true, opts.Ports)
	if probed == 0 {
		return 0, fmt.Errorf(
			"no free host port for the agent server in %d-%d; free a loopback port or publish fewer user ports",
			ports.ServeOnlyBasePort, ports.ServeOnlyBasePort+ports.ProbeRange-1,
		)
	}
	return probed, nil
}

// prepareHomeVolume offers the opt-in legacy home-volume copy, then resolves the
// project's home volume and its state. It returns the volume manager for the
// caller's subsequent reconfig/VM steps.
//
// A home volume preserved by a VM-only identity adoption (#144) is not passed
// here: adoption records it in the checkout state's RecoveredHomeVolume, which
// ResolveHomeVolume recovers, so the reference survives any caller that discards
// the adoption result or any step that fails before home resolution.
func prepareHomeVolume(
	ctx context.Context,
	client msb.Client,
	dclient docker.Client,
	k state.Key,
	imageInfo image.ImageInfo,
	skipVM bool,
	fingerprints state.Fingerprints,
	ui termio.UI,
	offerMigration func(context.Context, msb.Client, termio.UI) error,
) (*volume.Manager, string, state.HomeState, error) {
	if err := offerMigration(ctx, client, ui); err != nil {
		// A pending migration is a deliberate block, not an offer failure:
		// return it as-is so the user sees the guidance, not a wrapper.
		if errors.Is(err, runtimemigration.ErrMigrationPending) {
			return nil, "", state.HomeState{}, err
		}
		return nil, "", state.HomeState{}, fmt.Errorf("runtime home migration offer failed: %w", err)
	}

	volumes := volume.NewManager(dclient)
	recovered := liveHomeVolume(ctx, client, k, ui)
	homeVol, vs, err := volumes.ResolveHomeVolume(
		ctx, client, k, imageInfo, skipVM, fingerprints, recovered, ui,
	)
	if err != nil {
		return nil, "", state.HomeState{}, fmt.Errorf("volume setup failed: %w", err)
	}
	ui.Verbosef("home volume: %s", homeVol)
	return volumes, homeVol, vs, nil
}

// persistAgentVersion records the release baked into the runner image as this
// project's upgrade baseline, once the home volume is resolved so a fresh state
// exists. It is the writer of record: unlike boot-time provenance it does not
// depend on the profile implementing a VersionProvider. A failure only warns;
// the next run reconciles from the image label.
func persistAgentVersion(k state.Key, version string, ui termio.Logger) {
	if err := state.RecordAgentVersion(k, version); err != nil {
		ui.Warnf("could not persist the agent version: %v (continuing)", err)
	}
}

// persistOfferedVersion records that the given release's upgrade prompt has been
// shown, once the home volume is resolved. pendingUpgrade records it directly
// when a state file already exists; this deferred record covers the case where
// the state was recreated by home resolution in this same run, so a "don't
// remind again" choice still sticks.
func persistOfferedVersion(k state.Key, version string, ui termio.Logger) {
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
// or "" when the project has no VM or its mounts cannot be read. It lets home
// resolution recover the volume when the state file was lost (issue #219). The
// mount is read from the VM's persisted config, because the SDK's Config()
// leaves cfg.Volumes empty on a read-back (specs/live-mount-readback.md). A
// not-found VM is expected (first run); any other lookup error is logged so a
// transient failure is visible rather than silently orphaning the volume.
func liveHomeVolume(ctx context.Context, client msb.Client, k state.Key, ui termio.Logger) string {
	volume, _, err := vm.ProjectVMHomeVolume(ctx, client, k)
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
func loadEnvFingerprints(opts options.RunOptions, ui termio.Logger) (envsecret.EnvSecrets, state.Fingerprints) {
	env := envsecret.LoadEnvAndSecrets(ui, opts.AllowPlaceholderFor)
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
func persistConfigHashes(k state.Key, fingerprints state.Fingerprints, ui termio.Logger) {
	if err := persistFingerprints(k, fingerprints); err != nil {
		ui.Warnf("persisting config fingerprints on VM creation: %v (continuing)", err)
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
