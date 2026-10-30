package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

const removeTestVolume = "agents-sandbox-home-proj-aBc1234D-opencode-20260812T123456"

func TestVolumeRemove(t *testing.T) {
	t.Run("removes an unreferenced volume", func(t *testing.T) {
		cmd, _ := setupCommandFixtures(t, "volume", "remove", "--yes", removeTestVolume)
		m := &msb.MockMsbClient{}
		msb.WithMsbMock(t, m)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("volume remove: %v", err)
		}
		if len(m.RemovedVolumes) != 1 || m.RemovedVolumes[0] != removeTestVolume {
			t.Errorf("RemovedVolumes = %v, want [%q]", m.RemovedVolumes, removeTestVolume)
		}
	})

	t.Run("rm alias removes", func(t *testing.T) {
		cmd, _ := setupCommandFixtures(t, "volume", "rm", "--yes", removeTestVolume)
		m := &msb.MockMsbClient{}
		msb.WithMsbMock(t, m)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("volume rm: %v", err)
		}
		if len(m.RemovedVolumes) != 1 {
			t.Errorf("RemovedVolumes = %v, want one", m.RemovedVolumes)
		}
	})

	t.Run("refuses a referenced volume", func(t *testing.T) {
		cmd, _ := setupCommandFixtures(t, "volume", "remove", removeTestVolume)
		m := &msb.MockMsbClient{}
		msb.WithMsbMock(t, m)
		if err := seedHomeState(
			state.Key{Slug: "proj-aBc1234D", Agent: "opencode"},
			state.HomeState{HomeVolume: removeTestVolume},
		); err != nil {
			t.Fatalf("seed state: %v", err)
		}
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Fatalf("expected refusal, got %v", err)
		}
		if len(m.RemovedVolumes) != 0 {
			t.Errorf("RemovedVolumes = %v, want none", m.RemovedVolumes)
		}
	})

	t.Run("force overrides the reference", func(t *testing.T) {
		cmd, _ := setupCommandFixtures(t, "volume", "remove", "--force", "--yes", removeTestVolume)
		m := &msb.MockMsbClient{}
		msb.WithMsbMock(t, m)
		if err := seedHomeState(
			state.Key{Slug: "proj-aBc1234D", Agent: "opencode"},
			state.HomeState{HomeVolume: removeTestVolume},
		); err != nil {
			t.Fatalf("seed state: %v", err)
		}
		if err := cmd.Execute(); err != nil {
			t.Fatalf("volume remove --force: %v", err)
		}
		if len(m.RemovedVolumes) != 1 {
			t.Errorf("RemovedVolumes = %v, want one", m.RemovedVolumes)
		}
	})

	t.Run("fails fast without --yes in non-interactive mode", func(t *testing.T) {
		cmd, _ := setupCommandFixtures(t, "volume", "remove", removeTestVolume)
		m := &msb.MockMsbClient{}
		msb.WithMsbMock(t, m)
		err := cmd.Execute()
		if !errors.Is(err, termio.ErrConfirmationRequired) {
			t.Fatalf("expected ErrConfirmationRequired, got: %v", err)
		}
		if len(m.RemovedVolumes) != 0 {
			t.Errorf("RemovedVolumes = %v, want none", m.RemovedVolumes)
		}
	})

	t.Run("refuses a referenced volume before prompting", func(t *testing.T) {
		cmd, ui := setupCommandFixtures(t, "volume", "remove", removeTestVolume)
		m := &msb.MockMsbClient{}
		msb.WithMsbMock(t, m)
		if err := seedHomeState(
			state.Key{Slug: "proj-aBc1234D", Agent: "opencode"},
			state.HomeState{HomeVolume: removeTestVolume},
		); err != nil {
			t.Fatalf("seed state: %v", err)
		}
		ui.IsInteractiveResult = true
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Fatalf("expected refusal, got %v", err)
		}
		if len(ui.ConfirmCalls) != 0 {
			t.Errorf("guard must run before the prompt, got %v", ui.ConfirmCalls)
		}
	})

	t.Run("-f shorthand overrides the reference", func(t *testing.T) {
		cmd, _ := setupCommandFixtures(t, "volume", "remove", "-f", "--yes", removeTestVolume)
		m := &msb.MockMsbClient{}
		msb.WithMsbMock(t, m)
		if err := seedHomeState(
			state.Key{Slug: "proj-aBc1234D", Agent: "opencode"},
			state.HomeState{HomeVolume: removeTestVolume},
		); err != nil {
			t.Fatalf("seed state: %v", err)
		}
		if err := cmd.Execute(); err != nil {
			t.Fatalf("volume remove -f: %v", err)
		}
		if len(m.RemovedVolumes) != 1 {
			t.Errorf("RemovedVolumes = %v, want one", m.RemovedVolumes)
		}
	})

	t.Run("requires at least one name", func(t *testing.T) {
		cmd, _ := setupCommandFixtures(t, "volume", "remove")
		if err := cmd.Execute(); err == nil {
			t.Fatal("expected an argument error")
		}
	})
}
