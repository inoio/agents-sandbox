//go:build integration

package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

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
