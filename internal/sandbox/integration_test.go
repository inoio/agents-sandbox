//go:build integration

package sandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-msb/internal/git"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

func TestStartDockerdIfPresentWithDindImage(t *testing.T) {
	ctx := t.Context()
	ui := testutil.NewTestio(t)

	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dockerCli, err := client.New(client.FromEnv)
	if err != nil {
		t.Skipf("docker not available: %v", err)
	}
	defer dockerCli.Close()

	sb, cleanup, err := setupTestSandbox(t, ctx, dockerCli, &ui, DindBaseTag, "dind")
	if err != nil {
		t.Skipf("setup failed: %v", err)
	}
	defer cleanup()

	if err := startDockerdIfPresent(ctx, realSandbox{sandbox: sb}, &ui); err != nil {
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
	ui := testutil.NewTestio(t)

	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dockerCli, err := client.New(client.FromEnv)
	if err != nil {
		t.Skipf("docker not available: %v", err)
	}
	defer dockerCli.Close()

	sb, cleanup, err := setupTestSandbox(t, ctx, dockerCli, &ui, BaseTag, "plain")
	if err != nil {
		t.Skipf("setup failed: %v", err)
	}
	defer cleanup()

	// Should be a no-op on plain base (no dockerd binary).
	if err := startDockerdIfPresent(ctx, realSandbox{sandbox: sb}, &ui); err != nil {
		t.Fatalf("startDockerdIfPresent should be no-op on plain base, got: %v", err)
	}
}

func TestProjectVMLifecycle(t *testing.T) {
	ctx := t.Context()
	testUI := testutil.NewTestio(t)
	ui := &testUI

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
		ui,
	); err != nil {
		t.Skipf("cannot build base image: %v", err)
	}

	// Use a unique project slug derived from the test temp dir.
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	projectSlug := git.ProjectSlug(ui)
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
	sb, created, err := EnsureProjectVM(ctx, opts, cfg, imageRef, homeVolName, tmpRepo, nil, ui)
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
		_ = StopProjectVM(context.Background(), true, false, ui)
	}()

	// Step 2: EnsureDaemon is healthy.
	if err := EnsureDaemon(ctx, sb, ui); err != nil {
		t.Fatalf("EnsureDaemon: %v", err)
	}

	// Step 3: ResolveTarget with no branch returns /workspace.
	target, err := ResolveTarget(ctx, sb, "", ui)
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

	// Step 5: Detach and reconnect (simulates a second invocatuin).
	if err := sb.Detach(ctx); err != nil {
		t.Fatalf("detach failed: %v", err)
	}

	sb2, created2, err := EnsureProjectVM(ctx, opts, cfg, imageRef, homeVolName, tmpRepo, nil, ui)
	if err != nil {
		t.Fatalf("EnsureProjectVM (reconnect): %v", err)
	}
	if created2 {
		t.Error("expected created=false on second call (VM should exist)")
	}
	_ = sb2.Detach(ctx)
}

// setupTestSandbox handles shared setup: skip, ensure base image, ensure image
// loaded, and create a sandbox. Returns the sandbox, a cleanup function, and
// any error encountered.
func setupTestSandbox(
	t *testing.T,
	ctx context.Context,
	dockerCli *client.Client,
	ui *termio.Mock,
	imageRef, sbNameFmt string,
) (*msb.Sandbox, func(), error) {
	t.Helper()

	if err := buildDockerImage(ctx, dockerCli, EmbeddedDockerfile, BaseTag, "Building base", false, ui); err != nil {
		return nil, nil, fmt.Errorf("base image: %w", err)
	}

	if _, err := msb.Image.Get(ctx, imageRef); err != nil {
		saveResult, err := dockerCli.ImageSave(ctx, []string{imageRef})
		if err != nil {
			return nil, nil, fmt.Errorf("export: %w", err)
		}
		defer saveResult.Close()
		cmd := exec.CommandContext(ctx, "msb", "load", "--tag", imageRef)
		cmd.Stdin = saveResult
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, nil, fmt.Errorf("load: %w: %s", err, out)
		}
	}

	sbName := fmt.Sprintf(sbNameFmt, time.Now().UnixNano())
	sb, err := msb.CreateSandbox(ctx, sbName,
		msb.WithImage(imageRef),
		msb.WithUser("dev"),
		msb.WithWorkdir("/workspace"),
		msb.WithReplace(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create: %w", err)
	}

	cleanup := func() {
		ctx2, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = sb.Stop(ctx2)
		_ = sb.Close()
		_ = msb.RemoveSandbox(context.Background(), sbName)
	}
	return sb, cleanup, nil
}

