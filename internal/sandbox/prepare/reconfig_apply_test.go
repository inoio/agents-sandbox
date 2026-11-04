package prepare

import (
	"context"
	"errors"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestApplyReconfigHomeActionReturnsNewVolume covers the success path of
// applyReconfigHomeAction: a resolved migrate action creates a fresh home
// volume, and the new volume — not the old one — is returned so the run mounts
// what the action actually produced.
func TestApplyReconfigHomeActionReturnsNewVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	const oldVolume = "home-applyproj-old"
	key := state.Key{Slug: "applyproj", Agent: "opencode"}
	if err := seedHomeState(key, state.HomeState{HomeVolume: oldVolume, ImageDigest: "sha256:old"}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	action := volume.ActionMigrate
	decision := reconfigDecision{Recreate: true, HomeAction: &action}

	ui := termio.NewTestMock(t)
	newVolume, err := applyReconfigHomeAction(
		context.Background(),
		&msb.MockMsbClient{},
		volume.NewManager(docker.Get()),
		key,
		oldVolume,
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
		false,
		decision,
		&ui,
	)
	if err != nil {
		t.Fatalf("applyReconfigHomeAction: %v", err)
	}
	if newVolume == "" || newVolume == oldVolume {
		t.Fatalf("newVolume = %q, want a fresh volume differing from %q", newVolume, oldVolume)
	}
}

// TestApplyReconfigHomeActionError covers the branch where applying the
// chosen home-volume action fails and the reconfig aborts with that error.
func TestApplyReconfigHomeActionError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := reconfigMockClient()
	createErr := errors.New("create volume failed")
	mock.CreateVolumeFn = func(_ context.Context, _ string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return nil, createErr
	}
	msb.WithMsbMock(t, mock)

	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(prompt string, _ []termio.Choice, _ string) (string, error) {
			if contains(prompt, "Docker image changed") {
				return "m", nil // migrate -> triggers ApplyHomeAction -> CreateVolume
			}
			return "", nil
		},
	}
	volumes := volume.NewManager(docker.Get())

	persisted := state.HomeState{HomeVolume: "vol", ImageDigest: "sha256:old"}

	cfs, err := reprovision.LoadConfigFilesForHost(opencodeResolved(t), t.TempDir(), reprovision.VMHomeDir, ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	decision, err := readAndResolveReconfigDecision(
		context.Background(),
		mock,
		volumes,
		state.Key{Slug: "testproj", Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:new", Ref: "img:new", Digest: "sha256:new"},
		"vol",
		persisted,
		cfs,
		ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	_, err = applyReconfigHomeAction(
		context.Background(),
		mock,
		volumes,
		state.Key{Slug: "testproj", Agent: "opencode"},
		"vol",
		image.ImageInfo{DockerTag: "img:new", Ref: "img:new", Digest: "sha256:new"},
		false,
		decision,
		ui,
	)
	if err == nil {
		t.Fatal("expected an error when applying the home-volume action fails")
	}
	if !contains(err.Error(), "apply home action") {
		t.Errorf("error = %v, want it to mention the home action", err)
	}
	if !errors.Is(err, createErr) {
		t.Errorf("error = %v, want it to wrap the create-volume failure", err)
	}
}
