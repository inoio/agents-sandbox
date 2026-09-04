package main

import (
	"slices"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	sandboxmsb "github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

func TestImagePrune(t *testing.T) {
	t.Run("accepts and applies age flag", func(t *testing.T) {
		// A 2d-old stale VM makes its image prunable with --age 1d, but not the 7d default.
		m := &sandboxmsb.MockMsbClient{
			Sandboxes: []sandboxmsb.SandboxHandle{
				&sandboxmsb.MockSandboxHandle{
					Name_:      "agents-sandbox-vm-orphan-1mjusbm3wikhb0",
					Status_:    msbSdk.SandboxStatusStopped,
					UpdatedAt_: time.Now().Add(-2 * 24 * time.Hour),
				},
			},
			Images: []sandboxmsb.ImageHandle{
				sandboxmsb.MockImageHandle{
					Reference_:  "agents-sandbox/runner-orphan-1mjusbm3wikhb0:v1",
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
		if !slices.Contains(ui.OutCalls, "Pruned 1 runner image(s), 0 dangling docker image(s)") {
			t.Errorf("expected prune summary, got: %v", ui.OutCalls)
		}
	})

	t.Run("prunes images of stale slugs", func(t *testing.T) {
		m := &sandboxmsb.MockMsbClient{
			Sandboxes: []sandboxmsb.SandboxHandle{
				&sandboxmsb.MockSandboxHandle{
					Name_:      "agents-sandbox-vm-orphan-1mjusbm3wikhb0",
					Status_:    msbSdk.SandboxStatusStopped,
					UpdatedAt_: time.Now().Add(-30 * 24 * time.Hour),
				},
			},
			Images: []sandboxmsb.ImageHandle{
				sandboxmsb.MockImageHandle{
					Reference_:  "agents-sandbox/runner-orphan-1mjusbm3wikhb0:v1",
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
		if !slices.Contains(ui.OutCalls, "Pruned 1 runner image(s), 0 dangling docker image(s)") {
			t.Errorf("expected prune summary, got: %v", ui.OutCalls)
		}
	})
}
