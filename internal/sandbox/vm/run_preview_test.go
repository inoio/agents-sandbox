package vm

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/client"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	sandboximage "github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// dockerfileIDLabel is the image label carrying the runner image content
// identity (image.dockerfileIDLabelKey, which is unexported).
const dockerfileIDLabel = "org.agents-sandbox.dockerfile-id"

// agentVersionLabel is the image label carrying the baked agent release
// (image.agentVersionLabelKey, which is unexported).
const agentVersionLabel = "org.agents-sandbox.identity.agent-version"

// absentImageDockerMock makes the local image inspect report a missing image, so
// the preview reports "would build" for the "not present locally" reason.
func absentImageDockerMock(t *testing.T) {
	t.Helper()
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(
			context.Context, string, ...client.ImageInspectOption,
		) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, errors.New("no such image")
		},
	})
}

// noVolumeCreateMock fails the test if the preview ever creates a volume.
func noVolumeCreateMock(t *testing.T) {
	t.Helper()
	msb.WithMsbMock(t, &msb.MockMsbClient{
		CreateVolumeFn: func(
			context.Context, string, ...msbSdk.VolumeOption,
		) (msb.VolumeHandle, error) {
			t.Fatal("PreviewSandbox must not create a volume")
			return nil, errors.New("unexpected CreateVolume")
		},
	})
}

func previewKey() state.Key {
	return state.Key{Slug: git.MustCheckoutSlug(), Agent: "opencode"}
}

func TestPreviewSandboxReportsBuildAndCreate(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	absentImageDockerMock(t)
	noVolumeCreateMock(t)

	ui := termio.NewTestMock(t)
	p, err := PreviewSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, &ui)
	if err != nil {
		t.Fatalf("PreviewSandbox() error = %v", err)
	}
	if !p.BuildImage {
		t.Errorf("BuildImage = false, want true (reasons %v)", p.BuildReasons)
	}
	if !slices.Contains(p.BuildReasons, "runner image not present locally") {
		t.Errorf("BuildReasons = %v, want the absent-image reason", p.BuildReasons)
	}
	if !p.CreateHome {
		t.Error("CreateHome = false, want true with no recorded volume")
	}
	if p.HomeVolume != "" {
		t.Errorf("HomeVolume = %q, want empty on the create branch", p.HomeVolume)
	}
	if p.ResolveVersionAtBuild != true {
		t.Error("ResolveVersionAtBuild = false, want true when no version is known yet")
	}
	if p.UpgradeCheckSkipped {
		t.Error("UpgradeCheckSkipped = true, want false when resolution is deferred")
	}
}

func TestPreviewSandboxReportsReuse(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a := opencodeAgent(t)

	id, err := sandboximage.RunnerDockerfileID(a, "1.0.0", options.DockerSetting{})
	if err != nil {
		t.Fatalf("RunnerDockerfileID: %v", err)
	}
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(
			context.Context, string, ...client.ImageInspectOption,
		) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{dockerfileIDLabel: id},
				},
			}, nil
		},
	})
	k := previewKey()
	msb.WithMsbMock(t, &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "vol-1"}},
	})
	if err := seedHomeState(k, state.HomeState{HomeVolume: "vol-1", AgentVersion: "1.0.0"}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	ui := termio.NewTestMock(t)
	p, err := PreviewSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, &ui)
	if err != nil {
		t.Fatalf("PreviewSandbox() error = %v", err)
	}
	if p.BuildImage {
		t.Errorf("BuildImage = true, want false (reasons %v)", p.BuildReasons)
	}
	if p.CreateHome {
		t.Error("CreateHome = true, want false for a present recorded volume")
	}
	if p.HomeVolume != "vol-1" {
		t.Errorf("HomeVolume = %q, want %q", p.HomeVolume, "vol-1")
	}
	if p.AgentVersion != "1.0.0" {
		t.Errorf("AgentVersion = %q, want %q", p.AgentVersion, "1.0.0")
	}
	if p.ResolveVersionAtBuild {
		t.Error("ResolveVersionAtBuild = true, want false for a recorded version")
	}
	if !p.UpgradeCheckSkipped {
		t.Error("UpgradeCheckSkipped = false, want true for a known recorded version")
	}
}

