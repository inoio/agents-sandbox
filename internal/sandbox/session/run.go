package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"gitlab.inoio.de/inoio/opencode-msb/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-msb/internal/git"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/doctor"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/image"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/options"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/reprovision"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/state"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/volume"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// Constants moved to internal/sandbox/options for sharing across modules.

// parseMemory and resolveTmpSizeMiB moved to internal/sandbox/options.

func buildAttachCommand(target string, _ bool, args []string) string {
	parts := []string{"opencode", "attach", "http://127.0.0.1:4096", "--dir", target}
	parts = append(parts, args...)

	return strings.Join(parts, " ")
}

func buildOpencodeArgs(args []string, auto bool) []string {
	if !auto {
		return args
	}
	return append([]string{options.AutoFlag}, args...)
}

// serveOnlyMessage builds the message printed when serving opencode for
// external clients such as Opencode Desktop.
func serveOnlyMessage(host, port string) string {
	return fmt.Sprintf("Connect Opencode Desktop to: http://%s:%s\n\n"+
		"Optional: set OPENCODE_SERVER_PASSWORD (and OPENCODE_SERVER_USERNAME) to protect the server with basic auth.\n"+
		"Press Ctrl-D to stop serving.", host, port)
}

// runServeOnly keeps the VM alive and blocks until ctx is done (CTRL-D or
// SIGINT), without attaching an in-VM TUI. It holds the VM via a keeper exec so
// the msb idle timeout does not stop it while serving.
func runServeOnly(ctx context.Context, sb msb.Sandbox, ui termio.UI) error {
	host := options.ServeOnlyBindAddr
	port := options.ServeOnlyPort
	ui.Infof("%s", serveOnlyMessage(host, port))
	keeperCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	keeperDone := keepVMAlive(keeperCtx, sb)
	defer func() { _ = keeperDone() }()
	<-ctx.Done()
	return ctx.Err()
}

type sandboxSession struct {
	sb     msb.Sandbox
	name   string
	target string
	cwd    string
}

func (s *sandboxSession) cleanup() {
	if s.sb != nil {
		_ = s.sb.Detach(context.Background())
	}
	// Run git worktree prune on the host repo to clean up stale entries.
	/*if s.cwd != "" {
		_ = git.PruneWorktrees(context.Background(), s.cwd)
	}*/
}

func prepareSandbox(
	ctx context.Context,
	opts options.RunOptions,
	ui termio.UI,
) (*sandboxSession, error) {
	if !doctor.CheckAll(ctx, ui) {
		return nil, errors.New("preflight failed")
	}

	projectSlug := git.ProjectSlug(ui)

	imageRef, imageDigest, imageEnvs, err := image.EnsureImage(ctx, projectSlug, opts.Rebuild, ui)
	if err != nil {
		return nil, fmt.Errorf("image setup failed: %w", err)
	}
	ui.Verbosef("Using image '%s' (digest=%s)", imageRef, imageDigest)

	vm := volume.NewManager(ui)
	client := msb.Get()
	homeVol, vs, err := vm.ResolveHomeVolume(ctx, client, projectSlug, imageDigest, imageRef, opts, ui)
	if err != nil {
		return nil, fmt.Errorf("volume setup failed: %w", err)
	}
	ui.Verbosef("home volume: %s", homeVol)

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get current directory: %w", err)
	}
	recreate, restart, homeVol, err := decideReconfig(
		ctx,
		client,
		vm,
		opts,

		imageRef,
		imageDigest,
		homeVol,
		vs,
		ui,
	)
	if err != nil {
		return nil, err
	}
	ui.Verbosef("recreate: %v, restart: %v", recreate, restart)
	opts.Recreate = recreate
	sb, created, err := ensureProjectVM(ctx, opts, imageRef, homeVol, cwd, imageEnvs, ui)
	if err != nil {
		return nil, err
	}
	if created {
		desiredEnv := reprovision.MergeEnvMaps(
			reprovision.BuildEnvMap(configpaths.GetConfigPaths().UserEnvFile()),
			reprovision.BuildEnvMap(configpaths.GetConfigPaths().ProjectEnvFile()),
		)
		desiredSecrets := reprovision.BuildSecretsFromSpecs(reprovision.MergeSecretSpecs(
			reprovision.ParseSecretSpecLegacy(configpaths.GetConfigPaths().UserEnvSecretFile(), ui),
			reprovision.ParseSecretSpecLegacy(configpaths.GetConfigPaths().ProjectEnvSecretFile(), ui),
			reprovision.ParseSecretSpecYAML(configpaths.GetConfigPaths().UserEnvSecretYAMLFile(), ui),
			reprovision.ParseSecretSpecYAML(configpaths.GetConfigPaths().ProjectEnvSecretYAMLFile(), ui),
		), ui)
		if err := persistEnvSecrets(
			projectSlug,
			reprovision.BuildEnvState(desiredEnv),
			reprovision.BuildSecretState(desiredSecrets),
		); err != nil {
			ui.Warnf("persisting env/secret fingerprints on VM creation: %v (continuing)", err)
		}
	}
	name := projectVMName(projectSlug)

	var sandboxTarget string
	var sandboxErr error
	if sb == nil {
		ui.Infof("VM lifecycle skipped (--dry-run-vm)")
		sandboxTarget = resolveTargetNoBranch()
	} else {
		sandboxTarget, sandboxErr = setUpSandbox(ctx, sb, opts, created, ui, restart)
		if sandboxErr != nil {
			return nil, sandboxErr
		}
	}

	ui.Verbosef("attach target: %s", sandboxTarget)

	return &sandboxSession{
		sb:     sb,
		name:   name,
		target: sandboxTarget,
		cwd:    cwd,
	}, nil
}

