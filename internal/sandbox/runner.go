package sandbox

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-msb/internal/config"
	"gitlab.inoio.de/inoio/opencode-msb/internal/git"
	"gitlab.inoio.de/inoio/opencode-msb/internal/output"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("exit code %d", e.Code)
}

type RunOptions struct {
	Branch  string
	Rebuild bool
	DryRun  bool
	CPUs    uint8
	Memory  string
	TmpSize string
	User    string
	Auto    bool
	Args    []string
}

type Config struct {
	StateDir        string
	UserConfigDir   string
	UserLauncherDir string
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
func isSandboxActive(status msb.SandboxStatus) bool {
	switch status {
	case msb.SandboxStatusRunning, msb.SandboxStatusDraining, msb.SandboxStatusPaused:
		return true
	case msb.SandboxStatusStopped, msb.SandboxStatusCrashed:
		return false
	}
	return false
}

func buildAttachCommand(target string, auto bool, args []string) string {
	parts := []string{"opencode", "attach", "http://127.0.0.1:4096", "--dir", target}
	if auto {
		parts = append(parts, autoFlag)
	}
	parts = append(parts, args...)
	return strings.Join(parts, " ")
}

func buildEnvMap(filename string) map[string]string {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil
	}
	env := make(map[string]string)
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "=") {
			parts := strings.SplitN(line, "=", envKeyValueParts)
			if len(parts) == envKeyValueParts {
				env[parts[0]] = parts[1]
			}
		}
	}
	return env
}

func mergeEnvMaps(mapsToMerge ...map[string]string) map[string]string {
	result := make(map[string]string)
	for _, m := range mapsToMerge {
		maps.Copy(result, m)
	}
	return result
}

const autoFlag = "--auto"

func buildOpencodeArgs(args []string, auto bool) []string {
	if !auto {
		return args
	}
	return append([]string{autoFlag}, args...)
}

func resolveDockerfile() []byte {
	if data, err := os.ReadFile(".opencode-msb/Dockerfile"); err == nil {
		return data
	}
	return EmbeddedDockerfile
}

func envrcFiles(workspacePath string) []string {
	entries, err := os.ReadDir(workspacePath)
	if err != nil {
		return nil
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".envrc") {
			files = append(files, entry.Name())
		}
	}
	return files
}

type sandboxSession struct {
	sb     *msb.Sandbox
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
		_ = git.PruneWorktrees(s.cwd)
	}
}

func prepareSandbox(
	ctx context.Context,
	opts RunOptions,
	cfg Config,
	logger *output.Printer,
) (*sandboxSession, error) {
	if !CheckAll(ctx, logger) {
		return nil, errors.New("preflight failed")
	}

	projectSlug := git.ProjectSlug(logger)

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get current directory: %w", err)
	}

	dockerfile := resolveDockerfile()
	dockerCli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to Docker daemon (is dockerd running?): %w", err)
	}
	defer dockerCli.Close()

	imageRef, imageDigest, err := EnsureImage(ctx, dockerCli, dockerfile, projectSlug, opts.Rebuild, logger)
	if err != nil {
		return nil, fmt.Errorf("image setup failed: %w", err)
	}
	logger.Debugf("image: %s (digest=%s)", imageRef, imageDigest)

	vm := NewVolumeManager(logger)
	homeVol, err := vm.EnsureHome(ctx, projectSlug, imageDigest, imageRef)
	if err != nil {
		return nil, fmt.Errorf("volume setup failed: %w", err)
	}
	logger.Debugf("home volume: %s", homeVol)

	sb, created, err := EnsureProjectVM(ctx, opts, cfg, imageRef, homeVol, cwd, logger)
	if err != nil {
		return nil, err
	}
	name := projectVMName(projectSlug)

	if created {
		configFiles, loadErr := loadConfigFiles(cfg.UserConfigDir)
		if loadErr != nil {
			return nil, loadErr
		}
		fs := sb.FS()
		if provisionErr := provisionSandbox(ctx, fs, configFiles, cwd, logger); provisionErr != nil {
			return nil, provisionErr
		}
		if dockerErr := startDockerdIfPresent(ctx, sb, logger); dockerErr != nil {
			return nil, fmt.Errorf("docker startup: %w", dockerErr)
		}
	}

	if daemonErr := EnsureDaemon(ctx, sb, logger); daemonErr != nil {
		return nil, daemonErr
	}

	target, err := ResolveTarget(ctx, sb, opts.Branch, logger)
	if err != nil {
		return nil, err
	}
	logger.Debugf("attach target: %s", target)

	return &sandboxSession{
		sb:     sb,
		name:   name,
		target: target,
		cwd:    cwd,
	}, nil
}