// TestPreviewSandboxReconcilesStaleRecordedVersionWithImageLabel pins that the
// preview lets the existing image's label win over a disagreeing recorded
// version, matching the real run: a stale record must not make the preview
// report an older version or a phantom rebuild.
func TestPreviewSandboxReconcilesStaleRecordedVersionWithImageLabel(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	// TestMain stubs the seam to "no image"; restore the real reader so the
	// docker mock's label is what supplies the version.
	orig := existingAgentVersion
	existingAgentVersion = sandboximage.ExistingAgentVersion
	t.Cleanup(func() { existingAgentVersion = orig })
	a := opencodeAgent(t)

	id, err := sandboximage.RunnerDockerfileID(a, "2.0.0", options.DockerSetting{})
	if err != nil {
		t.Fatalf("RunnerDockerfileID: %v", err)
	}
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(
			context.Context, string, ...client.ImageInspectOption,
		) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{
						dockerfileIDLabel: id,
						agentVersionLabel: "2.0.0",
					},
				},
			}, nil
		},
	})
	k := previewKey()
	msb.WithMsbMock(t, &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "vol-1"}},
	})
	if err := seedHomeState(k, state.HomeState{HomeVolume: "vol-1", AgentVersion: "1.0.0"}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	ui := termio.NewTestMock(t)
	p, err := PreviewSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, &ui)
	if err != nil {
		t.Fatalf("PreviewSandbox() error = %v", err)
	}
	if p.AgentVersion != "2.0.0" {
		t.Errorf("AgentVersion = %q, want the image label %q", p.AgentVersion, "2.0.0")
	}
	if p.BuildImage {
		t.Errorf("BuildImage = true, want false (the real run reuses; reasons %v)", p.BuildReasons)
	}
}

func TestPreviewSandboxRecoversVersionFromImageLabelReadOnly(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	// The seam is what supplies the recovered version; the docker mock below
	// only feeds PreviewImage's image decision.
	orig := existingAgentVersion
	existingAgentVersion = func(context.Context, string, string) (string, bool) { return "1.0.0", true }
	t.Cleanup(func() { existingAgentVersion = orig })
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(
			context.Context, string, ...client.ImageInspectOption,
		) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{agentVersionLabel: "1.0.0"},
				},
			}, nil
		},
	})
	noVolumeCreateMock(t)

	ui := termio.NewTestMock(t)
	p, err := PreviewSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, &ui)
	if err != nil {
		t.Fatalf("PreviewSandbox() error = %v", err)
	}
	if p.AgentVersion != "1.0.0" {
		t.Errorf("AgentVersion = %q, want the recovered %q", p.AgentVersion, "1.0.0")
	}
	if p.ResolveVersionAtBuild {
		t.Error("ResolveVersionAtBuild = true, want false for a recovered version")
	}
	if !p.UpgradeCheckSkipped {
		t.Error("UpgradeCheckSkipped = false, want true for a recovered known version")
	}
	if _, err := os.Stat(filepath.Join(state.KeyDir(previewKey()), "state.yaml")); !os.IsNotExist(err) {
		t.Errorf("preview recorded a HomeState: stat error = %v, want not-exist", err)
	}
}

func TestPreviewSandboxPinnedVersionWinsAndNeverChecksNetwork(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	absentImageDockerMock(t)
	noVolumeCreateMock(t)

	orig := SetAgentLatestVersion(func(context.Context, agent.Agent) (string, error) {
		t.Fatal("PreviewSandbox must not run the upgrade check")
		return "", nil
	})
	t.Cleanup(func() { SetAgentLatestVersion(orig) })

	ui := termio.NewTestMock(t)
	p, err := PreviewSandbox(context.Background(), options.RunOptions{Agent: "opencode", AgentVersion: "2.0.0"}, &ui)
	if err != nil {
		t.Fatalf("PreviewSandbox() error = %v", err)
	}
	if p.AgentVersion != "2.0.0" {
		t.Errorf("AgentVersion = %q, want the pin %q", p.AgentVersion, "2.0.0")
	}
	if p.UpgradeCheckSkipped {
		t.Error("UpgradeCheckSkipped = true, want false for an explicit pin (nothing to disclose)")
	}
}

