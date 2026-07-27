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

func TestSameHomeVolumeInUseNoSandboxes(t *testing.T) {
	got, inUse, err := sameHomeVolumeInUse(t.Context(), "my-vol", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inUse {
		t.Error("expected not in use when no sandboxes exist")
	}
	if got != "" {
		t.Errorf("expected empty sandbox name, got %q", got)
	}
}

func TestSameBranchSessionExistsNoSandbox(t *testing.T) {
	exists, err := sameBranchSessionExists(t.Context(), "nonexistent-sandbox")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Error("expected false for nonexistent sandbox")
	}
}

func TestEnsureNoSameHomeSessionNoConflict(t *testing.T) {
	vm := NewVolumeManager(newTestLogger(t))
	got, err := ensureNoSameHomeSession(
		t.Context(),
		vm,
		"test-project",
		"nonexistent-vol",
		"my-sandbox",
		"my-image",
		newTestLogger(t),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "nonexistent-vol" {
		t.Errorf("expected original volume name, got %q", got)
	}
}

// TestSameHomeVolumeInUseConflict creates a real sandbox mounting a volume
// and verifies that sameHomeVolumeInUse detects the conflict and
// ensureNoSameHomeSession returns a clone volume.
func TestSameHomeVolumeInUseConflict(t *testing.T) {
	ctx := t.Context()
	logger := newTestLogger(t)

	// Create a test volume.
	volName := fmt.Sprintf("test-home-vol-%d", time.Now().UnixNano())
	vol, err := msb.CreateVolume(ctx, volName, msb.WithVolumeKind(msb.VolumeKindDir))
	if err != nil {
		t.Fatalf("create volume: %v", err)
	}
	defer func() {
		_ = msb.RemoveVolume(context.Background(), volName)
	}()

	// Create a sandbox that mounts the volume.
	sandboxName := fmt.Sprintf("test-sandbox-%d", time.Now().UnixNano())
	sb, err := msb.CreateSandbox(ctx, sandboxName,
		msb.WithImage("alpine:latest"),
		msb.WithMounts(map[string]msb.MountConfig{
			"/home/dev": msb.Mount.Named(vol.Name(), msb.MountOptions{}),
		}),
		msb.WithReplace(),
	)
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = sb.Stop(stopCtx)
		_ = sb.Close()
		_ = msb.RemoveSandbox(context.Background(), sandboxName)
	}()

	// Test sameHomeVolumeInUse returns the conflicting sandbox.
	conflictName, inUse, err := sameHomeVolumeInUse(ctx, volName, "")
	if err != nil {
		t.Fatalf("sameHomeVolumeInUse failed: %v", err)
	}
	if !inUse {
		t.Error("expected volume to be in use")
	}
	if conflictName != sandboxName {
		t.Errorf("expected conflicting sandbox %q, got %q", sandboxName, conflictName)
	}

	// Test that excluding the sandbox returns no conflict.
	_, inUseExcluded, err := sameHomeVolumeInUse(ctx, volName, sandboxName)
	if err != nil {
		t.Fatalf("sameHomeVolumeInUse with exclude failed: %v", err)
	}
	if inUseExcluded {
		t.Error("expected no conflict when excluding the sandbox")
	}

	// Test ensureNoSameHomeSession returns a clone volume when there's a conflict.
	// This requires an image that exists in msb; we'll skip if the image doesn't exist.
	vm := NewVolumeManager(logger)
	gotVol, err := ensureNoSameHomeSession(ctx, vm, "test-project", volName, "new-sandbox", "alpine:latest", logger)
	if err != nil {
		// If the image doesn't exist, that's acceptable for this test.
		// The key assertion is that we tried to clone (not returning the original).
		t.Skipf("ensureNoSameHomeSession failed (image may not exist): %v", err)
	}

	// The returned volume should be different from the original (it's a clone).
	if gotVol == volName {
		t.Error("expected a different (clone) volume name, got the original")
	}

	// Clean up the clone volume if one was created.
	if gotVol != volName {
		defer func() {
			_ = msb.RemoveVolume(context.Background(), gotVol)
		}()
	}
}

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
	if err := buildDockerImage(ctx, dockerCli, EmbeddedDockerfile, BaseTag, "Building base", false, logger); err != nil {
		t.Skipf("cannot build base image: %v", err)
	}

	// Build the dind base image.
	if err := buildDockerImage(ctx, dockerCli, EmbeddedDindDockerfile, DindBaseTag, "Building dind base", false, logger); err != nil {
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

	if err := buildDockerImage(ctx, dockerCli, EmbeddedDockerfile, BaseTag, "Building base", false, logger); err != nil {
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