// Run creates (or reuses) the project VM, provisions config, starts opencode
// serve, and attaches a TUI client.
//
// Note: Run is called from cli.go after all flags are resolved.
func Run(ctx context.Context, opts options.RunOptions, ui termio.UI) error {
	session, err := prepareSandbox(ctx, opts, ui)
	if err != nil {
		return err
	}
	defer session.cleanup()

	if opts.DryRun {
		ui.Infof("dry-run: Would run opencode")
		return nil
	}
	if opts.DryRunVM && session.sb == nil {
		ui.Infof("dry-run: Would start opencode in VM")
		return nil
	}

	projectSlug := git.ProjectSlug(ui)
	release, acquireErr := state.AcquireClientLease(projectSlug)
	if acquireErr != nil {
		ui.Warnf("client lease failed: %v", acquireErr)
	}
	defer func() {
		if acquireErr == nil && release != nil {
			release()
		}
	}()

	if opts.ServeOnly {
		if err := runServeOnly(ctx, session.sb, ui); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		if acquireErr == nil {
			release()
			release = nil
		}
		if err := reapOnLastClient(ctx, projectSlug, session.sb, opts.ReapPolicy, ui); err != nil {
			ui.Warnf("reap failed: %v", err)
		}
		return &options.ExitError{Code: 0}
	}

	var exitCode int
	var attachErr error
	setup := buildAttachCommand(session.target, opts.Auto, opts.Args)
	ui.Verbosef("%s", setup)
	// Run as a login shell so /etc/profile and ~/.profile are sourced,
	// putting tools installed under /usr/local/go/bin, ~/go/bin and
	// ~/.microsandbox/bin on PATH for opencode and its child shells.
	exitCode, attachErr = session.sb.Attach(ctx, "/bin/bash", "-l", "-c", setup)

	// Explicitly release the lease after attach returns, before reaping.
	// This ensures state.CountActiveClients reflects only OTHER live clients.
	// The deferred release above is a safety net.
	if acquireErr == nil {
		release()
		release = nil
	}

	if err := reapOnLastClient(ctx, projectSlug, session.sb, opts.ReapPolicy, ui); err != nil {
		ui.Warnf("reap failed: %v", err)
	}

	return finalizeRun(attachErr, exitCode)
}

