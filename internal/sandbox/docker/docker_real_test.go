//go:build integration

package docker

import (
	"context"
	"strings"
	"testing"

	"github.com/moby/moby/client"
)

// These integration tests exercise the real delegation layer against a live
// Docker daemon. Stateful operations are only invoked with image IDs that
// cannot exist, so their error paths are covered without mutating daemon state.
// They are excluded from the main suite via the `integration` build tag (see
// `make test-integration`) because they require a working Docker daemon, and
// TestRealDockerClientImagePullError also needs network access to a registry.

func TestRealDockerClientPing(t *testing.T) {
	c := RealClient()
	if _, err := c.Ping(context.Background(), client.PingOptions{}); err != nil {
		t.Errorf("Ping() error = %v, want nil", err)
	}
}

func TestRealDockerClientImageLookupErrors(t *testing.T) {
	c := RealClient()
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
	c := RealClient()
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
	c := RealClient()
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
	c := RealClient()
	// Prune with a filter that matches no image, so the call exercises the
	// delegation path without deleting dangling images. A plain prune removes
	// the classic builder's untagged layer-cache images, which the concurrent
	// image integration tests rely on (and which developers may rely on for
	// unrelated local builds).
	filters := make(client.Filters).Add("label", "agents-sandbox-tests.no-such-image=true")
	if _, err := c.ImagePrune(context.Background(), client.ImagePruneOptions{Filters: filters}); err != nil {
		t.Errorf("ImagePrune() error = %v, want nil", err)
	}
}