func TestPreviewSandboxCorruptStateReportsCreate(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	absentImageDockerMock(t)
	noVolumeCreateMock(t)

	k := previewKey()
	if err := os.MkdirAll(state.KeyDir(k), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	statePath := filepath.Join(state.KeyDir(k), "state.yaml")
	corrupt := []byte("{ corrupted: yaml: [")
	testutil.WriteFile(t, state.KeyDir(k), "state.yaml", string(corrupt))

	ui := termio.NewTestMock(t)
	p, err := PreviewSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, &ui)
	if err != nil {
		t.Fatalf("PreviewSandbox() error = %v, want a tolerated corrupt state", err)
	}
	if !p.CreateHome {
		t.Error("CreateHome = false, want true for a corrupt state with no live mount")
	}
	got, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, corrupt) {
		t.Errorf("state file changed: got %q, want %q", got, corrupt)
	}
}

func TestPreviewSandboxSurfacesRenderError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	noVolumeCreateMock(t)

	dockerfilePath := configpaths.Get().ProjectDockerfile()
	if err := os.MkdirAll(filepath.Dir(dockerfilePath), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	testutil.WritePath(t, dockerfilePath, "FROM ubuntu:24.04 AS agents-sandbox-base\n")

	ui := termio.NewTestMock(t)
	if _, err := PreviewSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, &ui); err == nil {
		t.Fatal("expected the render error for a reserved stage alias to surface")
	}
}

func TestPreviewSandboxUnknownAgentErrors(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	ui := termio.NewTestMock(t)
	if _, err := PreviewSandbox(context.Background(), options.RunOptions{Agent: "nope"}, &ui); err == nil {
		t.Fatal("expected an error for an unknown agent")
	}
}

func TestPreviewSandboxWorkspaceDirError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Remove(dir); err != nil {
		t.Skipf("cannot remove cwd: %v", err)
	}

	ui := termio.NewTestMock(t)
	if _, err := PreviewSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, &ui); err == nil {
		t.Fatal("expected an error when the workspace directory cannot be resolved")
	}
}

// TestPreviewSandboxUserProvidedAgentIgnoresPin pins the user-provided-agent
// precedence: like resolveBuildVersion, the preview ignores --agent-version for
// an agent the tool does not own, so it reports the recorded version and the
// reuse a real run would get instead of a phantom rebuild.
func TestPreviewSandboxUserProvidedAgentIgnoresPin(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a := opencodeAgent(t)
	k := previewKey()

	id, err := sandboximage.RunnerDockerfileID(a, "1.0.0", options.DockerSetting{})
	if err != nil {
		t.Fatalf("RunnerDockerfileID: %v", err)
	}
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(
			context.Context, string, ...client.ImageInspectOption,
		) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{dockerfileIDLabel: id},
				},
			}, nil
		},
	})
	noVolumeCreateMock(t)
	seedAgentState(t, k, "1.0.0", agentSourceUser)

	orig := SetAgentLatestVersion(func(context.Context, agent.Agent) (string, error) {
		t.Fatal("PreviewSandbox must not run the upgrade check for a user-provided agent")
		return "", nil
	})
	t.Cleanup(func() { SetAgentLatestVersion(orig) })

	ui := termio.NewTestMock(t)
	p, err := PreviewSandbox(context.Background(), options.RunOptions{Agent: "opencode", AgentVersion: "9.9.9"}, &ui)
	if err != nil {
		t.Fatalf("PreviewSandbox() error = %v", err)
	}
	if p.AgentVersion != "1.0.0" {
		t.Errorf("AgentVersion = %q, want the recorded %q (the pin must be ignored)", p.AgentVersion, "1.0.0")
	}
	if p.BuildImage {
		t.Errorf("BuildImage = true, want false (the real run reuses the 1.0.0 image; reasons %v)", p.BuildReasons)
	}
	if p.UpgradeCheckSkipped {
		t.Error("UpgradeCheckSkipped = true, want false for a user-provided agent (never upgrade-checked)")
	}
}