// Shell creates (or reuses) the project VM and drops the user into an
// interactive shell session, without starting opencode serve.
func Shell(ctx context.Context, opts options.RunOptions, ui termio.UI) error {
	session, err := prepareSandbox(ctx, opts, ui)
	if err != nil {
		return err
	}
	defer session.cleanup()

	if opts.DryRun {
		ui.Infof("dry-run: Would start interactive shell session")
		return nil
	}
	if opts.DryRunVM && session.sb == nil {
		ui.Infof("dry-run: Would start interactive shell session")
		return nil
	}

	projectSlug := git.ProjectSlug(ui)
	release, acquireErr := state.AcquireClientLease(projectSlug)
	if acquireErr != nil {
		ui.Warnf("client lease failed: %v", acquireErr)
	}
	defer func() {
		if acquireErr == nil && release != nil {
			release()
		}
	}()

	// Login shell so the interactive shell inherits PATH from /etc/profile and ~/.profile.
	exitCode, attachErr := session.sb.Attach(ctx, "/bin/bash", "-l")

	// Explicitly release the lease after attach returns, before reaping.
	// This ensures state.CountActiveClients reflects only OTHER live clients.
	// The deferred release above is a safety net.
	if acquireErr == nil {
		release()
		release = nil
	}

	if err := reapOnLastClient(ctx, projectSlug, session.sb, opts.ReapPolicy, ui); err != nil {
		ui.Warnf("reap failed: %v", err)
	}

	return finalizeRun(attachErr, exitCode)
}

// BuildImage builds (or updates) the runner image for Docker-in-Docker support.
func BuildImage(ctx context.Context, force, dryRun bool, ui termio.UI) error {
	if dryRun {
		ui.Infof("dry-run: Would build runner image")
		return nil
	}

	if !doctor.CheckDocker(ui) {
		return errors.New("docker not available")
	}
	projectSlug := git.ProjectSlug(ui)

	_, _, _, err := image.EnsureImageWithClient(ctx, msb.Get(), image.ResolveDockerfile(), projectSlug, force, ui)
	return err
}

func finalizeRun(attachErr error, exitCode int) error {
	if attachErr != nil {
		return fmt.Errorf("opencode session failed: %w", attachErr)
	}
	return &options.ExitError{Code: exitCode}
}

func currentEnvState(slug string, ui termio.UI) state.EnvState {
	st, err := state.ReadState(slug)
	if err != nil {
		if !errors.Is(err, state.ErrStateNotFound) {
			ui.Warnf("reading state for env fingerprint: %v (continuing)", err)
		}
		return state.EnvState{}
	}
	return st.EnvState
}

func currentSecretState(slug string, ui termio.UI) state.SecretState {
	st, err := state.ReadState(slug)
	if err != nil {
		if !errors.Is(err, state.ErrStateNotFound) {
			ui.Warnf("reading state for secret fingerprint: %v (continuing)", err)
		}
		return state.SecretState{}
	}
	return st.SecretState
}

func persistEnvSecrets(slug string, envState state.EnvState, secretState state.SecretState) error {
	st, err := state.ReadState(slug)
	if err != nil {
		if errors.Is(err, state.ErrStateNotFound) {
			st = new(state.HomeState)
		} else {
			return fmt.Errorf("read state for persistence: %w", err)
		}
	}
	st.EnvState = envState
	st.SecretState = secretState
	return state.WriteState(slug, *st)
}

// tmpMountPath is the mount point used for the sandbox tmpfs.
const tmpMountPath = "/tmp"

func buildMounts(homeVol, repoPath string, tmpSizeMiB uint32) map[string]msbSdk.MountConfig {
	return map[string]msbSdk.MountConfig{
		"/home/dev":      msbSdk.Mount.Named(homeVol, msbSdk.MountOptions{}),
		defaultTargetDir: msbSdk.Mount.Bind(repoPath, msbSdk.MountOptions{}),
		tmpMountPath: msbSdk.Mount.Tmpfs(msbSdk.TmpfsOptions{
			SizeMiB:  tmpSizeMiB,
			Readonly: false,
			Noexec:   false,
			Nosuid:   false,
			Nodev:    false,
		}),
	}
}

