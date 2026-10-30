package main

import (
	"errors"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestSandboxRemove(t *testing.T) {
	t.Run("removes a stopped VM", func(t *testing.T) {
		handle := &msb.MockSandboxHandle{
			Name_:   naming.LegacyVMPrefix + "proj-aBc1234D",
			Status_: msbSdk.SandboxStatusStopped,
		}
		m := &msb.MockMsbClient{}
		m.SetGotSandbox(handle)
		cmd, _ := setupCommandFixtures(t, "sandbox", "remove", "--yes", handle.Name_)
		msb.WithMsbMock(t, m)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("sandbox remove: %v", err)
		}
		if !handle.DidRmv {
			t.Error("stopped sandbox must be removed")
		}
	})

	t.Run("rm alias removes", func(t *testing.T) {
		handle := &msb.MockSandboxHandle{
			Name_:   naming.LegacyVMPrefix + "proj-aBc1234D",
			Status_: msbSdk.SandboxStatusStopped,
		}
		m := &msb.MockMsbClient{}
		m.SetGotSandbox(handle)
		cmd, _ := setupCommandFixtures(t, "sandbox", "rm", "--yes", handle.Name_)
		msb.WithMsbMock(t, m)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("sandbox rm: %v", err)
		}
		if !handle.DidRmv {
			t.Error("rm alias must remove the sandbox")
		}
	})

	t.Run("refuses a running VM without --force", func(t *testing.T) {
		handle := &msb.MockSandboxHandle{
			Name_:   naming.LegacyVMPrefix + "proj-aBc1234D",
			Status_: msbSdk.SandboxStatusRunning,
		}
		m := &msb.MockMsbClient{}
		m.SetGotSandbox(handle)
		cmd, _ := setupCommandFixtures(t, "sandbox", "remove", "--yes", handle.Name_)
		msb.WithMsbMock(t, m)
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "not stopped") {
			t.Fatalf("expected a not-stopped refusal, got %v", err)
		}
		if handle.DidRmv {
			t.Error("running sandbox must not be removed without --force")
		}
	})

	t.Run("force on a running VM stops then removes", func(t *testing.T) {
		handle := &msb.MockSandboxHandle{
			Name_:   naming.LegacyVMPrefix + "proj-aBc1234D",
			Status_: msbSdk.SandboxStatusRunning,
		}
		m := &msb.MockMsbClient{}
		m.SetGotSandbox(handle)
		cmd, _ := setupCommandFixtures(t, "sandbox", "remove", "-f", "--yes", handle.Name_)
		msb.WithMsbMock(t, m)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("sandbox remove -f: %v", err)
		}
		if !handle.DidStop {
			t.Error("force on a running VM must stop it first")
		}
		if !handle.DidRmv {
			t.Error("force on a running VM must remove it")
		}
	})

	t.Run("force with a declined confirmation does not stop", func(t *testing.T) {
		handle := &msb.MockSandboxHandle{
			Name_:   naming.LegacyVMPrefix + "proj-aBc1234D",
			Status_: msbSdk.SandboxStatusRunning,
		}
		m := &msb.MockMsbClient{}
		m.SetGotSandbox(handle)
		cmd, ui := setupCommandFixtures(t, "sandbox", "remove", "--force", handle.Name_)
		msb.WithMsbMock(t, m)
		ui.IsInteractiveResult = true
		ui.ConfirmResult = false
		err := cmd.Execute()
		if !errors.Is(err, termio.ErrCancelled) {
			t.Fatalf("expected ErrCancelled, got %v", err)
		}
		if handle.DidStop {
			t.Error("a declined confirmation must not stop the sandbox")
		}
		if handle.DidRmv {
			t.Error("a declined confirmation must not remove the sandbox")
		}
	})

	t.Run("force keeps persisted state", func(t *testing.T) {
		key := state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"}
		name := naming.LegacyVMPrefix + key.Slug + "-" + key.Agent
		handle := &msb.MockSandboxHandle{Name_: name, Status_: msbSdk.SandboxStatusStopped}
		m := &msb.MockMsbClient{}
		m.SetGotSandbox(handle)
		cmd, _ := setupCommandFixtures(t, "sandbox", "remove", "--force", "--yes", name)
		msb.WithMsbMock(t, m)
		if err := seedHomeState(key, state.HomeState{}); err != nil {
			t.Fatalf("seed state: %v", err)
		}
		if err := cmd.Execute(); err != nil {
			t.Fatalf("sandbox remove --force: %v", err)
		}
		if _, err := state.ReadState(key); err != nil {
			t.Fatalf("state must survive a forced removal, got %v", err)
		}
	})

	t.Run("refuses a task-sandbox name", func(t *testing.T) {
		handle := &msb.MockSandboxHandle{
			Name_:   naming.LegacyTaskPrefix + "x-1729",
			Status_: msbSdk.SandboxStatusStopped,
		}
		m := &msb.MockMsbClient{}
		m.SetGotSandbox(handle)
		cmd, _ := setupCommandFixtures(t, "sandbox", "remove", "--yes", handle.Name_)
		msb.WithMsbMock(t, m)
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "not an agents-sandbox project VM") {
			t.Fatalf("expected a refusal, got %v", err)
		}
		if handle.DidRmv {
			t.Error("a task sandbox must not be removed")
		}
	})

	t.Run("fails fast without --yes in non-interactive mode", func(t *testing.T) {
		handle := &msb.MockSandboxHandle{
			Name_:   naming.LegacyVMPrefix + "proj-aBc1234D",
			Status_: msbSdk.SandboxStatusStopped,
		}
		m := &msb.MockMsbClient{}
		m.SetGotSandbox(handle)
		cmd, _ := setupCommandFixtures(t, "sandbox", "remove", handle.Name_)
		msb.WithMsbMock(t, m)
		err := cmd.Execute()
		if !errors.Is(err, termio.ErrConfirmationRequired) {
			t.Fatalf("expected ErrConfirmationRequired, got: %v", err)
		}
		if handle.DidRmv {
			t.Error("sandbox must not be removed without confirmation")
		}
	})

	t.Run("running refusal precedes the prompt", func(t *testing.T) {
		handle := &msb.MockSandboxHandle{
			Name_:   naming.LegacyVMPrefix + "proj-aBc1234D",
			Status_: msbSdk.SandboxStatusRunning,
		}
		m := &msb.MockMsbClient{}
		m.SetGotSandbox(handle)
		cmd, ui := setupCommandFixtures(t, "sandbox", "remove", handle.Name_)
		msb.WithMsbMock(t, m)
		ui.IsInteractiveResult = true
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "not stopped") {
			t.Fatalf("expected a not-stopped refusal, got %v", err)
		}
		if len(ui.ConfirmCalls) != 0 {
			t.Errorf("guard must run before the prompt, got %v", ui.ConfirmCalls)
		}
	})
}
