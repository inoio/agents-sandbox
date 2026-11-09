package main

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestSandboxRemove(t *testing.T) {
	t.Run("removes a stopped VM", func(t *testing.T) {
		handle := &msbtest.MockSandboxHandle{
			Name_:   naming.LegacyVMPrefix + "proj-aBc1234D",
			Status_: msb.SandboxStatusStopped,
		}
		m := &msbtest.MockMsbClient{}
		m.SetGotSandbox(handle)
		cmd, _ := setupCommandFixtures(t, "sandbox", "remove", "--yes", handle.Name_)
		msbtest.WithMsbMock(t, m)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("sandbox remove: %v", err)
		}
		if !handle.DidRmv {
			t.Error("stopped sandbox must be removed")
		}
	})

	t.Run("rm alias removes", func(t *testing.T) {
		handle := &msbtest.MockSandboxHandle{
			Name_:   naming.LegacyVMPrefix + "proj-aBc1234D",
			Status_: msb.SandboxStatusStopped,
		}
		m := &msbtest.MockMsbClient{}
		m.SetGotSandbox(handle)
		cmd, _ := setupCommandFixtures(t, "sandbox", "rm", "--yes", handle.Name_)
		msbtest.WithMsbMock(t, m)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("sandbox rm: %v", err)
		}
		if !handle.DidRmv {
			t.Error("rm alias must remove the sandbox")
		}
	})

	t.Run("refuses a running VM without --force", func(t *testing.T) {
		handle := &msbtest.MockSandboxHandle{
			Name_:   naming.LegacyVMPrefix + "proj-aBc1234D",
			Status_: msb.SandboxStatusRunning,
		}
		m := &msbtest.MockMsbClient{}
		m.SetGotSandbox(handle)
		cmd, _ := setupCommandFixtures(t, "sandbox", "remove", "--yes", handle.Name_)
		msbtest.WithMsbMock(t, m)
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "not stopped") {
			t.Fatalf("expected a not-stopped refusal, got %v", err)
		}
		if handle.DidRmv {
			t.Error("running sandbox must not be removed without --force")
		}
	})

	t.Run("force on a running VM stops then removes", func(t *testing.T) {
		handle := &msbtest.MockSandboxHandle{
			Name_:   naming.LegacyVMPrefix + "proj-aBc1234D",
			Status_: msb.SandboxStatusRunning,
		}
		m := &msbtest.MockMsbClient{}
		m.SetGotSandbox(handle)
		cmd, _ := setupCommandFixtures(t, "sandbox", "remove", "-f", "--yes", handle.Name_)
		msbtest.WithMsbMock(t, m)
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
		handle := &msbtest.MockSandboxHandle{
			Name_:   naming.LegacyVMPrefix + "proj-aBc1234D",
			Status_: msb.SandboxStatusRunning,
		}
		m := &msbtest.MockMsbClient{}
		m.SetGotSandbox(handle)
		cmd, ui := setupCommandFixtures(t, "sandbox", "remove", "--force", handle.Name_)
		msbtest.WithMsbMock(t, m)
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
		handle := &msbtest.MockSandboxHandle{Name_: name, Status_: msb.SandboxStatusStopped}
		m := &msbtest.MockMsbClient{}
		m.SetGotSandbox(handle)
		cmd, _ := setupCommandFixtures(t, "sandbox", "remove", "--force", "--yes", name)
		msbtest.WithMsbMock(t, m)
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
		handle := &msbtest.MockSandboxHandle{
			Name_:   naming.LegacyTaskPrefix + "x-1729",
			Status_: msb.SandboxStatusStopped,
		}
		m := &msbtest.MockMsbClient{}
		m.SetGotSandbox(handle)
		cmd, _ := setupCommandFixtures(t, "sandbox", "remove", "--yes", handle.Name_)
		msbtest.WithMsbMock(t, m)
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "not an agents-sandbox project VM") {
			t.Fatalf("expected a refusal, got %v", err)
		}
		if handle.DidRmv {
			t.Error("a task sandbox must not be removed")
		}
	})

	t.Run("fails fast without --yes in non-interactive mode", func(t *testing.T) {
		handle := &msbtest.MockSandboxHandle{
			Name_:   naming.LegacyVMPrefix + "proj-aBc1234D",
			Status_: msb.SandboxStatusStopped,
		}
		m := &msbtest.MockMsbClient{}
		m.SetGotSandbox(handle)
		cmd, _ := setupCommandFixtures(t, "sandbox", "remove", handle.Name_)
		msbtest.WithMsbMock(t, m)
		err := cmd.Execute()
		if !errors.Is(err, termio.ErrConfirmationRequired) {
			t.Fatalf("expected ErrConfirmationRequired, got: %v", err)
		}
		if handle.DidRmv {
			t.Error("sandbox must not be removed without confirmation")
		}
	})

	t.Run("running refusal precedes the prompt", func(t *testing.T) {
		handle := &msbtest.MockSandboxHandle{
			Name_:   naming.LegacyVMPrefix + "proj-aBc1234D",
			Status_: msb.SandboxStatusRunning,
		}
		m := &msbtest.MockMsbClient{}
		m.SetGotSandbox(handle)
		cmd, ui := setupCommandFixtures(t, "sandbox", "remove", handle.Name_)
		msbtest.WithMsbMock(t, m)
		ui.IsInteractiveResult = true
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "not stopped") {
			t.Fatalf("expected a not-stopped refusal, got %v", err)
		}
		if len(ui.ConfirmCalls) != 0 {
			t.Errorf("guard must run before the prompt, got %v", ui.ConfirmCalls)
		}
	})
}

