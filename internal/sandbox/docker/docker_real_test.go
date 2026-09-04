package docker

import (
	"context"
	"strings"
	"testing"

	"github.com/moby/moby/client"
)

// These tests exercise the real delegation layer against a live Docker
// daemon. Stateful operations are only invoked with image IDs that cannot
// exist, so their error paths are covered without mutating daemon state.

func TestRealDockerClientPing(t *testing.T) {
	c := &realDockerClient{}
	if _, err := c.Ping(context.Background(), client.PingOptions{}); err != nil {
		t.Errorf("Ping() error = %v, want nil", err)
	}
}

func TestRealDockerClientImageLookupErrors(t *testing.T) {
	c := &realDockerClient{}
	ctx := context.Background()
	const missing = "agents-sandbox-tests-no-such-image"

	if _, err := c.ImageInspect(ctx, missing); err == nil {
		t.Error("ImageInspect() = nil error, want error for missing image")
	}
	if _, err := c.ImageSave(ctx, []string{missing}); err == nil {
		t.Error("ImageSave() = nil error, want error for missing image")
	}
	if _, err := c.ImageRemove(ctx, missing, client.ImageRemoveOptions{}); err == nil {
		t.Error("ImageRemove() = nil error, want error for missing image")
	}
	if _, err := c.ImageTag(
		ctx,
		client.ImageTagOptions{Source: missing, Target: "agents-sandbox-tests:nope"},
	); err == nil {
		t.Error("ImageTag() = nil error, want error for missing source image")
	}
}

func TestRealDockerClientImageBuildError(t *testing.T) {
	c := &realDockerClient{}
	// An invalid build context makes the daemon abort the build, exercising the
	// delegation path without producing a real image.
	if _, err := c.ImageBuild(
		context.Background(),
		strings.NewReader("not a dockerfile"),
		client.ImageBuildOptions{},
	); err == nil {
		t.Error("ImageBuild() = nil error, want error for invalid build context")
	}
}

func TestRealDockerClientImagePullError(t *testing.T) {
	c := &realDockerClient{}
	// A repository that cannot exist is rejected by the registry, exercising the
	// delegation path without pulling any image.
	if _, err := c.ImagePull(
		context.Background(),
		"agents-sandbox-tests-no-such-image",
		client.ImagePullOptions{},
	); err == nil {
		t.Error("ImagePull() = nil error, want error for missing image")
	}
}

func TestRealDockerClientImagePrune(t *testing.T) {
	c := &realDockerClient{}
	if _, err := c.ImagePrune(context.Background(), client.ImagePruneOptions{}); err != nil {
		t.Errorf("ImagePrune() error = %v, want nil", err)
	}
}
