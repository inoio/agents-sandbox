package sandbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"

	"gitlab.inoio.de/inoio/opencode-msb/internal/config"
	"gitlab.inoio.de/inoio/opencode-msb/internal/git"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// configFiles holds the merged configuration and its SHA-256 hash.
type configFiles struct {
	files map[string][]byte
	hash  string
}

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
	User     string
	Auto     bool
	Args     []string
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
func isSandboxActive(status msb.SandboxStatus) bool {
	switch status {
	case msb.SandboxStatusRunning, msb.SandboxStatusDraining, msb.SandboxStatusPaused:
		return true
	case msb.SandboxStatusStopped, msb.SandboxStatusCrashed:
		return false
	}
	return false
}

func buildAttachCommand(target string, _ bool, args []string) string {
	parts := []string{"opencode", "attach", "http://127.0.0.1:4096", "--dir", target}
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
	ui stdio.UI,
) (*sandboxSession, error) {
	if !CheckAll(ctx, ui) {
		return nil, errors.New("preflight failed")
	}

	projectSlug := git.ProjectSlug(ui)

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

	imageRef, imageDigest, imageEnvs, err := EnsureImage(ctx, dockerCli, dockerfile, projectSlug, opts.Rebuild, ui)
	if err != nil {
		return nil, fmt.Errorf("image setup failed: %w", err)
	}
	ui.Verbosef("image: %s (digest=%s)", imageRef, imageDigest)

	vm := NewVolumeManager(ui)
	homeVol, err := vm.EnsureHome(ctx, projectSlug, imageDigest, imageRef, opts, ui)
	if err != nil {
		return nil, fmt.Errorf("volume setup failed: %w", err)
	}
	ui.Verbosef("home volume: %s", homeVol)

	sb, created, err := EnsureProjectVM(ctx, opts, cfg, imageRef, homeVol, cwd, imageEnvs, ui)
	if err != nil {
		return nil, err
	}
	name := projectVMName(projectSlug)

	var sandboxTarget string
	if sb == nil {
		ui.Infof("VM lifecycle skipped (--dry-run-vm)")
		sandboxTarget = resolveTargetNoBranch()
	} else {
		// Build the merged config and compute its hash.
		cfs, err := loadConfigFiles(cfg.UserConfigDir)
		if err != nil {
			return nil, err
		}

		// Read the config currently in the VM and compute its hash so we can
		// detect whether it differs from the one we are about to provision.
		vmHash, readErr := readConfigFromVM(ctx, sb)
		if readErr != nil {
			ui.Warnf("failed to read VM config: %v (proceeding without comparison)", readErr)
		}

		// Decide whether we need to provision and restart.
		if vmHash != "" && vmHash != cfs.hash {
			if daemonIsHealthy(ctx, sb) {
				action, promptErr := promptConfigChange(cfs.hash, ui)
				if promptErr != nil {
					return nil, promptErr
				}
				if action == "restart" {
					provisionCtx, cancel := context.WithTimeout(ctx, provisionTimeout)
					if provErr := provisionSandbox(provisionCtx, sb.FS(), cfs.files, cwd, ui); provErr != nil {
						cancel()
						return nil, provErr
					}
					cancel()
					if restartErr := EnsureDaemon(ctx, sb, ui); restartErr != nil {
						return nil, restartErr
					}
					ui.Infof("opencode serve restarting…")
				} else {
					ui.Infof("config change detected; proceeding without restart")
				}
			} else {
				ui.Infof("config change detected; restarting (daemon was not healthy)")
				provisionCtx, cancel := context.WithTimeout(ctx, provisionTimeout)
				if provErr := provisionSandbox(provisionCtx, sb.FS(), cfs.files, cwd, ui); provErr != nil {
					cancel()
					return nil, provErr
				}
				cancel()
				if restartErr := EnsureDaemon(ctx, sb, ui); restartErr != nil {
					return nil, restartErr
				}
			}
		} else {
			if dockerErr := ensureDockerdIfPresent(ctx, sb, ui, created); dockerErr != nil {
				return nil, fmt.Errorf("docker startup: %w", dockerErr)
			}
			if daemonErr := EnsureDaemon(ctx, sb, ui); daemonErr != nil {
				return nil, daemonErr
			}
		}

		target, err := ResolveTarget(ctx, sb, opts.Branch, ui)
		if err != nil {
			return nil, err
		}
		sandboxTarget = target
	}

	ui.Verbosef("attach target: %s", sandboxTarget)

	return &sandboxSession{
		sb:     sb,
		name:   name,
		target: sandboxTarget,
		cwd:    cwd,
	}, nil
}

// ensureDockerdIfPresent ensures dockerd is running inside the VM when the
// sandbox was freshly created and Docker-in-Docker support is requested.
func ensureDockerdIfPresent(ctx context.Context, sb *msb.Sandbox, ui stdio.UI, created bool) error {
	if created {
		return startDockerdIfPresent(ctx, sb, ui)
	}
	return nil
}

// Run creates (or reuses) the project VM, provisions config, starts opencode
// serve, and attaches a TUI client.
//
// Note: Run is called from cli.go after all flags are resolved.
func Run(ctx context.Context, opts RunOptions, cfg Config, ui stdio.UI) error {
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

	return finalizeRun(attachErr, nil, exitCode)
}

