//go:build integration

package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-msb/internal/git"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

func TestStartDockerdIfPresentWithDindImage(t *testing.T) {
	ctx := t.Context()
	logger := newTestLogger(t)

	// Build the dind base image requires Docker on the host.
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dockerCli, err := client.New(client.FromEnv)
	if err != nil {
		t.Skipf("docker not available: %v", err)
	}
	defer dockerCli.Close()

	// Build the plain base image first (dind extends it).
	if err := buildDockerImage(
		ctx,
		dockerCli,
		EmbeddedDockerfile,
		BaseTag,
		"Building base",
		false,
		logger,
	); err != nil {
		t.Skipf("cannot build base image: %v", err)
	}

	// Build the dind base image.
	if err := buildDockerImage(
		ctx,
		dockerCli,
		EmbeddedDindDockerfile,
		DindBaseTag,
		"Building dind base",
		false,
		logger,
	); err != nil {
		t.Skipf("cannot build dind image: %v", err)
	}

	// Load into msb.
	imageRef := DindBaseTag
	if _, err := msb.Image.Get(ctx, imageRef); err != nil {
		saveResult, err := dockerCli.ImageSave(ctx, []string{DindBaseTag})
		if err != nil {
			t.Skipf("cannot export dind image: %v", err)
		}
		defer saveResult.Close()
		cmd := exec.CommandContext(ctx, "msb", "load", "--tag", imageRef)
		cmd.Stdin = saveResult
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("cannot load dind image into msb: %v: %s", err, out)
		}
	}

	sandboxName := fmt.Sprintf("test-dind-%d", time.Now().UnixNano())
	sb, err := msb.CreateSandbox(ctx, sandboxName,
		msb.WithImage(imageRef),
		msb.WithUser("dev"),
		msb.WithWorkdir("/workspace"),
		msb.WithReplace(),
	)
	if err != nil {
		t.Skipf("cannot create sandbox: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = sb.Stop(stopCtx)
		_ = sb.Close()
		_ = msb.RemoveSandbox(context.Background(), sandboxName)
	}()

	if err := startDockerdIfPresent(ctx, sb, logger); err != nil {
		t.Fatalf("startDockerdIfPresent failed: %v", err)
	}

	out, err := sb.Shell(ctx, "docker run --rm hello-world", msb.WithExecUser("dev"))
	if err != nil {
		t.Fatalf("docker run hello-world failed: %v", err)
	}
	if !out.Success() {
		t.Fatalf("docker run hello-world exited non-zero:\n%s\n%s", out.Stdout(), out.Stderr())
	}
	if !strings.Contains(out.Stdout(), "Hello from Docker!") {
		t.Errorf("expected hello-world output, got:\n%s", out.Stdout())
	}
}

func TestStartDockerdIfPresentWithPlainBaseImage(t *testing.T) {
	ctx := t.Context()
	logger := newTestLogger(t)

	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dockerCli, err := client.New(client.FromEnv)
	if err != nil {
		t.Skipf("docker not available: %v", err)
	}
	defer dockerCli.Close()

	if err := buildDockerImage(
		ctx,
		dockerCli,
		EmbeddedDockerfile,
		BaseTag,
		"Building base",
		false,
		logger,
	); err != nil {
		t.Skipf("cannot build base image: %v", err)
	}

	imageRef := BaseTag
	if _, err := msb.Image.Get(ctx, imageRef); err != nil {
		saveResult, err := dockerCli.ImageSave(ctx, []string{BaseTag})
		if err != nil {
			t.Skipf("cannot export base image: %v", err)
		}
		defer saveResult.Close()
		cmd := exec.CommandContext(ctx, "msb", "load", "--tag", imageRef)
		cmd.Stdin = saveResult
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("cannot load base image into msb: %v: %s", err, out)
		}
	}

	sandboxName := fmt.Sprintf("test-plain-%d", time.Now().UnixNano())
	sb, err := msb.CreateSandbox(ctx, sandboxName,
		msb.WithImage(imageRef),
		msb.WithUser("dev"),
		msb.WithWorkdir("/workspace"),
		msb.WithReplace(),
	)
	if err != nil {
		t.Skipf("cannot create sandbox: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = sb.Stop(stopCtx)
		_ = sb.Close()
		_ = msb.RemoveSandbox(context.Background(), sandboxName)
	}()

	// Should be a no-op on plain base (no dockerd binary).
	if err := startDockerdIfPresent(ctx, sb, logger); err != nil {
		t.Fatalf("startDockerdIfPresent should be no-op on plain base, got: %v", err)
	}
}

