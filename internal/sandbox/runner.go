package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"

	"gitlab.inoio.de/inoio/opencode-msb/internal/git"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("exit code %d", e.Code)
}

type RunOptions struct {
	ReapPolicy ReapPolicy

	IdleTimeout time.Duration

	Branch   string
	Rebuild  bool
	DryRun   bool
	DryRunVM bool
	CPUs     uint8
	Memory   string
	TmpSize  string
	DiskSize string
	User     string
	Auto     bool
	Args     []string

	// Recreate forces a project-VM rebuild on this invocation. It is set by
	// prepareSandbox from the reconfig decision and is never user-facing.
	Recreate bool
}

type Config struct {
	UserStateDir  string
	UserConfigDir string
	UserCacheDir  string
}

// UserOpenCodeConfigDir returns the directory of the opencode server's own
// user config, nested under the tool's user config base.
func (c Config) UserOpenCodeConfigDir() string {
	return filepath.Join(c.UserConfigDir, configDirName)
}

// UserEnvFile returns the user-level environment definitions file.
func (c Config) UserEnvFile() string {
	return filepath.Join(c.UserConfigDir, envFileName)
}

// UserEnvSecretFile returns the user-level secret environment definitions file.
func (c Config) UserEnvSecretFile() string {
	return filepath.Join(c.UserConfigDir, envSecretFileName)
}

const (
	defaultMemoryMiB   = 4096
	defaultTmpSizeMiB  = 2048
	maxSandboxNameLen  = 128
	sandboxStopTimeout = 30 * time.Second
	envKeyValueParts   = 2
	mibPerGib          = 1024
)

func parseMemory(spec string) uint32 {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return defaultMemoryMiB
	}
	multiplier := uint32(1)
	last := spec[len(spec)-1]
	rest := spec
	switch last {
	case 'g', 'G':
		multiplier = 1024
		rest = spec[:len(spec)-1]
	case 'm', 'M':
		multiplier = 1
		rest = spec[:len(spec)-1]
	}
	n, err := strconv.Atoi(rest)
	if err != nil {
		return defaultMemoryMiB
	}
	return uint32(n) * multiplier //nolint:gosec // G115: n is from Atoi on a memory spec, bounded by realistic values
}

func resolveTmpSizeMiB(spec string) uint32 {
	if spec == "" {
		return defaultTmpSizeMiB
	}
	return parseMemory(spec)
}

// isSandboxActive reports whether a sandbox status represents a live VM that
// WithReplace would terminate. Stopped or crashed sandboxes are stale state
// that can be replaced silently.
func isSandboxActive(status msbSdk.SandboxStatus) bool {
	switch status {
	case msbSdk.SandboxStatusRunning, msbSdk.SandboxStatusDraining, msbSdk.SandboxStatusPaused:
		return true
	case msbSdk.SandboxStatusStopped, msbSdk.SandboxStatusCrashed:
		return false
	}
	return false
}

func buildAttachCommand(target string, _ bool, args []string) string {
	parts := []string{"opencode", "attach", "http://127.0.0.1:4096", "--dir", target}
	parts = append(parts, args...)

	return strings.Join(parts, " ")
}

func buildOpencodeArgs(args []string, auto bool) []string {
	if !auto {
		return args
	}
	return append([]string{autoFlag}, args...)
}

func resolveDockerfile() []byte {
	if data, err := os.ReadFile(projectDockerfile()); err == nil {
		return data
	}
	return EmbeddedDockerfile
}

type sandboxSession struct {
	sb     Sandbox
	name   string
	target string
	cwd    string
}

func (s *sandboxSession) cleanup() {
	if s.sb != nil {
		_ = s.sb.Detach(context.Background())
	}
	// Run git worktree prune on the host repo to clean up stale entries.
	if s.cwd != "" {
		_ = git.PruneWorktrees(context.Background(), s.cwd)
	}
}