// Shell creates (or reuses) the project VM and drops the user into an
// interactive shell session, without starting opencode serve.
func Shell(ctx context.Context, opts RunOptions, cfg Config, ui stdio.UI) error {
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
	return finalizeRun(attachErr, nil, exitCode)
}

// BuildImage builds (or updates) the runner image for Docker-in-Docker support.
func BuildImage(ctx context.Context, force, dryRun bool, ui stdio.UI) error {
	if dryRun {
		ui.Infof("dry-run: Would build runner image")
		return nil
	}

	if !CheckDocker(ui) {
		return errors.New("docker not available")
	}
	projectSlug := git.ProjectSlug(ui)
	dockerfile := resolveDockerfile()
	dockerCli, err := client.New(client.FromEnv)
	if err != nil {
		return fmt.Errorf("cannot connect to Docker daemon (is dockerd running?): %w", err)
	}
	defer dockerCli.Close()

	_, _, _, err = EnsureImage(ctx, dockerCli, dockerfile, projectSlug, force, ui)
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

func promptConfigChange(_ string, ui stdio.UI) (string, error) {
	selection, err := ui.Select(
		"opencode provider config has changed. Restart the daemon to apply the new config?",
		[]stdio.Choice{
			{
				Label: "Proceed without changes (keep current config)", Key: "p",
				Description: "Daemon continues with the existing config",
			},
			{
				Label: "Restart opencode serve (apply new config)", Key: "r",
				Description: "Daemon restarts with new config; active clients disconnect",
			},
		},
		"proceed",
	)
	if err != nil {
		return "", fmt.Errorf("prompt config change: %w", err)
	}
	return selection, nil
}

// readConfigFromVM reads all JSON files inside /home/dev/.config/opencode/
// from the VM, computes a deterministic SHA-256 hash, and returns the hex
// string. Returns an empty string if the directory or its files do not exist.
func readConfigFromVM(ctx context.Context, sb *msb.Sandbox) (string, error) {
	cmd := "(cd /home/dev/.config/opencode && for f in */* */.* * .*; do [ -f \"$f\" ] && printf '\\0%s\\0' \"$f\" && cat \"$f\"; done)"
	out, err := sb.Shell(ctx, cmd)
	if err != nil {
		return "", fmt.Errorf("read vm config: %w", err)
	}
	if out == nil || out.Stdout() == "" {
		return "", nil // no config directory or no files (fresh VM)
	}
	return hashHex(out.StdoutBytes()), nil
}

// hashHex returns the hex-encoded SHA-256 digest of data.
func hashHex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// daemonIsHealthy returns true when the opencode serve daemon reports healthy.
func daemonIsHealthy(ctx context.Context, sb *msb.Sandbox) bool {
	out, err := sb.Shell(ctx, "curl -sf "+daemonHealthURL)
	if err != nil || out == nil || !out.Success() {
		return false
	}
	h, _ := parseHealthResponse(out.Stdout())
	return h
}

// loadConfigFiles builds the merged opencode configuration from the user's
// config directory, any project-specific config in .opencode-msb/opencode,
// and the embedded provider config. Returns the files and a SHA-256 hash.
func loadConfigFiles(userConfigDir string) (*configFiles, error) {
	providerCfg, err := config.LoadProviderConfig(config.EmbeddedProviderConfig)
	if err != nil {
		return nil, fmt.Errorf("load provider config: %w", err)
	}
	projectConfigDir := ""
	if _, statErr := os.Stat(".opencode-msb/opencode"); statErr == nil {
		projectConfigDir = ".opencode-msb/opencode"
	}
	files, err := config.BuildMergedConfig(userConfigDir, projectConfigDir, providerCfg)
	if err != nil {
		return nil, fmt.Errorf("merge config: %w", err)
	}

	// Compute a deterministic hash over the files so we can compare across runs.
	hash := configHash(files)
	return &configFiles{
		files: files,
		hash:  hash,
	}, nil
}

// configHash produces a deterministic SHA-256 hash over all config files.
// It concatenates the sorted set of file names and contents, separated by NUL
// bytes, so the hash is stable regardless of map iteration order.
func configHash(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	n := 0
	for _, name := range names {
		// NUL separator + filename + NUL separator + file content
		n += 1 + len(name) + 1 + len(files[name])
	}

	var buf bytes.Buffer
	buf.Grow(n)
	for _, name := range names {
		buf.WriteByte(0)
		buf.WriteString(name)
		buf.WriteByte(0)
		buf.Write(files[name])
	}
	h := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(h[:])
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

type sandboxFS interface {
	Mkdir(ctx context.Context, path string) error
	Write(ctx context.Context, path string, data []byte) error
	Remove(ctx context.Context, path string) error
}

func provisionSandbox(
	ctx context.Context,
	fs sandboxFS,
	configFiles map[string][]byte,
	repoPath string,
	ui stdio.UI,
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
			ui.Warnf("failed to remove envrc %s: %v", envrc, err)
		}
	}
	return nil
}
