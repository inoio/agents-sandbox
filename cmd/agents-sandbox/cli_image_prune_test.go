package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	sandboxmsb "github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestImagePruneNonInteractiveRequiresYes(t *testing.T) {
	m := &sandboxmsb.MockMsbClient{}
	m.Images = append(m.Images, sandboxmsb.MockImageHandle{
		Reference_:  "agents-sandbox/runner-orphan-1mjusbm3wikhb0:v1",
		LastUsedAt_: time.Now().Add(-30 * 24 * time.Hour),
	})

	cmd, _ := setupCommandFixtures(t, "image", "prune")
	sandboxmsb.WithMsbMock(t, m)

	err := cmd.Execute()
	if !errors.Is(err, termio.ErrConfirmationRequired) {
		t.Fatalf("expected ErrConfirmationRequired, got: %v", err)
	}
	if len(m.RemovedImages) != 0 {
		t.Errorf("expected no removals, got: %v", m.RemovedImages)
	}
}

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
		cmd, ui := setupCommandFixtures(t, "image", "prune", "--age", "1d", "--yes")
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
		cmd, ui := setupCommandFixtures(t, "image", "prune", "--yes")
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

func TestImagePruneFormatUnknown(t *testing.T) {
	runPruneTestError(t, []string{"image", "prune", "--format", "yaml"}, "unsupported format")
}

func TestImagePruneFormatJSON(t *testing.T) {
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
	cmd, ui := setupCommandFixtures(t, "image", "prune", "--format", "json", "--yes")
	sandboxmsb.WithMsbMock(t, m)
	docker.WithNoopDockerMock(t)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got struct {
		DryRun       bool `json:"dry_run"`
		Images       int  `json:"images"`
		DockerImages int  `json:"docker_images"`
		Details      []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"details"`
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if err := json.Unmarshal([]byte(joined), &got); err != nil {
		t.Fatalf("json.Unmarshal: %v; output: %q", err, joined)
	}
	if got.Images != 1 || got.DockerImages != 0 || len(got.Details) != 1 {
		t.Fatalf("unexpected JSON: %+v", got)
	}
	if got.Details[0].Type != "msb-image" || got.Details[0].Name == "" {
		t.Errorf("unexpected detail: %+v", got.Details[0])
	}
}

// TestImagePruneFormatJSONIncludesNativeReport pins that the native image-prune
// report's skipped-in-use and reclaimed-bytes reach the JSON.
func TestImagePruneFormatJSONIncludesNativeReport(t *testing.T) {
	physical := uint64(2048)
	m := &sandboxmsb.MockMsbClient{
		ImagePruneFn: func(context.Context) (*msbSdk.ImagePruneReport, error) {
			return &msbSdk.ImagePruneReport{SkippedInUse: 4, BytesReclaimed: &physical}, nil
		},
	}
	cmd, ui := setupCommandFixtures(t, "image", "prune", "--format", "json", "--yes")
	sandboxmsb.WithMsbMock(t, m)
	docker.WithNoopDockerMock(t)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got struct {
		SkippedInUse   uint32  `json:"skipped_in_use"`
		BytesReclaimed *uint64 `json:"bytes_reclaimed"`
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if err := json.Unmarshal([]byte(joined), &got); err != nil {
		t.Fatalf("json.Unmarshal: %v; output: %q", err, joined)
	}
	if got.SkippedInUse != 4 || got.BytesReclaimed == nil || *got.BytesReclaimed != physical {
		t.Errorf("native report fields = %+v", got)
	}
}
