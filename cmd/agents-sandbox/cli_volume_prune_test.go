package main

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestVolumePruneNonInteractiveRequiresYes(t *testing.T) {
	m := &msb.MockMsbClient{}
	m.Volumes = append(m.Volumes, &msb.MockVolumeHandle{
		Name_:      "agents-sandbox-home-orphan-1mjusbm3wikhb0-20260806T143022",
		CreatedAt_: time.Now().Add(-30 * 24 * time.Hour),
	})

	cmd, _ := setupCommandFixtures(t, "volume", "prune")
	msb.WithMsbMock(t, m)

	err := cmd.Execute()
	if !errors.Is(err, termio.ErrConfirmationRequired) {
		t.Fatalf("expected ErrConfirmationRequired, got: %v", err)
	}
	if len(m.RemovedVolumes) != 0 {
		t.Errorf("expected no removals, got: %v", m.RemovedVolumes)
	}
}

func TestVolumePrune(t *testing.T) {
	t.Run("prunes home volumes of stale slugs", func(t *testing.T) {
		m := &msb.MockMsbClient{
			Sandboxes: []msb.SandboxHandle{
				&msb.MockSandboxHandle{
					Name_:      "agents-sandbox-vm-orphan-1mjusbm3wikhb0",
					Status_:    msbSdk.SandboxStatusStopped,
					UpdatedAt_: time.Now().Add(-30 * 24 * time.Hour),
				},
			},
			Volumes: []msb.VolumeHandle{
				&msb.MockVolumeHandle{
					Name_:      "agents-sandbox-home-orphan-1mjusbm3wikhb0-20260806T143022",
					CreatedAt_: time.Now().Add(-30 * 24 * time.Hour),
				},
			},
		}
		cmd, ui := setupCommandFixtures(t, "volume", "prune", "--yes")
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
		cmd, _ := setupCommandFixtures(t, "volume", "prune", "--age", "invalid", "--yes")
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

func TestVolumePruneFormatJSON(t *testing.T) {
	m := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-vm-orphan-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: time.Now().Add(-30 * 24 * time.Hour),
			},
		},
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{
				Name_:      "agents-sandbox-home-orphan-1mjusbm3wikhb0-20260806T143022",
				CreatedAt_: time.Now().Add(-30 * 24 * time.Hour),
			},
		},
	}
	cmd, ui := setupCommandFixtures(t, "volume", "prune", "--format", "json", "--yes")
	msb.WithMsbMock(t, m)
	docker.WithNoopDockerMock(t)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got struct {
		DryRun  bool `json:"dry_run"`
		Volumes int  `json:"volumes"`
		Details []struct {
			Type            string `json:"type"`
			Name            string `json:"name"`
			Slug            string `json:"slug"`
			StaleForSeconds int64  `json:"stale_for_seconds"`
		} `json:"details"`
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if err := json.Unmarshal([]byte(joined), &got); err != nil {
		t.Fatalf("json.Unmarshal: %v; output: %q", err, joined)
	}
	if got.Volumes != 1 || len(got.Details) != 1 {
		t.Fatalf("unexpected JSON: %+v", got)
	}
	if got.Details[0].Type != "volume" || got.Details[0].Name == "" || got.Details[0].Slug == "" {
		t.Errorf("unexpected detail: %+v", got.Details[0])
	}
}