// TestSandboxRemoveWithoutPurgeLeavesStateAndVolume pins the unchanged default:
// plain `sandbox remove` removes the VM only, leaving the recorded home volume
// and the project's state so the next run reuses it.
func TestSandboxRemoveWithoutPurgeLeavesStateAndVolume(t *testing.T) {
	key := state.Key{Slug: purgeTestSlug, Agent: purgeTestAgent}
	volumeName := homeVolumeNameFor(purgeTestSlug)
	handle := &msbtest.MockSandboxHandle{
		Name_:   naming.LegacyVMPrefix + key.Slug + "-" + key.Agent,
		Status_: msb.SandboxStatusStopped,
	}
	m := &msbtest.MockMsbClient{}
	m.SetGotSandbox(handle)
	m.Volumes = append(m.Volumes, homeVol(volumeName))

	cmd, _ := setupCommandFixtures(t, "sandbox", "remove", "--yes", handle.Name_)
	msbtest.WithMsbMock(t, m)
	if err := seedHomeState(key, state.HomeState{HomeVolume: volumeName}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox remove: %v", err)
	}
	if !handle.DidRmv {
		t.Error("the VM must be removed")
	}
	if len(m.RemovedVolumes) != 0 {
		t.Errorf("plain sandbox remove must not remove a volume, got: %v", m.RemovedVolumes)
	}
	st, err := state.ReadState(key)
	if err != nil || st.HomeVolume != volumeName {
		t.Errorf("state after removal = %+v, %v; want the recorded home volume kept", st, err)
	}
}

