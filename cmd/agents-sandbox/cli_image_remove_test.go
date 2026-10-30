package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestImageRemove(t *testing.T) {
	t.Run("removes an unreferenced image", func(t *testing.T) {
		ref := naming.RunnerImageRef("proj-1mjusbm3wikhb0", "opencode")
		cmd, _ := setupCommandFixtures(t, "image", "remove", "--yes", ref)
		m := &msb.MockMsbClient{}
		msb.WithMsbMock(t, m)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("image remove: %v", err)
		}
		if len(m.RemovedImages) != 1 || m.RemovedImages[0].Ref != ref || m.RemovedImages[0].Force {
			t.Errorf("RemovedImages = %v, want one non-forced %q", m.RemovedImages, ref)
		}
	})

	t.Run("rm alias removes", func(t *testing.T) {
		ref := naming.RunnerImageRef("proj-1mjusbm3wikhb0", "opencode")
		cmd, _ := setupCommandFixtures(t, "image", "rm", "--yes", ref)
		m := &msb.MockMsbClient{}
		msb.WithMsbMock(t, m)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("image rm: %v", err)
		}
		if len(m.RemovedImages) != 1 {
			t.Errorf("RemovedImages = %v, want one", m.RemovedImages)
		}
	})

	t.Run("refuses a referenced image naming the project", func(t *testing.T) {
		ref := naming.RunnerImageRef("proj-1mjusbm3wikhb0", "opencode")
		cmd, _ := setupCommandFixtures(t, "image", "remove", "--yes", ref)
		m := &msb.MockMsbClient{}
		msb.WithMsbMock(t, m)
		if err := seedHomeState(
			state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"},
			state.HomeState{},
		); err != nil {
			t.Fatalf("seed state: %v", err)
		}
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "proj-1mjusbm3wikhb0/opencode") {
			t.Fatalf("expected refusal naming the project, got %v", err)
		}
		if len(m.RemovedImages) != 0 {
			t.Errorf("RemovedImages = %v, want none", m.RemovedImages)
		}
	})

	t.Run("force overrides the reference", func(t *testing.T) {
		ref := naming.RunnerImageRef("proj-1mjusbm3wikhb0", "opencode")
		cmd, _ := setupCommandFixtures(t, "image", "remove", "--force", "--yes", ref)
		m := &msb.MockMsbClient{}
		msb.WithMsbMock(t, m)
		if err := seedHomeState(
			state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"},
			state.HomeState{},
		); err != nil {
			t.Fatalf("seed state: %v", err)
		}
		if err := cmd.Execute(); err != nil {
			t.Fatalf("image remove --force: %v", err)
		}
		if len(m.RemovedImages) != 1 || !m.RemovedImages[0].Force {
			t.Errorf("RemovedImages = %v, want one forced", m.RemovedImages)
		}
	})

	t.Run("fails fast without --yes in non-interactive mode", func(t *testing.T) {
		ref := naming.RunnerImageRef("proj-1mjusbm3wikhb0", "opencode")
		cmd, _ := setupCommandFixtures(t, "image", "remove", ref)
		m := &msb.MockMsbClient{}
		msb.WithMsbMock(t, m)
		err := cmd.Execute()
		if !errors.Is(err, termio.ErrConfirmationRequired) {
			t.Fatalf("expected ErrConfirmationRequired, got: %v", err)
		}
		if len(m.RemovedImages) != 0 {
			t.Errorf("RemovedImages = %v, want none", m.RemovedImages)
		}
	})

	t.Run("refuses a referenced image before prompting", func(t *testing.T) {
		ref := naming.RunnerImageRef("proj-1mjusbm3wikhb0", "opencode")
		cmd, ui := setupCommandFixtures(t, "image", "remove", ref)
		m := &msb.MockMsbClient{}
		msb.WithMsbMock(t, m)
		if err := seedHomeState(
			state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"},
			state.HomeState{},
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

	t.Run("missing ref surfaces the msb error", func(t *testing.T) {
		ref := naming.RunnerImageRef("proj-1mjusbm3wikhb0", "opencode")
		cmd, _ := setupCommandFixtures(t, "image", "remove", "--yes", ref)
		m := &msb.MockMsbClient{
			ImageRemoveFn: func(_ context.Context, _ string, _ bool) error {
				return errors.New("image not found")
			},
		}
		msb.WithMsbMock(t, m)
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "image not found") {
			t.Fatalf("expected the msb error to surface, got %v", err)
		}
	})

	t.Run("requires at least one reference", func(t *testing.T) {
		cmd, _ := setupCommandFixtures(t, "image", "remove")
		if err := cmd.Execute(); err == nil {
			t.Fatal("expected an argument error")
		}
	})
}

func TestImageRemoveOwnershipAndShorthand(t *testing.T) {
	t.Run("refuses a non-runner reference", func(t *testing.T) {
		cmd, _ := setupCommandFixtures(t, "image", "remove", "--yes", "docker.io/library/alpine:latest")
		m := &msb.MockMsbClient{}
		msb.WithMsbMock(t, m)
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "not managed by agents-sandbox") {
			t.Fatalf("expected an ownership refusal, got %v", err)
		}
		if len(m.RemovedImages) != 0 {
			t.Errorf("RemovedImages = %v, want none", m.RemovedImages)
		}
	})

	t.Run("-f shorthand forces removal", func(t *testing.T) {
		ref := naming.RunnerImageRef("proj-1mjusbm3wikhb0", "opencode")
		cmd, _ := setupCommandFixtures(t, "image", "remove", "-f", "--yes", ref)
		m := &msb.MockMsbClient{}
		msb.WithMsbMock(t, m)
		if err := seedHomeState(
			state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"},
			state.HomeState{},
		); err != nil {
			t.Fatalf("seed state: %v", err)
		}
		if err := cmd.Execute(); err != nil {
			t.Fatalf("image remove -f: %v", err)
		}
		if len(m.RemovedImages) != 1 || !m.RemovedImages[0].Force {
			t.Errorf("RemovedImages = %v, want one forced", m.RemovedImages)
		}
	})
}