func TestProjectVMLifecycle(t *testing.T) {
	ctx := t.Context()
	logger := newTestLogger(t)

	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Ensure msb runtime is available.
	if err := msb.EnsureInstalled(ctx); err != nil {
		t.Skipf("msb runtime not available: %v", err)
	}

	// Build the base image (same pattern as existing integration tests).
	dockerCli, err := client.New(client.FromEnv)
	if err != nil {
		t.Skipf("docker not available: %v", err)
	}
	defer dockerCli.Close()

	if err := buildDockerImage(
		ctx,
		dockerCli,
		EmbeddedDockerfile,
		BaseTag,
		"Building base",
		false,
		logger,
	); err != nil {
		t.Skipf("cannot build base image: %v", err)
	}

	// Use a unique project slug derived from the test temp dir.
	tmpRepo := t.TempDir()
	t.Chdir(tmpRepo)
	runGitInTest(t, tmpRepo, "init", "-b", "main")
	runGitInTest(t, tmpRepo, "config", "user.email", "test@example.com")
	runGitInTest(t, tmpRepo, "config", "user.name", "Test User")
	writeFileInTest(t, tmpRepo, "README.md", "hello")
	runGitInTest(t, tmpRepo, "add", "README.md")
	runGitInTest(t, tmpRepo, "commit", "-m", "initial")

	projectSlug := git.ProjectSlug(logger)
	imageRef := BaseTag
	homeVolName := HomeVolumeName(projectSlug, "sha256:integration-test")
	// Ensure the home volume exists.
	if _, err := msb.GetVolume(ctx, homeVolName); err != nil {
		vol, volErr := msb.CreateVolume(ctx, homeVolName, msb.WithVolumeKind(msb.VolumeKindDir))
		if volErr != nil {
			t.Skipf("cannot create volume: %v", volErr)
		}
		defer func() { _ = msb.RemoveVolume(context.Background(), vol.Name()) }()
	}

	opts := RunOptions{Memory: "1G", TmpSize: "512M"}
	cfg := Config{
		StateDir:        filepath.Join(t.TempDir(), "state"),
		UserConfigDir:   t.TempDir(),
		UserLauncherDir: t.TempDir(),
	}

	// Step 1: EnsureProjectVM creates the VM.
	sb, created, err := EnsureProjectVM(ctx, opts, cfg, imageRef, homeVolName, tmpRepo, nil, logger)
	if err != nil {
		t.Fatalf("EnsureProjectVM (create): %v", err)
	}
	if !created {
		t.Fatal("expected created=true on first call")
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = sb.Detach(stopCtx)
		_ = StopProjectVM(context.Background(), true, logger)
	}()

	// Step 2: EnsureDaemon is healthy.
	if err := EnsureDaemon(ctx, sb, logger); err != nil {
		t.Fatalf("EnsureDaemon: %v", err)
	}

	// Step 3: ResolveTarget with no branch returns /workspace.
	target, err := ResolveTarget(ctx, sb, "", logger)
	if err != nil {
		t.Fatalf("ResolveTarget (no branch): %v", err)
	}
	if target != "/workspace" {
		t.Errorf("expected /workspace, got %q", target)
	}

	// Step 4: Attach a trivial command and verify it runs.
	exitCode, attachErr := sb.Attach(ctx, "/bin/bash", "-l", "-c", "echo hello")
	if attachErr != nil {
		t.Fatalf("attach failed: %v", attachErr)
	}
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}

	// Step 5: Detach and reconnect (simulates a second invocation).
	if err := sb.Detach(ctx); err != nil {
		t.Fatalf("detach failed: %v", err)
	}

	sb2, created2, err := EnsureProjectVM(ctx, opts, cfg, imageRef, homeVolName, tmpRepo, nil, logger)
	if err != nil {
		t.Fatalf("EnsureProjectVM (reconnect): %v", err)
	}
	if created2 {
		t.Error("expected created=false on second call (VM should exist)")
	}
	_ = sb2.Detach(ctx)
}

func runGitInTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s failed: %v: %s", args, dir, err, out)
	}
}

func writeFileInTest(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