// TestSandboxRemovePurgeAcceptsSlugAndSlugAgent pins the two purge target forms:
// a bare project slug removes every artifact of the whole project, while a
// <slug>-<agent> key removes only that agent's artifacts and state.
func TestSandboxRemovePurgeAcceptsSlugAndSlugAgent(t *testing.T) {
	t.Run("slug purges every agent", func(t *testing.T) {
		volumeName := homeVolumeNameFor(purgeTestSlug)
		imageRef := purgeTestSlug + ":" + purgeTestAgent + "-latest"
		vm := mkStoppedVM(purgeTestSlug)
		m := &msbtest.MockMsbClient{}
		m.Sandboxes = append(m.Sandboxes, vm)
		m.Volumes = append(m.Volumes, homeVol(volumeName))
		m.Images = append(m.Images, msbImg(imageRef))

		cmd, _ := setupCommandFixtures(t, "sandbox", "remove", "--purge", purgeTestSlug, "--yes")
		msbtest.WithMsbMock(t, m)
		if err := seedHomeState(
			state.Key{Slug: purgeTestSlug, Agent: purgeTestAgent},
			state.HomeState{HomeVolume: volumeName},
		); err != nil {
			t.Fatalf("seed state: %v", err)
		}

		if err := cmd.Execute(); err != nil {
			t.Fatalf("sandbox remove --purge: %v", err)
		}
		if !slices.Contains(m.RemovedVolumes, volumeName) {
			t.Errorf("expected volume %q removed, got: %v", volumeName, m.RemovedVolumes)
		}
		if len(m.RemovedImages) != 1 || m.RemovedImages[0].Ref != imageRef {
			t.Errorf("expected image %q removed, got: %+v", imageRef, m.RemovedImages)
		}
		if !vm.(*msbtest.MockSandboxHandle).DidRemove() {
			t.Error("expected the sandbox removed")
		}
		key := state.Key{Slug: purgeTestSlug, Agent: purgeTestAgent}
		if _, err := state.StateFileModTime(key); !errors.Is(err, state.ErrStateNotFound) {
			t.Errorf("expected the state file removed, got: %v", err)
		}
	})

	t.Run("slug-agent purges one key only", func(t *testing.T) {
		const otherAgent = "codex"
		opencodeVolume := homeVolumeNameFor(purgeTestSlug)
		codexVolume := naming.HomeVolumeName(purgeTestSlug, otherAgent, "20260806T143022")
		m := &msbtest.MockMsbClient{}
		m.Volumes = append(m.Volumes, homeVol(opencodeVolume), homeVol(codexVolume))

		cmd, _ := setupCommandFixtures(
			t, "sandbox", "remove", "--purge", purgeTestSlug+"-"+purgeTestAgent, "--yes",
		)
		msbtest.WithMsbMock(t, m)
		for key, volumeName := range map[state.Key]string{
			{Slug: purgeTestSlug, Agent: purgeTestAgent}: opencodeVolume,
			{Slug: purgeTestSlug, Agent: otherAgent}:     codexVolume,
		} {
			if err := seedHomeState(key, state.HomeState{HomeVolume: volumeName}); err != nil {
				t.Fatalf("seed state %+v: %v", key, err)
			}
		}

		if err := cmd.Execute(); err != nil {
			t.Fatalf("sandbox remove --purge <slug>-<agent>: %v", err)
		}
		if !slices.Contains(m.RemovedVolumes, opencodeVolume) {
			t.Errorf("expected volume %q removed, got: %v", opencodeVolume, m.RemovedVolumes)
		}
		if slices.Contains(m.RemovedVolumes, codexVolume) {
			t.Errorf("a single-key purge must not remove another agent's volume %q", codexVolume)
		}
		otherKey := state.Key{Slug: purgeTestSlug, Agent: otherAgent}
		if _, err := state.StateFileModTime(otherKey); err != nil {
			t.Errorf("another agent's state must survive, got: %v", err)
		}
	})
}

// TestSandboxRemovePurgeQuietNamesVolumes proves the shared purge engine names
// the home volume it removes even under --quiet on the `sandbox remove --purge`
// surface, which shares runProjectPurge with `prune --purge`.
func TestSandboxRemovePurgeQuietNamesVolumes(t *testing.T) {
	volumeName := homeVolumeNameFor(purgeTestSlug)

	cmd, ui := setupCommandFixtures(t, "sandbox", "remove", "--purge", purgeTestSlug, "--quiet")
	ui.IsInteractiveResult = true
	ui.ConfirmResult = true

	if err := seedHomeState(
		state.Key{Slug: purgeTestSlug, Agent: purgeTestAgent},
		state.HomeState{HomeVolume: volumeName},
	); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	m := &msbtest.MockMsbClient{}
	m.Volumes = append(m.Volumes, homeVol(volumeName))
	msbtest.WithMsbMock(t, m)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox remove --purge --quiet: %v", err)
	}
	if !slices.Contains(ui.PrintCalls, "    - "+volumeName) {
		t.Errorf("quiet purge must name the volume via Print; PrintCalls=%v", ui.PrintCalls)
	}
	if len(ui.OutCalls) != 0 {
		t.Errorf("quiet purge must not write table output, got OutCalls=%v", ui.OutCalls)
	}
}