// TestPreviewSandboxUserProvidedAgentNeverRecoversVersion pins that a
// user-provided agent with no recorded version defers resolution to build time
// instead of recovering a version from the image label, matching the real run.
func TestPreviewSandboxUserProvidedAgentNeverRecoversVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	absentImageDockerMock(t)
	noVolumeCreateMock(t)
	seedAgentState(t, previewKey(), "", agentSourceUser)

	orig := existingAgentVersion
	existingAgentVersion = func(context.Context, string, string) (string, bool) {
		t.Fatal("PreviewSandbox must not recover a version for a user-provided agent")
		return "", false
	}
	t.Cleanup(func() { existingAgentVersion = orig })

	ui := termio.NewTestMock(t)
	p, err := PreviewSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, &ui)
	if err != nil {
		t.Fatalf("PreviewSandbox() error = %v", err)
	}
	if !p.ResolveVersionAtBuild {
		t.Error("ResolveVersionAtBuild = false, want true for a user-provided agent with no recorded version")
	}
	if p.UpgradeCheckSkipped {
		t.Error("UpgradeCheckSkipped = true, want false when resolution is deferred")
	}
}

// TestPreviewSandboxSurfacesAdoptionError covers the pending-legacy-identity
// error arm: a corrupt legacy state document must surface from the read-only
// adoption check rather than be silently ignored.
func TestPreviewSandboxSurfacesAdoptionError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacySlug := git.MustRepoSlug()
	if legacySlug == git.MustCheckoutSlug() {
		t.Skip("no distinct legacy identity in this checkout")
	}
	legacyKey := state.Key{Slug: legacySlug, Agent: "opencode"}
	if err := os.MkdirAll(state.KeyDir(legacyKey), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	testutil.WriteFile(t, state.KeyDir(legacyKey), "state.yaml", `": [`)

	ui := termio.NewTestMock(t)
	if _, err := PreviewSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, &ui); err == nil {
		t.Fatal("expected the pending-identity check error to surface")
	}
}

// TestPreviewSandboxSurfacesHomeVolumeLookupError covers the home-volume
// lookup-error arm: a recorded volume whose lookup fails transiently (not true
// absence) must surface from PreviewHomeVolume rather than be reported as
// "would create", matching the real run's fail-closed check.
func TestPreviewSandboxSurfacesHomeVolumeLookupError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	msb.WithMsbMock(t, &msb.MockMsbClient{
		GetVolumeFn: func(context.Context, string) (msb.VolumeHandle, error) {
			return nil, errors.New("daemon unavailable")
		},
	})
	if err := seedHomeState(previewKey(), state.HomeState{HomeVolume: "vol-1"}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	ui := termio.NewTestMock(t)
	if _, err := PreviewSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, &ui); err == nil {
		t.Fatal("expected the home-volume lookup error to surface")
	}
}

func TestPreviewSandboxForcedRebuildReportsBuild(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	noVolumeCreateMock(t)

	ui := termio.NewTestMock(t)
	p, err := PreviewSandbox(context.Background(), options.RunOptions{Rebuild: true}, &ui)
	if err != nil {
		t.Fatalf("PreviewSandbox() error = %v", err)
	}
	if !p.BuildImage {
		t.Error("BuildImage = false, want true for --rebuild")
	}
	if !slices.Contains(p.BuildReasons, "forced by --rebuild") {
		t.Errorf("BuildReasons = %v, want the forced reason", p.BuildReasons)
	}
}
