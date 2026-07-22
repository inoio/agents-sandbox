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
	Worktree       string
	ImageRebuild   bool
	VolumeFallback bool
	ResetHome      bool
	CPUs           uint8
	Memory         string
	Auto           bool
	Args           []string
}

type Config struct {
	StateDir      string
	UserConfigDir string
}

var vmEnv = []string{
	"SANDBOX_USER=dev",
	"SHELL=/bin/bash",
}

func parseMemory(spec string) uint32 {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return 4096
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
		return 4096
	}
	return uint32(n) * multiplier
}

func sandboxName(projectSlug, branchSlug string) string {
	name := "opencode-msb-" + projectSlug + "-" + branchSlug
	if len(name) > 128 {
		name = name[:128]
	}
	return name
}

func buildEnvMap(envExtra []string) map[string]string {
	env := make(map[string]string)
	for _, e := range vmEnv {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			env[parts[0]] = parts[1]
		}
	}
	for _, e := range envExtra {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			env[parts[0]] = parts[1]
		}
	}
	return env
}

func buildOpencodeArgs(args []string, auto bool) []string {
	if !auto {
		return args
	}
	return append([]string{"--auto"}, args...)
}

func readSandboxEnv() []string {
	data, err := os.ReadFile(".opencode-msb/env")
	if err != nil {
		return nil
	}
	var env []string
	for _, line := range strings.Split(string(data), "\n") {
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

func envrcFiles(worktreePath string) []string {
	entries, err := os.ReadDir(worktreePath)
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

func resolveWorkspace(cwd string, opts RunOptions, cfg Config, projectSlug string, logger *log.Logger) (wtPath string, branch string, cwdBranch string, created bool, err error) {
	if opts.Worktree == "" {
		branch, err = git.BranchAt(cwd)
		if err != nil {
			return "", "", "", false, fmt.Errorf("unable to determine git branch: %w", err)
		}
		return cwd, branch, "", false, nil
	}

	branch = opts.Worktree
	cwdBranch, err = git.BranchAt(cwd)
	if err != nil {
		return "", "", "", false, fmt.Errorf("--worktree requires a git repository; current directory is not inside one")
	}
	if cwdBranch == opts.Worktree {
		return cwd, branch, cwdBranch, false, nil
	}

	baseRef := "HEAD"
	if !git.BranchExists(cwd, opts.Worktree) {
		if prompt.IsInteractive() {
			create, promptErr := prompt.ConfirmDefault(fmt.Sprintf("Branch '%s' does not exist. Create it from HEAD?", opts.Worktree), true, logger)
			if promptErr != nil {
				return "", "", "", false, fmt.Errorf("prompt for branch creation: %w", promptErr)
			}
			if !create {
				return "", "", "", false, fmt.Errorf("branch '%s' does not exist", opts.Worktree)
			}
			baseRef, promptErr = prompt.Input(fmt.Sprintf("Base ref for new branch '%s'", opts.Worktree), "HEAD", logger)
			if promptErr != nil {
				return "", "", "", false, fmt.Errorf("prompt for base ref: %w", promptErr)
			}
		}
	}

	wtPath, created, err = git.EnsureWorktreeFromRef(cwd, cfg.StateDir, projectSlug, opts.Worktree, baseRef)
	if err != nil {
		return "", "", "", false, fmt.Errorf("worktree setup failed: %w", err)
	}
	return wtPath, branch, cwdBranch, created, nil
}

func cleanupWorktree(wtPath, cwd, cwdBranch string, opts RunOptions, logger *log.Logger) error {
	hasChanges, err := git.HasUncommittedChanges(wtPath)
	if err != nil {
		return fmt.Errorf("check uncommitted changes: %w", err)
	}

	force := false
	if hasChanges {
		choice, err := prompt.Select(fmt.Sprintf("Worktree '%s' on branch '%s' has uncommitted changes", wtPath, opts.Worktree), []prompt.Choice{
			{Label: "Keep", Key: "k", Description: "Keep the worktree with changes"},
			{Label: "Commit", Key: "c", Description: "Commit all changes before cleanup"},
			{Label: "Discard", Key: "d", Description: "Discard all changes"},
		}, "k", logger)
		if err != nil {
			return fmt.Errorf("prompt for uncommitted changes: %w", err)
		}
		switch choice {
		case "k":
			logger.Warn(fmt.Sprintf("kept worktree '%s' on branch '%s' with uncommitted changes", wtPath, opts.Worktree))
			return nil
		case "c":
			if err := git.CommitAll(wtPath, "opencode-msb: commit changes before cleanup"); err != nil {
				if errors.Is(err, git.ErrNothingToCommit) {
					logger.Info("no changes to commit; continuing cleanup")
				} else {
					return fmt.Errorf("commit all changes: %w", err)
				}
			}
		case "d":
			if err := git.DiscardAll(wtPath); err != nil {
				return fmt.Errorf("discard all changes: %w", err)
			}
			force = true
		}
	}

	choice, err := prompt.Select(fmt.Sprintf("Worktree '%s' on branch '%s'", wtPath, opts.Worktree), []prompt.Choice{
		{Label: "Keep", Key: "k", Description: "Keep the worktree"},
		{Label: "Remove", Key: "r", Description: "Remove worktree, keep branch"},
		{Label: "Merge", Key: "m", Description: "Merge branch into original branch and remove worktree"},
	}, "r", logger)
	if err != nil {
		return fmt.Errorf("prompt for worktree cleanup: %w", err)
	}

	switch choice {
	case "k":
		return nil
	case "r":
		if err := git.RemoveWorktree(wtPath, force); err != nil {
			return fmt.Errorf("remove worktree: %w", err)
		}
	case "m":
		targetBranch, err := prompt.Input("Merge target branch", cwdBranch, logger)
		if err != nil {
			return fmt.Errorf("prompt for merge target: %w", err)
		}
		if err := git.MergeBranchInto(cwd, opts.Worktree, targetBranch); err != nil {
			_ = git.AbortMerge(cwd)
			if rmErr := git.RemoveWorktree(wtPath, force); rmErr != nil {
				return fmt.Errorf("branch %s was not merged into %s, and worktree removal failed: %v (merge error: %w)", opts.Worktree, targetBranch, rmErr, err)
			}
			return fmt.Errorf("branch %s was not merged into %s: %w", opts.Worktree, targetBranch, err)
		}
		if err := git.RemoveWorktree(wtPath, force); err != nil {
			return fmt.Errorf("remove worktree after merge: %w", err)
		}
	}
	return nil
}

func Run(ctx context.Context, opts RunOptions, cfg Config, logger *log.Logger) error {
	if !CheckAll(ctx, logger) {
		return fmt.Errorf("preflight failed")
	}

	projectSlug := git.ProjectSlug(logger)

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	wtPath, branch, cwdBranch, created, err := resolveWorkspace(cwd, opts, cfg, projectSlug, logger)
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

	providerCfg, err := config.LoadProviderConfig(config.EmbeddedProviderConfig)
	if err != nil {
		return fmt.Errorf("load provider config: %w", err)
	}
	projectConfigDir := ""
	if _, err := os.Stat(".opencode-msb/opencode"); err == nil {
		projectConfigDir = ".opencode-msb/opencode"
	}
	configFiles, err := config.BuildMergedConfig(cfg.UserConfigDir, projectConfigDir, providerCfg)
	if err != nil {
		return fmt.Errorf("merge config: %w", err)
	}
	secrets := BuildSecrets(logger)
	cpus := opts.CPUs
	if cpus == 0 {
		cpus = sysinfo.NumCPUs()
	}
	maxMemoryGiB := sysinfo.TotalMemoryGiB()
	name := sandboxName(projectSlug, git.BranchSlug(branch))

	envExtra := readSandboxEnv()
	envMap := buildEnvMap(envExtra)

	mounts := map[string]msb.MountConfig{
		"/home/dev":  msb.Mount.Named(homeVol, msb.MountOptions{}),
		"/workspace": msb.Mount.Bind(wtPath, msb.MountOptions{}),
	}

	spin := log.NewSpinner(logger)
	spin.Start("Checking microsandbox runtime")
	if err := msb.EnsureInstalled(ctx); err != nil {
		spin.StopError(err)
		return fmt.Errorf("microsandbox runtime: %w", err)
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
		msb.WithMaxMemory(uint32(maxMemoryGiB)*1024),
		msb.WithReplace(),
	)
	if err != nil {
		spin.StopError(err)
		return fmt.Errorf("create sandbox: %w", err)
	}
	spin.Stop()
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = sb.Stop(stopCtx)
		_ = sb.Close()
		_ = msb.RemoveSandbox(context.Background(), name)
	}()

	fs := sb.FS()
	if err := fs.Mkdir(ctx, "/home/dev/.config/opencode"); err != nil {
		return fmt.Errorf("mkdir opencode config: %w", err)
	}
	for fname, data := range configFiles {
		if err := fs.Write(ctx, "/home/dev/.config/opencode/"+fname, data); err != nil {
			return fmt.Errorf("write config file %s: %w", fname, err)
		}
	}
	for _, envrc := range envrcFiles(wtPath) {
		if err := fs.Remove(ctx, "/workspace/"+envrc); err != nil {
			logger.Warn(fmt.Sprintf("failed to remove envrc %s: %v", envrc, err))
		}
	}

	opencodeArgs := buildOpencodeArgs(opts.Args, opts.Auto)
	setup := `exec opencode ` + strings.Join(opencodeArgs, " ")
	exitCode, attachErr := sb.Attach(ctx, "/bin/bash", "-c", setup)

	if created {
		if cleanupErr := cleanupWorktree(wtPath, cwd, cwdBranch, opts, logger); cleanupErr != nil {
			return cleanupErr
		}
	}

	if attachErr != nil {
		return fmt.Errorf("opencode session failed: %w", attachErr)
	}
	return &ExitError{Code: exitCode}
}
