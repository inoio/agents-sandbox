package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-msb/internal/config"
	"gitlab.inoio.de/inoio/opencode-msb/internal/git"
	"gitlab.inoio.de/inoio/opencode-msb/internal/log"
	"gitlab.inoio.de/inoio/opencode-msb/internal/prompt"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sysinfo"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("exit code %d", e.Code)
}

type RunOptions struct {
	Branch         string
	ImageRebuild   bool
	VolumeFallback bool
	ResetHome      bool
	CPUs           uint8
	Memory         string
	Auto           bool
	TestRun        bool
	Args           []string
}

type Config struct {
	StateDir      string
	UserConfigDir string
}

var vmEnv = []string{ //nolint:gochecknoglobals // static VM env defaults, never mutated
	"SANDBOX_USER=dev",
	"SHELL=/bin/bash",
}

const (
	defaultMemoryMiB   = 4096
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

func sandboxName(projectSlug, branchSlug string) string {
	name := "opencode-msb-" + projectSlug + "-" + branchSlug
	if len(name) > maxSandboxNameLen {
		name = name[:maxSandboxNameLen]
	}
	return name
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

// runningSandboxExists reports whether a sandbox with the given name exists and
// is in an active state (running, draining, or paused).
func runningSandboxExists(ctx context.Context, name string) (bool, error) {
	handle, err := msb.GetSandbox(ctx, name)
	if err != nil {
		if msb.IsKind(err, msb.ErrSandboxNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("check existing sandbox %q: %w", name, err)
	}
	return isSandboxActive(handle.Status()), nil
}

// promptExistingSession asks whether to terminate an already-running session or
// exit the current one. Returns true when the user chooses to terminate.
func promptExistingSession(name string, logger *log.Logger) (bool, error) {
	choice, err := prompt.Select(
		fmt.Sprintf("A session is already running for this project and branch (sandbox %q)", name),
		[]prompt.Choice{
			{Label: "Exit", Key: "e", Description: "Keep the running session and exit"},
			{Label: "Terminate", Key: "t", Description: "Terminate the running session and continue"},
		},
		"e",
		logger,
	)
	if err != nil {
		return false, fmt.Errorf("prompt for existing session: %w", err)
	}
	return choice == "t", nil
}

// ensureNoConflictingSession aborts the run when a live VM for the same sandbox
// name already exists and the user does not choose to terminate it. Stale
// (stopped/crashed) sandboxes are left for createSandbox's WithReplace to clean
// up; only an active session prompts.
func ensureNoConflictingSession(
	ctx context.Context,
	name, projectSlug, branch string,
	logger *log.Logger,
) error {
	running, err := runningSandboxExists(ctx, name)
	if err != nil {
		return err
	}
	if !running {
		return nil
	}
	terminate, err := promptExistingSession(name, logger)
	if err != nil {
		return err
	}
	if !terminate {
		return fmt.Errorf("a session is already running for %q on branch %q", projectSlug, branch)
	}
	return nil
}

func buildEnvMap(envExtra []string) map[string]string {
	env := make(map[string]string)
	for _, e := range vmEnv {
		parts := strings.SplitN(e, "=", envKeyValueParts)
		if len(parts) == envKeyValueParts {
			env[parts[0]] = parts[1]
		}
	}
	for _, e := range envExtra {
		parts := strings.SplitN(e, "=", envKeyValueParts)
		if len(parts) == envKeyValueParts {
			env[parts[0]] = parts[1]
		}
	}
	return env
}

const autoFlag = "--auto"

func buildOpencodeArgs(args []string, auto bool) []string {
	if !auto {
		return args
	}
	return append([]string{autoFlag}, args...)
}

func readSandboxEnv() []string {
	data, err := os.ReadFile(".opencode-msb/env")
	if err != nil {
		return nil
	}
	var env []string
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "=") {
			env = append(env, line)
		}
	}
	return env
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

func promptBranchCreation(branch string, logger *log.Logger) (string, error) {
	if !prompt.IsInteractive() {
		return "HEAD", nil
	}
	create, err := prompt.ConfirmDefault(
		fmt.Sprintf("Branch '%s' does not exist. Create it?", branch),
		true,
		logger,
	)
	if err != nil {
		return "", fmt.Errorf("prompt for branch creation: %w", err)
	}
	if !create {
		return "", fmt.Errorf("branch '%s' does not exist", branch)
	}
	baseRef, err := prompt.Input(fmt.Sprintf("Base ref for new branch '%s'", branch), "HEAD", logger)
	if err != nil {
		return "", fmt.Errorf("prompt for base ref: %w", err)
	}
	return baseRef, nil
}

func resolveWorkspace(
	cwd string,
	opts RunOptions,
	cfg Config,
	projectSlug string,
	logger *log.Logger,
) (string, string, string, bool, error) {
	var (
		workspacePath string
		branch        string
		cwdBranch     string
		created       bool
		err           error
	)
	if opts.Branch == "" {
		branch, err = git.BranchAt(cwd)
		if err != nil {
			return "", "", "", false, fmt.Errorf("unable to determine git branch: %w", err)
		}
		return cwd, branch, "", false, nil
	}

	branch = opts.Branch
	cwdBranch, err = git.BranchAt(cwd)
	if err != nil {
		return "", "", "", false, errors.New("--branch requires a git repository; current directory is not inside one")
	}
	if cwdBranch == opts.Branch {
		return cwd, branch, cwdBranch, false, nil
	}

	baseRef := "HEAD"
	if !git.BranchExists(cwd, opts.Branch) {
		baseRef, err = promptBranchCreation(opts.Branch, logger)
		if err != nil {
			return "", "", "", false, err
		}
	}

	workspacePath, created, err = git.EnsureManagedRepoFromRef(cwd, cfg.StateDir, projectSlug, opts.Branch, baseRef)
	if err != nil {
		return "", "", "", false, fmt.Errorf("managed repo setup failed: %w", err)
	}
	return workspacePath, branch, cwdBranch, created, nil
}

func cleanupManagedRepo(repoPath, cwd, cwdBranch string, opts RunOptions, logger *log.Logger) error {
	hasChanges, err := git.HasUncommittedChanges(repoPath)
	if err != nil {
		return fmt.Errorf("check uncommitted changes: %w", err)
	}

	force := false
	if hasChanges {
		var abort bool
		force, abort, err = handleUncommittedChanges(repoPath, opts.Branch, logger)
		if err != nil {
			return err
		}
		if abort {
			return nil
		}
	}

	return handleRepoCleanup(repoPath, cwd, cwdBranch, opts, force, logger)
}

func handleUncommittedChanges(repoPath, branch string, logger *log.Logger) (bool, bool, error) {
	choice, err := prompt.Select(
		fmt.Sprintf("Managed repo '%s' on branch '%s' has uncommitted changes", repoPath, branch),
		[]prompt.Choice{
			{Label: "Keep", Key: "k", Description: "Keep the managed repo with changes"},
			{Label: "Commit", Key: "c", Description: "Commit all changes before cleanup"},
			{Label: "Discard", Key: "d", Description: "Discard all changes"},
		},
		"k",
		logger,
	)
	if err != nil {
		return false, false, fmt.Errorf("prompt for uncommitted changes: %w", err)
	}
	switch choice {
	case "k":
		logger.Warn(
			fmt.Sprintf("kept managed repo '%s' on branch '%s' with uncommitted changes", repoPath, branch),
		)
		return false, true, nil
	case "c":
		if commitErr := git.CommitAll(repoPath, "opencode-msb: commit changes before cleanup"); commitErr != nil {
			if errors.Is(commitErr, git.ErrNothingToCommit) {
				logger.Info("no changes to commit; continuing cleanup")
			} else {
				return false, false, fmt.Errorf("commit all changes: %w", commitErr)
			}
		}
	case "d":
		if discardErr := git.DiscardAll(repoPath); discardErr != nil {
			return false, false, fmt.Errorf("discard all changes: %w", discardErr)
		}
		return true, false, nil
	}
	return false, false, nil
}

func handleRepoCleanup(repoPath, cwd, cwdBranch string, opts RunOptions, force bool, logger *log.Logger) error {
	choice, err := prompt.Select(
		fmt.Sprintf("Managed repo '%s' on branch '%s'", repoPath, opts.Branch),
		[]prompt.Choice{
			{Label: "Keep", Key: "k", Description: "Keep the managed repo"},
			{Label: "Remove", Key: "r", Description: "Remove managed repo, keep branch"},
			{Label: "Merge", Key: "m", Description: "Merge branch into original branch and remove managed repo"},
		},
		"r",
		logger,
	)
	if err != nil {
		return fmt.Errorf("prompt for managed repo cleanup: %w", err)
	}

	switch choice {
	case "k":
		return nil
	case "r":
		if err := git.RemoveManagedRepo(repoPath, force); err != nil {
			return fmt.Errorf("remove managed repo: %w", err)
		}
	case "m":
		targetBranch, err := prompt.Input("Merge target branch", cwdBranch, logger)
		if err != nil {
			return fmt.Errorf("prompt for merge target: %w", err)
		}
		if err := git.MergeBranchInto(cwd, repoPath, opts.Branch, targetBranch); err != nil {
			_ = git.AbortMerge(cwd)
			if rmErr := git.RemoveManagedRepo(repoPath, force); rmErr != nil {
				return fmt.Errorf(
					"branch %s was not merged into %s, and managed repo removal failed: %w (merge error: %w)",
					opts.Branch,
					targetBranch,
					rmErr,
					err,
				)
			}
			return fmt.Errorf("branch %s was not merged into %s: %w", opts.Branch, targetBranch, err)
		}
		if err := git.RemoveManagedRepo(repoPath, force); err != nil {
			return fmt.Errorf("remove managed repo after merge: %w", err)
		}
	}
	return nil
}

func Run(ctx context.Context, opts RunOptions, cfg Config, logger *log.Logger) error {
	if !CheckAll(ctx, logger) {
		return errors.New("preflight failed")
	}

	projectSlug := git.ProjectSlug(logger)

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	repoPath, branch, cwdBranch, created, err := resolveWorkspace(cwd, opts, cfg, projectSlug, logger)
	if err != nil {
		return err
	}

	dockerfile := resolveDockerfile()
	dockerCli, err := client.New(client.FromEnv)
	if err != nil {
		return fmt.Errorf("cannot connect to Docker daemon (is dockerd running?): %w", err)
	}
	defer dockerCli.Close()

	imageRef, imageDigest, err := EnsureImage(ctx, dockerCli, dockerfile, opts.ImageRebuild, logger)
	if err != nil {
		return fmt.Errorf("image setup failed: %w", err)
	}

	vm := NewVolumeManager(opts.VolumeFallback, cfg.StateDir, logger)
	homeVol, err := vm.EnsureHome(ctx, projectSlug, imageDigest, imageRef, opts.ResetHome)
	if err != nil {
		return fmt.Errorf("volume setup failed: %w", err)
	}

	configFiles, err := loadConfigFiles(cfg.UserConfigDir)
	if err != nil {
		return err
	}
	secrets := BuildSecrets(logger)
	name := sandboxName(projectSlug, git.BranchSlug(branch))

	if err = ensureNoConflictingSession(ctx, name, projectSlug, branch, logger); err != nil {
		return err
	}

	sb, err := createSandbox(ctx, name, imageRef, repoPath, homeVol, secrets, opts, logger)
	if err != nil {
		return err
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), sandboxStopTimeout)
		defer cancel()
		_ = sb.Stop(stopCtx)
		_ = sb.Close()
		_ = msb.RemoveSandbox(context.Background(), name)
	}()

	fs := sb.FS()
	if err := provisionSandbox(ctx, fs, configFiles, repoPath, logger); err != nil {
		return err
	}

	var exitCode int
	var attachErr error
	if opts.TestRun {
		logger.Info("test run: setup validated, skipping opencode execution")
	} else {
		opencodeArgs := buildOpencodeArgs(opts.Args, opts.Auto)
		setup := `exec opencode ` + strings.Join(opencodeArgs, " ")
		exitCode, attachErr = sb.Attach(ctx, "/bin/bash", "-c", setup)
	}

	var cleanupErr error
	if created {
		cleanupErr = cleanupManagedRepo(repoPath, cwd, cwdBranch, opts, logger)
	}

	return finalizeRun(attachErr, cleanupErr, exitCode)
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

func createSandbox(
	ctx context.Context,
	name, imageRef, repoPath, homeVol string,
	secrets []msb.SecretEntry,
	opts RunOptions,
	logger *log.Logger,
) (*msb.Sandbox, error) {
	cpus := opts.CPUs
	if cpus == 0 {
		cpus = sysinfo.NumCPUs()
	}
	maxMemoryGiB := sysinfo.TotalMemoryGiB()
	envExtra := readSandboxEnv()
	envMap := buildEnvMap(envExtra)
	mounts := map[string]msb.MountConfig{
		"/home/dev":  msb.Mount.Named(homeVol, msb.MountOptions{}),
		"/workspace": msb.Mount.Bind(repoPath, msb.MountOptions{}),
	}

	spin := log.NewSpinner(logger)
	spin.Start("Checking microsandbox runtime")
	if err := msb.EnsureInstalled(ctx); err != nil {
		spin.StopError(err)
		return nil, fmt.Errorf("microsandbox runtime: %w", err)
	}
	spin.Stop()

	spin = log.NewSpinner(logger)
	spin.Start("Starting sandbox VM")
	sb, err := msb.CreateSandbox(ctx, name,
		msb.WithImage(imageRef),
		msb.WithMounts(mounts),
		msb.WithSecrets(secrets...),
		msb.WithEnv(envMap),
		msb.WithUser("dev"),
		msb.WithWorkdir("/workspace"),
		msb.WithCPUs(cpus),
		msb.WithMaxCPUs(sysinfo.NumCPUs()),
		msb.WithMemory(parseMemory(opts.Memory)),
		//nolint:gosec // G115: maxMemoryGiB is physical RAM in GiB, cannot overflow uint32
		msb.WithMaxMemory(uint32(maxMemoryGiB)*mibPerGib),
		msb.WithReplace(),
	)
	if err != nil {
		spin.StopError(err)
		return nil, fmt.Errorf("create sandbox: %w", err)
	}
	spin.Stop()
	return sb, nil
}

func provisionSandbox(
	ctx context.Context,
	fs sandboxFS,
	configFiles map[string][]byte,
	repoPath string,
	logger *log.Logger,
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
			logger.Warn(fmt.Sprintf("failed to remove envrc %s: %v", envrc, err))
		}
	}
	return nil
}