func Run(ctx context.Context, opts RunOptions, cfg Config, logger *output.Printer) error {
	session, err := prepareSandbox(ctx, opts, cfg, logger)
	if err != nil {
		return err
	}
	defer session.cleanup()

	var exitCode int
	var attachErr error
	if opts.DryRun {
		logger.Infof("dry run: VM and daemon validated, skipping opencode execution")
	} else {
		setup := buildAttachCommand(session.target, opts.Auto, opts.Args)
		// Run as a login shell so /etc/profile and ~/.profile are sourced,
		// putting tools installed under /usr/local/go/bin, ~/go/bin and
		// ~/.microsandbox/bin on PATH for opencode and its child shells.
		exitCode, attachErr = session.sb.Attach(ctx, "/bin/bash", "-l", "-c", setup)
	}

	return finalizeRun(attachErr, nil, exitCode)
}

func Shell(ctx context.Context, opts RunOptions, cfg Config, logger *output.Printer) error {
	session, err := prepareSandbox(ctx, opts, cfg, logger)
	if err != nil {
		return err
	}
	defer session.cleanup()

	// Login shell so the interactive shell inherits PATH from /etc/profile and ~/.profile.
	exitCode, attachErr := session.sb.Attach(ctx, "/bin/bash", "-l")
	return finalizeRun(attachErr, nil, exitCode)
}

func BuildImage(ctx context.Context, force bool, logger *output.Printer) error {
	if !CheckDocker(logger) {
		return errors.New("docker not available")
	}
	projectSlug := git.ProjectSlug(logger)
	dockerfile := resolveDockerfile()
	dockerCli, err := client.New(client.FromEnv)
	if err != nil {
		return fmt.Errorf("cannot connect to Docker daemon (is dockerd running?): %w", err)
	}
	defer dockerCli.Close()

	_, _, err = EnsureImage(ctx, dockerCli, dockerfile, projectSlug, force, logger)
	return err
}

func finalizeRun(attachErr, cleanupErr error, exitCode int) error {
	if attachErr != nil {
		if cleanupErr != nil {
			return errors.Join(
				fmt.Errorf("opencode session failed: %w", attachErr),
				fmt.Errorf("managed repo cleanup failed: %w", cleanupErr),
			)
		}
		return fmt.Errorf("opencode session failed: %w", attachErr)
	}
	if cleanupErr != nil {
		return fmt.Errorf("managed repo cleanup failed: %w", cleanupErr)
	}
	return &ExitError{Code: exitCode}
}

type sandboxFS interface {
	Mkdir(ctx context.Context, path string) error
	Write(ctx context.Context, path string, data []byte) error
	Remove(ctx context.Context, path string) error
}

func loadConfigFiles(userConfigDir string) (map[string][]byte, error) {
	providerCfg, err := config.LoadProviderConfig(config.EmbeddedProviderConfig)
	if err != nil {
		return nil, fmt.Errorf("load provider config: %w", err)
	}
	projectConfigDir := ""
	if _, statErr := os.Stat(".opencode-msb/opencode"); statErr == nil {
		projectConfigDir = ".opencode-msb/opencode"
	}
	configFiles, err := config.BuildMergedConfig(userConfigDir, projectConfigDir, providerCfg)
	if err != nil {
		return nil, fmt.Errorf("merge config: %w", err)
	}
	return configFiles, nil
}

func buildMounts(homeVol, repoPath string, tmpSizeMiB uint32) map[string]msb.MountConfig {
	return map[string]msb.MountConfig{
		"/home/dev":  msb.Mount.Named(homeVol, msb.MountOptions{}),
		"/workspace": msb.Mount.Bind(repoPath, msb.MountOptions{}),
		"/tmp": msb.Mount.Tmpfs(msb.TmpfsOptions{
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
	fs sandboxFS,
	configFiles map[string][]byte,
	repoPath string,
	logger *output.Printer,
) error {
	if err := fs.Mkdir(ctx, "/home/dev/.config/opencode"); err != nil {
		return fmt.Errorf("mkdir opencode config: %w", err)
	}
	for fname, data := range configFiles {
		if err := fs.Write(ctx, "/home/dev/.config/opencode/"+fname, data); err != nil {
			return fmt.Errorf("write config file %s: %w", fname, err)
		}
	}
	for _, envrc := range envrcFiles(repoPath) {
		if err := fs.Remove(ctx, "/workspace/"+envrc); err != nil {
			logger.Warnf("failed to remove envrc %s: %v", envrc, err)
		}
	}
	return nil
}