func setUpSandbox(
	ctx context.Context,
	sb msb.Sandbox,
	opts options.RunOptions,
	created bool,
	ui termio.UI,
	restart bool,
) (string, error) {
	cfs, err := reprovision.LoadConfigFiles(configpaths.GetConfigPaths().UserOpencodeConfigDir())
	if err != nil {
		return "", err
	}

	ui.Verbosef("expected config files: %v", cfs.Keys)

	if restart {
		restartDaemons(ctx, sb, cfs.Files, ui)
		return ResolveTarget(ctx, sb, opts.Worktree, ui)
	}

	vmData := reprovision.ReadVMFiles(ctx, sb, "/home/dev/.config/opencode", ui)
	if len(cfs.Files) > 0 && (created || len(vmData) == 0) {
		if provErr := reprovision.ProvisionSandbox(ctx, sb.FS(), cfs.Files); provErr != nil {
			ui.Warnf("provision failed: %v (continuing)", provErr)
		}
	}

	if dockerErr := startDockerdIfPresent(ctx, sb, ui); dockerErr != nil {
		return "", fmt.Errorf("docker startup: %w", dockerErr)
	}

	if daemonErr := ensureDaemon(ctx, sb, ui); daemonErr != nil {
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

	// image-change home-volume prompt runs before the rebuild decision.
	if hs.ImageDigest != imageDigest {
		action := vm.ResolveHomeAction(ui, hs.ImageDigest, imageDigest)
		if action == "4" {
			ui.Infof("exiting as requested by user")
			return false, false, homeVol, &options.ExitError{Code: 1}
		}
		newVol, err := vm.ApplyHomeAction(ctx, client, slug, homeVol, imageRef, imageDigest, action, opts, ui)
		if err != nil {
			return false, false, homeVol, fmt.Errorf("apply home action: %w", err)
		}
		homeVol = newVol
	}

	cfs, err := reprovision.LoadConfigFiles(configpaths.GetConfigPaths().UserOpencodeConfigDir())
	if err != nil {
		return false, false, homeVol, err
	}

	var opencfgChanged bool
	if liveSb != nil {
		vmData := reprovision.ReadVMFiles(ctx, liveSb, "/home/dev/.config/opencode", ui)
		opencfgChanged = len(vmData) > 0 && !reprovision.ConfigEqual(cfs.Parsed, cfs.Keys, vmData)
		if detachErr := liveSb.Detach(context.Background()); detachErr != nil {
			ui.Verbosef("failed to detach live sandbox handle: %v", detachErr)
		}
	}

	desiredEnv := reprovision.MergeEnvMaps(
		reprovision.BuildEnvMap(configpaths.GetConfigPaths().UserEnvFile()),
		reprovision.BuildEnvMap(configpaths.GetConfigPaths().ProjectEnvFile()),
	)
	desiredSecrets := reprovision.BuildSecretsFromSpecs(reprovision.MergeSecretSpecs(
		reprovision.ParseSecretSpecLegacy(configpaths.GetConfigPaths().UserEnvSecretFile(), ui),
		reprovision.ParseSecretSpecLegacy(configpaths.GetConfigPaths().ProjectEnvSecretFile(), ui),
		reprovision.ParseSecretSpecYAML(configpaths.GetConfigPaths().UserEnvSecretYAMLFile(), ui),
		reprovision.ParseSecretSpecYAML(configpaths.GetConfigPaths().ProjectEnvSecretYAMLFile(), ui),
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
	return recreate, restart, homeVol, nil
}

// restartDaemons provisions config files and restarts the opencode daemon so
// an opencode-config change is picked up. Env/secret changes are never routed
// here: they require a VM rebuild and are handled by the recreate path instead.
func restartDaemons(ctx context.Context, sb msb.Sandbox, files map[string][]byte, ui termio.UI) {
	if err := reprovision.ProvisionSandbox(ctx, sb.FS(), files); err != nil {
		ui.Warnf("provision failed: %v (keeping existing daemon)", err)
		return
	}
	ui.Infof("opencode serve restarting…")
	if _, _, err := daemonShellFunc(ctx, sb, daemonKillCmd); err != nil {
		ui.Warnf("kill stale daemon failed (continuing): %v", err)
	}
	if err := ensureDaemon(ctx, sb, ui); err != nil {
		ui.Warnf("daemon restart failed: %v (using existing)", err)
	}
}