// buildDockerImage builds a Docker image using the provided client.
func buildDockerImage(
	ctx context.Context,
	dockerCli *client.Client,
	dockerfile []byte,
	tag string,
	label string,
	force bool,
	ui termio.UI,
) error {
	spinner := ui.Spinner(label)
	tarBuf, err := dockerfileTar(dockerfile)
	if err != nil {
		return fmt.Errorf("create build context: %w", err)
	}
	buildResp, err := dockerCli.ImageBuild(
		ctx, tarBuf,
		client.ImageBuildOptions{
			Tags:      []string{tag},
			Remove:    true,
			NoCache:   force,
			BuildArgs: userBuildArgs(),
		},
	)
	if err != nil {
		spinner.StopError(err)
		return fmt.Errorf("docker image build failed: %w", err)
	}
	buildErr := scanBuildOutput(buildResp.Body, ui)
	_ = buildResp.Body.Close()
	if buildErr != nil {
		spinner.StopError(buildErr)
		if strings.Contains(buildErr.Error(), "pull access denied") {
			return fmt.Errorf("docker image build failed (base image not found or not logged in): %w", buildErr)
		}
		return fmt.Errorf("docker image build failed: %w", buildErr)
	}
	spinner.Stop()
	return nil
}

// dockerfileTar creates a minimal Docker build context tar from a Dockerfile.
func dockerfileTar(dockerfile []byte) (*bytes.Buffer, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name: "Dockerfile",
		Mode: 0o644,
		Size: int64(len(dockerfile)),
	}); err != nil {
		return nil, fmt.Errorf("tar write header: %w", err)
	}
	if _, err := io.Copy(tw, bytes.NewReader(dockerfile)); err != nil {
		_ = tw.Close()
		return nil, fmt.Errorf("tar write dockerfile: %w", err)
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("tar close: %w", err)
	}
	return &buf, nil
}

// userBuildArgs returns Docker build arguments that align the in-image dev user
// with the host user that owns the bind-mounted /workspace.
func userBuildArgs() map[string]*string {
	u := strconv.Itoa(os.Getuid())
	g := strconv.Itoa(os.Getgid())
	return map[string]*string{
		"USER_UID": &u,
		"USER_GID": &g,
	}
}

type dockerBuildMessage struct {
	Stream      string      `json:"stream,omitempty"`
	Error       string      `json:"error,omitempty"`
	ErrorDetail dockerError `json:"errorDetail,omitempty"`
}

type dockerError struct {
	Message string `json:"message"`
}

func scanBuildOutput(r io.Reader, ui termio.UI) error {
	dec := json.NewDecoder(r)
	for {
		var msg dockerBuildMessage
		if err := dec.Decode(&msg); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("unexpected Docker build output: %w", err)
		}
		if msg.Error != "" {
			return fmt.Errorf("%s", msg.Error)
		}
		if msg.ErrorDetail.Message != "" {
			return fmt.Errorf("%s", msg.ErrorDetail.Message)
		}
		trimmedMsg := strings.Trim(msg.Stream, " \n")
		if trimmedMsg != "" {
			ui.Verbose(trimmedMsg)
		}
	}
}
