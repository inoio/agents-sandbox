package vm

import (
	"context"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
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
		volume.NewManager(&ui),
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