func prepareSandbox(
	ctx context.Context,
	opts RunOptions,
	cfg Config,
	ui termio.UI,
) (*sandboxSession, error) {
	if !CheckAll(ctx, ui) {
		return nil, errors.New("preflight failed")
	}

	projectSlug := git.ProjectSlug(ui)

	imageRef, imageDigest, imageEnvs, err := EnsureImage(ctx, projectSlug, opts.Rebuild, ui)
	if err != nil {
		return nil, fmt.Errorf("image setup failed: %w", err)
	}
	ui.Verbosef("Using image '%s' (digest=%s)", imageRef, imageDigest)

	vm := NewVolumeManager(ui)
	client := msb.Get()
	homeVol, state, err := vm.ResolveHomeVolume(ctx, client, projectSlug, imageDigest, imageRef, opts, ui)
	if err != nil {
		return nil, fmt.Errorf("volume setup failed: %w", err)
	}
	ui.Verbosef("home volume: %s", homeVol)

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get current directory: %w", err)
	}
	recreate, restart, restartDockerd, homeVol, err := decideReconfig(
		ctx,
		client,
		vm,
		opts,
		cfg,
		imageRef,
		imageDigest,
		homeVol,
		state,
		ui,
	)
	if err != nil {
		return nil, err
	}
	ui.Verbosef("recreate: %v, restart: %v, restartDockerd: %v", recreate, restart, restartDockerd)
	opts.Recreate = recreate
	sb, created, err := EnsureProjectVM(ctx, opts, cfg, imageRef, homeVol, cwd, imageEnvs, ui)
	if err != nil {
		return nil, err
	}
	name := projectVMName(projectSlug)

	var sandboxTarget string
	var sandboxErr error
	if sb == nil {
		ui.Infof("VM lifecycle skipped (--dry-run-vm)")
		sandboxTarget = resolveTargetNoBranch()
	} else {
		sandboxTarget, sandboxErr = setUpSandbox(ctx, sb, opts, cfg, cwd, created, ui, restart, restartDockerd)
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
func Run(ctx context.Context, opts RunOptions, cfg Config, ui termio.UI) error {
	session, err := prepareSandbox(ctx, opts, cfg, ui)
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
	release, acquireErr := AcquireClientLease(projectSlug)
	if acquireErr != nil {
		ui.Warnf("client lease failed: %v", acquireErr)
	}
	defer func() {
		if acquireErr == nil && release != nil {
			release()
		}
	}()

	var exitCode int
	var attachErr error
	setup := buildAttachCommand(session.target, opts.Auto, opts.Args)
	ui.Verbosef("%s", setup)
	// Run as a login shell so /etc/profile and ~/.profile are sourced,
	// putting tools installed under /usr/local/go/bin, ~/go/bin and
	// ~/.microsandbox/bin on PATH for opencode and its child shells.
	exitCode, attachErr = session.sb.Attach(ctx, "/bin/bash", "-l", "-c", setup)

	// Explicitly release the lease after attach returns, before reaping.
	// This ensures CountActiveClients reflects only OTHER live clients.
	// The deferred release above is a safety net.
	if acquireErr == nil {
		release()
		release = nil
	}

	if err := ReapOnLastClient(ctx, projectSlug, session.sb, opts.ReapPolicy, ui); err != nil {
		ui.Warnf("reap failed: %v", err)
	}

	return finalizeRun(attachErr, exitCode)
}

// Shell creates (or reuses) the project VM and drops the user into an
// interactive shell session, without starting opencode serve.
func Shell(ctx context.Context, opts RunOptions, cfg Config, ui termio.UI) error {
	session, err := prepareSandbox(ctx, opts, cfg, ui)
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
	release, acquireErr := AcquireClientLease(projectSlug)
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
	// This ensures CountActiveClients reflects only OTHER live clients.
	// The deferred release above is a safety net.
	if acquireErr == nil {
		release()
		release = nil
	}

	if err := ReapOnLastClient(ctx, projectSlug, session.sb, opts.ReapPolicy, ui); err != nil {
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

	if !CheckDocker(ui) {
		return errors.New("docker not available")
	}
	projectSlug := git.ProjectSlug(ui)

	_, _, _, err := ensureImageWithClient(ctx, msb.Get(), resolveDockerfile(), projectSlug, force, ui)
	return err
}

func finalizeRun(attachErr error, exitCode int) error {
	if attachErr != nil {
		return fmt.Errorf("opencode session failed: %w", attachErr)
	}
	return &ExitError{Code: exitCode}
}

func buildMounts(homeVol, repoPath string, tmpSizeMiB uint32) map[string]msbSdk.MountConfig {
	return map[string]msbSdk.MountConfig{
		"/home/dev":  msbSdk.Mount.Named(homeVol, msbSdk.MountOptions{}),
		"/workspace": msbSdk.Mount.Bind(repoPath, msbSdk.MountOptions{}),
		"/tmp": msbSdk.Mount.Tmpfs(msbSdk.TmpfsOptions{
			SizeMiB:  tmpSizeMiB,
			Readonly: false,
			Noexec:   false,
			Nosuid:   false,
			Nodev:    false,
		}),
	}
}

func provisionSandbox(
	ctx context.Context,
	fs SandboxFS,
	configFiles map[string][]byte,
) error {
	if err := fs.Mkdir(ctx, "/home/dev/.config/opencode"); err != nil {
		return fmt.Errorf("mkdir opencode config: %w", err)
	}
	for fname, data := range configFiles {
		if err := fs.Write(ctx, "/home/dev/.config/opencode/"+fname, data); err != nil {
			return fmt.Errorf("write config file %s: %w", fname, err)
		}
	}
	return nil
}

func setUpSandbox(
	ctx context.Context,
	sb Sandbox,
	opts RunOptions,
	cfg Config,
	_ string,
	created bool,
	ui termio.UI,
	restart bool,
	restartDockerd bool,
) (string, error) {
	cfs, err := loadConfigFiles(cfg.UserOpenCodeConfigDir())
	if err != nil {
		return "", err
	}

	ui.Verbosef("expected config files: %v", cfs.keys)

	if restart {
		if restartDockerd {
			applyEnvAndSecrets(ctx, cfg, ui)
		}
		restartDaemons(ctx, sb, cfs.files, restartDockerd, ui)
		return ResolveTarget(ctx, sb, opts.Branch, ui)
	}

	if len(cfs.files) > 0 && created {
		if provErr := provisionSandbox(ctx, sb.FS(), cfs.files); provErr != nil {
			ui.Warnf("provision failed: %v (continuing)", provErr)
		}
	}

	if dockerErr := startDockerdIfPresent(ctx, sb, ui); dockerErr != nil {
		return "", fmt.Errorf("docker startup: %w", dockerErr)
	}

	if daemonErr := EnsureDaemon(ctx, sb, ui); daemonErr != nil {
		return "", daemonErr
	}

	return ResolveTarget(ctx, sb, opts.Branch, ui)
}

// applyEnvAndSecrets fetches the live VM handle and applies the current
// desire env/secret configuration via the SDK Modify API. It is called on the
// reuse+restart path so that a daemon restart picks up the new values.
func applyEnvAndSecrets(ctx context.Context, cfg Config, ui termio.UI) {
	slug := git.ProjectSlug(ui)
	handle, err := msb.Get().GetSandbox(ctx, projectVMName(slug))
	if err != nil {
		ui.Warnf("could not fetch VM handle to apply env/secrets: %v (continuing)", err)
		return
	}

	desiredEnv := mergeEnvMaps(buildEnvMap(cfg.UserEnvFile()), buildEnvMap(ProjectEnvFile()))
	desiredSecrets := BuildSecrets(mergeEnvMaps(
		buildEnvMap(cfg.UserEnvSecretFile()),
		buildEnvMap(ProjectEnvSecretFile()),
	), ui)

	if _, err := reconcileEnvAndSecrets(ctx, handle, desiredEnv, desiredSecrets); err != nil {
		ui.Warnf("env/secret application failed: %v (continuing)", err)
	}
}

// decideReconfig centralizes all reconfiguration decisions: the image-change
// home-volume prompt, the VM recreate decision, the daemon-restart decision,
// and cpu/memory staging. It re-fetches the existing sandbox handle to read
// live VM files (no passed-in Sandbox is available at the call site).
//
// Note: the plan's literal signature included an sb Sandbox parameter, but
// this is never available to decideReconfig in prepareSandbox (it runs
// before EnsureProjectVM, before any sandbox exists). The function re-fetches
// and Connect()s its own sandbox for file reads.
func decideReconfig(
	ctx context.Context,
	client MsbClient,
	vm *VolumeManager,
	opts RunOptions, cfg Config,
	imageRef, imageDigest, homeVol string, state HomeState,
	ui termio.UI,
) (bool, bool, bool, string, error) {
	slug := git.ProjectSlug(ui)
	handle, _ := client.GetSandbox(ctx, projectVMName(slug))

	var curCfg *msbSdk.SandboxConfig
	var liveSb Sandbox
	if handle != nil {
		curCfg, _ = handle.Config()
		liveSb, _ = handle.Connect(ctx)
	}

	// image-change home-volume prompt runs before the rebuild decision.
	if state.ImageDigest != imageDigest {
		action := vm.ResolveHomeAction(ui, state.ImageDigest, imageDigest)
		if action == actionQuit {
			return false, false, false, homeVol, &ExitError{Code: 1}
		}
		newVol, err := vm.ApplyHomeAction(ctx, client, slug, homeVol, imageRef, imageDigest, action, opts, ui)
		if err != nil {
			return false, false, false, homeVol, fmt.Errorf("apply home action: %w", err)
		}
		homeVol = newVol
	}

	cfs, err := loadConfigFiles(cfg.UserOpenCodeConfigDir())
	if err != nil {
		return false, false, false, homeVol, err
	}

	var opencfgChanged bool
	if liveSb != nil {
		vmData := readVMFiles(ctx, liveSb, "/home/dev/.config/opencode", ui)
		opencfgChanged = len(vmData) > 0 && !configEqual(cfs.parsed, cfs.keys, vmData)
	}

	desiredEnv := mergeEnvMaps(buildEnvMap(cfg.UserEnvFile()), buildEnvMap(ProjectEnvFile()))
	desiredSecrets := BuildSecrets(mergeEnvMaps(
		buildEnvMap(cfg.UserEnvSecretFile()),
		buildEnvMap(ProjectEnvSecretFile()),
	), ui)
	envChanged, secretsChanged := envOrSecretsChanged(curCfg, desiredEnv, desiredSecrets)

	plan := planReconfig(curCfg, imageRef, opts, envChanged, secretsChanged, opencfgChanged)
	otherClients := CountActiveClients(slug)
	applyRecreate, applyRestart, err := resolveReconfig(ctx, ui, plan, otherClients, plan.changes)
	if err != nil {
		return false, false, false, homeVol, err
	}
	recreate := applyRecreate
	restart := applyRestart && !recreate && !plan.recreate
	return recreate, restart, restart && plan.restartDockerd, homeVol, nil
}

// restartDaemons provisions config files and restarts the daemons a config
// change requires. restartDockerd additionally restarts dockerd (for env/secret
// changes) so newly applied process env/settings take effect.
func restartDaemons(ctx context.Context, sb Sandbox, files map[string][]byte, restartDockerd bool, ui termio.UI) {
	if err := provisionSandbox(ctx, sb.FS(), files); err != nil {
		ui.Warnf("provision failed: %v (keeping existing daemon)", err)
		return
	}
	if restartDockerd {
		ui.Infof("restarting dockerd for env/secret change…")
		if err := startDockerdIfPresent(ctx, sb, ui); err != nil {
			ui.Warnf("dockerd restart failed (continuing): %v", err)
		}
	}
	ui.Infof("opencode serve restarting…")
	if _, _, err := daemonShellFunc(ctx, sb, daemonKillCmd); err != nil {
		ui.Warnf("kill stale daemon failed (continuing): %v", err)
	}
	if err := EnsureDaemon(ctx, sb, ui); err != nil {
		ui.Warnf("daemon restart failed: %v (using existing)", err)
	}
}
