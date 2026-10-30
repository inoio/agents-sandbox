package main

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestSandboxPruneNonInteractiveRequiresYes(t *testing.T) {
	vm := &msb.MockSandboxHandle{
		Name_:      "agents-sandbox-vm-proj-1mjusbm3wikhb0",
		Status_:    msbSdk.SandboxStatusStopped,
		UpdatedAt_: time.Now().Add(-30 * 24 * time.Hour),
	}
	m := &msb.MockMsbClient{}
	m.Sandboxes = append(m.Sandboxes, vm)

	cmd, _ := setupCommandFixtures(t, "sandbox", "prune")
	msb.WithMsbMock(t, m)

	err := cmd.Execute()
	if !errors.Is(err, termio.ErrConfirmationRequired) {
		t.Fatalf("expected ErrConfirmationRequired, got: %v", err)
	}
	if vm.DidRemove() {
		t.Error("expected no removals")
	}
}

func TestSandboxPruneDryRunSkipsConfirmation(t *testing.T) {
	vm := &msb.MockSandboxHandle{
		Name_:      "agents-sandbox-vm-proj-1mjusbm3wikhb0",
		Status_:    msbSdk.SandboxStatusStopped,
		UpdatedAt_: time.Now().Add(-30 * 24 * time.Hour),
	}
	m := &msb.MockMsbClient{}
	m.Sandboxes = append(m.Sandboxes, vm)

	cmd, ui := setupCommandFixtures(t, "sandbox", "prune", "--dry-run")
	msb.WithMsbMock(t, m)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ui.ConfirmCalls) != 0 {
		t.Errorf("dry-run must not prompt, got: %v", ui.ConfirmCalls)
	}
	if vm.DidRemove() {
		t.Error("dry-run must not remove anything")
	}
	if !slices.Contains(ui.OutCalls, "dry-run: Would prune 1 sandbox(es)") {
		t.Errorf("expected dry-run preview, got: %v", ui.OutCalls)
	}
}

func TestSandboxPrune(t *testing.T) {
	t.Run("prunes stale sandbox", func(t *testing.T) {
		m := &msb.MockMsbClient{
			Sandboxes: []msb.SandboxHandle{
				&msb.MockSandboxHandle{
					Name_:      "agents-sandbox-vm-proj-1mjusbm3wikhb0",
					Status_:    msbSdk.SandboxStatusStopped,
					UpdatedAt_: time.Now().Add(-30 * 24 * time.Hour),
				},
			},
		}
		cmd, ui := setupCommandFixtures(t, "sandbox", "prune", "--yes")
		msb.WithMsbMock(t, m)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !slices.Contains(ui.OutCalls, "Pruned 1 sandbox(es)") {
			t.Errorf("expected prune summary, got: %v", ui.OutCalls)
		}
	})
}

func TestSandboxPruneFormatJSON(t *testing.T) {
	m := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-vm-proj-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: time.Now().Add(-30 * 24 * time.Hour),
			},
		},
	}
	cmd, ui := setupCommandFixtures(t, "sandbox", "prune", "--format", "json", "--yes")
	msb.WithMsbMock(t, m)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got struct {
		DryRun    bool `json:"dry_run"`
		Sandboxes int  `json:"sandboxes"`
		Details   []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"details"`
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if err := json.Unmarshal([]byte(joined), &got); err != nil {
		t.Fatalf("json.Unmarshal: %v; output: %q", err, joined)
	}
	if got.Sandboxes != 1 || len(got.Details) != 1 {
		t.Fatalf("unexpected JSON: %+v", got)
	}
	if got.Details[0].Type != "vm" || got.Details[0].Name == "" {
		t.Errorf("unexpected detail: %+v", got.Details[0])
	}
}