// TestSandboxRemovePurgeImplicitCwdTarget pins that `sandbox remove --purge`
// with no target resolves the current checkout's slug, purging its artifacts
// while leaving an unrelated project alone.
func TestSandboxRemovePurgeImplicitCwdTarget(t *testing.T) {
	clone := newOriginClone(t)
	identity := mustIdentity(t, clone)
	t.Chdir(clone)

	volumeName := homeVolumeNameFor(identity.CheckoutSlug)
	otherVolumeName := homeVolumeNameFor("otherproject-1mjusbm3wikhb0")
	vm := mkStoppedVM(identity.CheckoutSlug)
	m := &msbtest.MockMsbClient{}
	m.Sandboxes = append(m.Sandboxes, vm)
	m.Volumes = append(m.Volumes, homeVol(volumeName), homeVol(otherVolumeName))

	cmd, _ := setupCommandFixtures(t, "sandbox", "remove", "--purge", "--yes")
	msbtest.WithMsbMock(t, m)
	if err := seedHomeState(
		state.Key{Slug: identity.CheckoutSlug, Agent: purgeTestAgent},
		state.HomeState{HomeVolume: volumeName},
	); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox remove --purge (implicit): %v", err)
	}
	if !slices.Contains(m.RemovedVolumes, volumeName) {
		t.Errorf("expected the checkout slug's volume %q removed, got: %v", volumeName, m.RemovedVolumes)
	}
	if slices.Contains(m.RemovedVolumes, otherVolumeName) {
		t.Errorf("an unrelated project's volume %q must survive", otherVolumeName)
	}
	if !vm.(*msbtest.MockSandboxHandle).DidRemove() {
		t.Error("expected the checkout slug's sandbox removed")
	}
}

// TestSandboxRemovePurgeRefusesRunningWithoutForce pins that the purge path
// shares the running-sandbox guard: it fails fast naming --force and prompts
// nothing.
func TestSandboxRemovePurgeRefusesRunningWithoutForce(t *testing.T) {
	running := mkActiveVM(purgeTestSlug)
	m := &msbtest.MockMsbClient{}
	m.Sandboxes = append(m.Sandboxes, running)

	cmd, ui := setupCommandFixtures(t, "sandbox", "remove", "--purge", purgeTestSlug, "--yes")
	msbtest.WithMsbMock(t, m)

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected a --force hint error, got: %v", err)
	}
	if running.(*msbtest.MockSandboxHandle).DidRemove() {
		t.Error("a refused purge must not remove the running sandbox")
	}
	if len(ui.ConfirmCalls) != 0 {
		t.Errorf("the running guard must fail before prompting, got: %v", ui.ConfirmCalls)
	}
}

// TestSandboxRemoveArgValidation pins the argument rules: a plain remove needs
// exactly one name, while --purge accepts at most one target (defaulting to the
// cwd). An extra argument is rejected before any runtime access.
func TestSandboxRemoveArgValidation(t *testing.T) {
	t.Run("plain remove requires a name", func(t *testing.T) {
		cmd, _ := setupCommandFixtures(t, "sandbox", "remove", "--yes")
		msbtest.WithMsbMock(t, &msbtest.MockMsbClient{})
		if err := cmd.Execute(); err == nil {
			t.Fatal("plain sandbox remove without a name should fail")
		}
	})
	t.Run("plain remove rejects a second name", func(t *testing.T) {
		cmd, _ := setupCommandFixtures(t, "sandbox", "remove", "--yes", "one", "two")
		msbtest.WithMsbMock(t, &msbtest.MockMsbClient{})
		if err := cmd.Execute(); err == nil {
			t.Fatal("plain sandbox remove with two names should fail")
		}
	})
	t.Run("purge rejects a second target", func(t *testing.T) {
		cmd, _ := setupCommandFixtures(t, "sandbox", "remove", "--purge", "--yes", "one", "two")
		msbtest.WithMsbMock(t, &msbtest.MockMsbClient{})
		if err := cmd.Execute(); err == nil {
			t.Fatal("sandbox remove --purge with two targets should fail")
		}
	})
}
