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
	provisionTimeout   = 15 * time.Second
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

	action := vm.ResolveHomeAction(ui, state.ImageDigest, imageDigest)
	if action == actionQuit {
		ui.Infof("exiting as requested by user")
		return nil, &ExitError{Code: 1}
	}
	homeVol, err = vm.ApplyHomeAction(ctx, client, projectSlug, homeVol, imageRef, imageDigest, action, opts, ui)
	if err != nil {
		return nil, fmt.Errorf("apply home volume action: %w", err)
	}
	ui.Verbosef("home volume after action: %s", homeVol)

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get current directory: %w", err)
	}
	sb, _, err := EnsureProjectVM(ctx, opts, cfg, imageRef, homeVol, cwd, imageEnvs, ui)
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
		sandboxTarget, sandboxErr = setUpSandbox(ctx, sb, opts, cfg, cwd, ui)
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

	var exitCode int
	var attachErr error
	setup := buildAttachCommand(session.target, opts.Auto, opts.Args)
	ui.Verbosef("%s", setup)
	// Run as a login shell so /etc/profile and ~/.profile are sourced,
	// putting tools installed under /usr/local/go/bin, ~/go/bin and
	// ~/.microsandbox/bin on PATH for opencode and its child shells.
	exitCode, attachErr = session.sb.Attach(ctx, "/bin/bash", "-l", "-c", setup)

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

	// Login shell so the interactive shell inherits PATH from /etc/profile and ~/.profile.
	exitCode, attachErr := session.sb.Attach(ctx, "/bin/bash", "-l")
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

// setUpSandbox handles all sandbox setup after the VM is running.
func setUpSandbox(
	ctx context.Context,
	sb Sandbox,
	opts RunOptions,
	cfg Config,
	_ string,
	ui termio.UI,
) (string, error) {
	cfs, err := loadConfigFiles(cfg.UserOpenCodeConfigDir())
	if err != nil {
		return "", err
	}

	ui.Verbosef("expected config files: %v", cfs.keys)

	vmData := readVMFiles(ctx, sb, "/home/dev/.config/opencode", ui)

	if len(vmData) > 0 {
		if !configEqual(cfs.parsed, cfs.keys, vmData) {
			handleConfigChange(ctx, sb, cfs, ui)
			return ResolveTarget(ctx, sb, opts.Branch, ui)
		}
	} else {
		ui.Verbosef("no VM config found (fresh setup)")
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

// handleConfigChange provisions the sandbox and restarts the daemon if required.
func handleConfigChange(ctx context.Context, sb Sandbox, cfs *configFiles, ui termio.UI) {
	daemonHealthy := daemonIsHealthy(ctx, sb)
	if daemonHealthy {
		action, promptErr := promptConfigChange(ui)
		if promptErr != nil {
			_ = promptErr
			return
		}
		if action == "r" {
			ensureProvisionedAndRunning(ctx, sb.FS(), cfs.files, sb, ui)
			return
		}
		ui.Infof("config change detected; proceeding without restart")
		return
	}
	restartUnhealthyDaemon(ctx, sb, cfs.files, ui)
}

func ensureProvisionedAndRunning(
	ctx context.Context,
	fs SandboxFS,
	files map[string][]byte,
	sb Sandbox,
	ui termio.UI,
) {
	if provErr := provisionSandbox(ctx, fs, files); provErr != nil {
		ui.Warnf("provision failed: %v (keeping existing daemon)", provErr)
		return
	}
	ui.Infof("opencode serve restarting…")
	if _, _, err := daemonShellFunc(ctx, sb, daemonKillCmd); err != nil {
		ui.Warnf("kill stale daemon failed (continuing): %v", err)
	}
	if restartErr := EnsureDaemon(ctx, sb, ui); restartErr != nil {
		ui.Warnf("daemon restart failed: %v (keeping existing)", restartErr)
	}
}

func restartUnhealthyDaemon(ctx context.Context, sb Sandbox, files map[string][]byte, ui termio.UI) {
	provisionCtx, cancel := context.WithTimeout(ctx, provisionTimeout)
	defer cancel()

	ui.Infof("config change detected; restarting (daemon was not healthy)")
	if provErr := provisionSandbox(provisionCtx, sb.FS(), files); provErr != nil {
		ui.Warnf("provision failed: %v (using existing config)", provErr)
		return
	}
	if restartErr := EnsureDaemon(ctx, sb, ui); restartErr != nil {
		ui.Warnf("daemon restart failed: %v (using existing)", restartErr)
	}
}
