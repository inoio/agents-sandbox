package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
)

func TestVolumePrune(t *testing.T) {
	t.Run("prunes home volumes of stale slugs", func(t *testing.T) {
		m := &msb.MockMsbClient{
			Sandboxes: []msb.SandboxHandle{
				&msb.MockSandboxHandle{
					Name_:      "opencode-sandbox-vm-orphan-1mjusbm3wikhb0",
					Status_:    msbSdk.SandboxStatusStopped,
					UpdatedAt_: time.Now().Add(-30 * 24 * time.Hour),
				},
			},
			Volumes: []msb.VolumeHandle{
				&msb.MockVolumeHandle{
					Name_:      "opencode-sandbox-home-orphan-1mjusbm3wikhb0-20260806T143022",
					CreatedAt_: time.Now().Add(-30 * 24 * time.Hour),
				},
			},
		}
		cmd, ui := setupCommandFixtures(t, "volume", "prune")
		msb.WithMsbMock(t, m)
		docker.WithNoopDockerMock(t)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !slices.Contains(ui.OutCalls, "Pruned 1 home volume(s)") {
			t.Errorf("expected prune summary, got: %v", ui.OutCalls)
		}
	})

	t.Run("invalid age error", func(t *testing.T) {
		cmd, _ := setupCommandFixtures(t, "volume", "prune", "--age", "invalid")
		msb.WithMsbMock(t, &msb.MockMsbClient{})
		err := cmd.Execute()
		if err == nil {
			t.Fatal("expected error for invalid age")
		}
		if !strings.Contains(err.Error(), "invalid age") {
			t.Errorf("expected invalid age error, got: %v", err)
		}
	})
}
