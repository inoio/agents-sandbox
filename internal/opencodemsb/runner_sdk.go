//go:build cgo

package opencodemsb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	m "github.com/superradcompany/microsandbox/sdk/go"
)

func runCommand(opts RunOptions) error {
	ctx := context.Background()
	tick, summary := newTiming(opts.Timing)
	defer summary()

	if !CheckAll(ctx) {
		return fmt.Errorf("preflight failed")
	}
	tick("preflight")

	projectSlug := ProjectSlug()
	branch := opts.Worktree
	if branch == "" {
		var err error
		branch, err = BranchName(".")
		if err != nil {
			return fmt.Errorf("unable to determine git branch: %w", err)
		}
	}
	tick("project/branch resolution")

	cwd, _ := os.Getwd()
	wtPath, err := CurrentWorktreePath(cwd)
	if err != nil || wtPath == "" {
		wtPath, err = EnsureWorktree(cwd, stateDir, projectSlug, branch)
		if err != nil {
			return fmt.Errorf("worktree setup failed: %w", err)
		}
	}
	tick("worktree resolution")

	dockerfile := resolveDockerfile()
	imageRef, imageDigest, err := EnsureImage(ctx, dockerfile, opts.ImageRebuild)
	if err != nil {
		return fmt.Errorf("image setup failed: %w", err)
	}
	tick("image hash/check/build")

	vm := NewVolumeManager(opts.VolumeFallback, stateDir)
	homeVol, err := vm.EnsureHome(ctx, projectSlug, imageDigest, imageRef, opts.ResetHome)
	if err != nil {
		return fmt.Errorf("volume setup failed: %w", err)
	}
	tick("volume ensure")

	providerCfg, err := LoadProviderConfig(EmbeddedProviderConfig)
	if err != nil {
		return fmt.Errorf("load provider config: %w", err)
	}
	projectConfigDir := ""
	if _, err := os.Stat(".sandbox/opencode"); err == nil {
		projectConfigDir = ".sandbox/opencode"
	}
	configFiles, err := BuildMergedConfig(userConfigDir, projectConfigDir, providerCfg)
	if err != nil {
		return fmt.Errorf("merge config: %w", err)
	}
	secrets := BuildSecrets()
	cpus := opts.CPUs
	if cpus == 0 {
		cpus = NumCPUs()
	}
	maxMemoryGiB := TotalMemoryGiB()
	name := sandboxName(projectSlug, BranchSlug(branch))
	tick("config/secrets")

	envExtra := readSandboxEnv()
	envMap := buildEnvMap(envExtra)

	mounts := map[string]m.MountConfig{
		"/home/dev":           m.Mount.Named(homeVol, m.MountOptions{}),
		"/home/dev/workspace": m.Mount.Bind(wtPath, m.MountOptions{}),
	}

	if err := m.EnsureInstalled(ctx); err != nil {
		return fmt.Errorf("microsandbox runtime: %w", err)
	}

	sb, err := m.CreateSandbox(ctx, name,
		m.WithImage(imageRef),
		m.WithMounts(mounts),
		m.WithSecrets(secrets...),
		m.WithEnv(envMap),
		m.WithUser("dev"),
		m.WithWorkdir("/home/dev/workspace"),
		m.WithCPUs(cpus),
		m.WithMaxCPUs(NumCPUs()),
		m.WithMemory(parseMemory(opts.Memory)),
		m.WithMaxMemory(uint32(maxMemoryGiB)*1024),
		m.WithReplace(),
	)
	if err != nil {
		return fmt.Errorf("create sandbox: %w", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = sb.Stop(stopCtx)
		_ = sb.Close()
		_ = m.RemoveSandbox(context.Background(), name)
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
		if err := fs.Remove(ctx, "/home/dev/workspace/"+envrc); err != nil {
			warn(fmt.Sprintf("failed to remove envrc %s: %v", envrc, err))
		}
	}
	tick("config setup")

	setup := `eval "$(goenv init -)" && exec opencode ` + strings.Join(opts.Args, " ")
	exitCode, err := sb.Attach(ctx, "/bin/bash", "-lc", setup)
	tick("opencode session")

	if err != nil {
		return fmt.Errorf("opencode session failed: %w", err)
	}
	return &exitError{code: exitCode}
}
