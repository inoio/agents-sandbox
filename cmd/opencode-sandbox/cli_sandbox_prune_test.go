package main

import (
	"slices"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
)

func TestSandboxPrune(t *testing.T) {
	t.Run("prunes stale sandbox", func(t *testing.T) {
		m := &msb.MockMsbClient{
			Sandboxes: []msb.SandboxHandle{
				&msb.MockSandboxHandle{
					Name_:      "opencode-sandbox-vm-proj-1mjusbm3wikhb0",
					Status_:    msbSdk.SandboxStatusStopped,
					UpdatedAt_: time.Now().Add(-30 * 24 * time.Hour),
				},
			},
		}
		cmd, ui := setupCommandFixtures(t, "sandbox", "prune")
		msb.WithMsbMock(t, m)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !slices.Contains(ui.OutCalls, "Pruned 1 sandbox(es)") {
			t.Errorf("expected prune summary, got: %v", ui.OutCalls)
		}
	})
}
