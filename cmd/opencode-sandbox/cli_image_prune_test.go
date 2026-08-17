package main

import (
	"slices"
	"testing"
	"time"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
	sandboxmsb "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
)

func TestImagePrune(t *testing.T) {
	t.Run("accepts and applies age flag", func(t *testing.T) {
		// A 2d-old orphan image: pruned with --age 1d, but would survive the 7d default.
		m := &sandboxmsb.MockMsbClient{
			Images: []sandboxmsb.ImageHandle{
				sandboxmsb.MockImageHandle{
					Reference_:  "opencode-sandbox/runner-orphan:v1",
					LastUsedAt_: time.Now().Add(-2 * 24 * time.Hour),
				},
			},
		}
		cmd, ui := setupCommandFixtures(t, "image", "prune", "--age", "1d")
		sandboxmsb.WithMsbMock(t, m)
		docker.WithNoopDockerMock(t)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !slices.Contains(ui.OutCalls, "image prune: 1 runner image(s), 0 dangling docker image(s)") {
			t.Errorf("expected prune summary, got: %v", ui.OutCalls)
		}
	})

	t.Run("prunes orphan images", func(t *testing.T) {
		m := &sandboxmsb.MockMsbClient{
			Images: []sandboxmsb.ImageHandle{
				sandboxmsb.MockImageHandle{
					Reference_:  "opencode-sandbox/runner-orphan:v1",
					LastUsedAt_: time.Now().Add(-30 * 24 * time.Hour),
				},
			},
		}
		cmd, ui := setupCommandFixtures(t, "image", "prune")
		sandboxmsb.WithMsbMock(t, m)
		docker.WithNoopDockerMock(t)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !slices.Contains(ui.OutCalls, "image prune: 1 runner image(s), 0 dangling docker image(s)") {
			t.Errorf("expected prune summary, got: %v", ui.OutCalls)
		}
	})
}
