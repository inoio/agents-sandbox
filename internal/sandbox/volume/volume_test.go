package volume

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/options"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/state"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
)

func TestHomeVolumeName(t *testing.T) {
	got := HomeVolumeName("myproj-aBc1234D")
	expectedPrefix := "opencode-msb-home-myproj-aBc1234D-"
	if !strings.HasPrefix(got, expectedPrefix) {
		t.Errorf("expected prefix %q, got %q", expectedPrefix, got)
	}
	suffix := strings.TrimPrefix(got, expectedPrefix)
	if len(suffix) != 15 {
		t.Errorf("expected 15-char timestamp, got %d chars: %q", len(suffix), suffix)
	}
}

func TestHomeVolumeNameDifferentInputs(t *testing.T) {
	got := HomeVolumeName("myproj-aBc1234D")
	if !strings.HasPrefix(got, "opencode-msb-home-myproj-aBc1234D-") {
		t.Errorf("unexpected name format: %q", got)
	}
}

func TestHomeVolumeNameTimestamp(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	got := HomeVolumeName("myproject")
	after := time.Now().UTC().Add(time.Second)

	if !strings.HasPrefix(got, "opencode-msb-home-myproject-") {
		t.Fatalf("expected prefix, got %q", got)
	}
	suffix := strings.TrimPrefix(got, "opencode-msb-home-myproject-")
	if len(suffix) != 15 {
		t.Fatalf("expected 15-char timestamp, got %d chars: %q", len(suffix), suffix)
	}
	ts, err := time.Parse("20060102T150405", suffix)
	if err != nil {
		t.Fatalf("timestamp %q does not parse: %v", suffix, err)
	}
	if ts.Before(before) || ts.After(after) {
		t.Errorf("timestamp %v not within expected range", ts)
	}
}

func TestNewManager(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	vm := NewManager(&testUI)
	if vm.ui == nil {
		t.Error("expected ui to be set")
	}
}

func TestPrefillVolumeRunsCopyCommand(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	ui := &testUI
	client := &msb.MockMsbClient{}
	vm := NewManager(ui)

	err := vm.PrefillVolume(
		context.Background(),
		client,
		"myproject",
		"test-home-vol",
		"opencode-msb/runner-test:latest",
		ui,
	)
	if err != nil {
		t.Fatalf("PrefillVolume failed: %v", err)
	}
	if len(client.CreatedSandboxes) != 1 {
		t.Fatalf("expected 1 created prefill sandbox, got %d", len(client.CreatedSandboxes))
	}
	if len(client.RemovedSandboxes) != 1 {
		t.Fatalf("expected 1 removed prefill sandbox, got %d", len(client.RemovedSandboxes))
	}
}

func TestResolveHomeAction_SameDigestReturnsKeep(t *testing.T) {
	ui := testutil.TermUIMock(t)
	vm := NewManager(&ui)
	action := vm.ResolveHomeAction(&ui, "same-digest", "same-digest")
	if action != actionKeep {
		t.Errorf("expected actionKeep for matching digests, got %q", action)
	}
}

func TestResolveHomeAction_DifferentDigestInNonInteractiveReturnsKeep(t *testing.T) {
	ui := testutil.TermUIMock(t)
	ui.IsInteractiveResult = false
	vm := NewManager(&ui)
	action := vm.ResolveHomeAction(&ui, "old", "new")
	if action != actionKeep {
		t.Errorf("expected actionKeep in non-interactive mode, got %q", action)
	}
}

func TestResolveHomeAction_DifferentDigestInInteractivePrompt(t *testing.T) {
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(prompt string, choices []termio.Choice, _ string) (string, error) {
			if !strings.Contains(prompt, "Docker image changed") {
				return "", fmt.Errorf("unexpected prompt: %q", prompt)
			}
			if len(choices) != 4 {
				return "", fmt.Errorf("expected 4 choices, got %d", len(choices))
			}
			return actionMigrate, nil
		},
	}
	vm := NewManager(ui)
	action := vm.ResolveHomeAction(ui, "old", "new")
	if action != actionMigrate {
		t.Errorf("expected actionMigrate, got %q", action)
	}
}

func TestResolveHomeAction_ActionQuitReturnsQuit(t *testing.T) {
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return actionQuit, nil
		},
	}
	vm := NewManager(ui)
	action := vm.ResolveHomeAction(ui, "old", "new")
	if action != actionQuit {
		t.Errorf("expected actionQuit, got %q", action)
	}
}

func TestRecordHomeImage_UpdatesDigestInState(t *testing.T) {
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	state.WriteState("myproj", state.HomeState{
		HomeVolume:  "opencode-msb-home-myproj-20260806T143022",
		ImageDigest: "sha256:old",
	})

	vm := NewManager(&termio.Mock{})
	if err := vm.RecordHomeImage("myproj", "sha256:new", &termio.Mock{}); err != nil {
		t.Fatalf("RecordHomeImage: %v", err)
	}

	st, err := state.ReadState("myproj")
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.ImageDigest != "sha256:new" {
		t.Errorf("ImageDigest = %q, want %q", st.ImageDigest, "sha256:new")
	}
	if st.HomeVolume != "opencode-msb-home-myproj-20260806T143022" {
		t.Errorf("HomeVolume changed to %q, want unchanged", st.HomeVolume)
	}
}

func TestRecordHomeImage_MissingStateIsNoop(t *testing.T) {
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	vm := NewManager(&termio.Mock{})
	if err := vm.RecordHomeImage("nosuchproj", "sha256:new", &termio.Mock{}); err != nil {
		t.Fatalf("RecordHomeImage should not error on missing state, got: %v", err)
	}
}

func TestApplyHomeAction_KeepReturnsOldVolume(t *testing.T) {
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	mock := &msb.MockMsbClient{}
	vm := NewManager(&termio.Mock{})

	var createdVols int
	mock.CreateVolumeFn = func(_ context.Context, _ string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		createdVols++
		return &msb.MockVolumeHandle{}, nil
	}

	vol, err := vm.ApplyHomeAction(
		context.Background(),
		mock,
		"myproj",
		"opencode-msb-home-myproj-old",
		"img-tag",
		"sha256:new",
		actionKeep,
		options.RunOptions{},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vol != "opencode-msb-home-myproj-old" {
		t.Errorf("volume = %q, want old volume", vol)
	}
	if len(mock.CreatedSandboxes) != 0 {
		t.Errorf("expected no sandboxes created for keep, got %v", mock.CreatedSandboxes)
	}
	if createdVols != 0 {
		t.Errorf("expected no volumes created for keep, got %d", createdVols)
	}
}

func TestApplyHomeAction_ExecutesAndKeepsOld(t *testing.T) {
	tests := []struct {
		name          string
		action        string
		wantSandboxes int
	}{
		{name: "reset", action: actionReset, wantSandboxes: 1},
		{name: "migrate", action: actionMigrate, wantSandboxes: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state.SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

			slug := "myproj"
			oldVol := "opencode-msb-home-myproj-old"
			state.WriteState(slug, state.HomeState{HomeVolume: oldVol, ImageDigest: "sha256:old"})

			mock := &msb.MockMsbClient{}
			vm := NewManager(&termio.Mock{})

			newVol, err := vm.ApplyHomeAction(
				context.Background(),
				mock,
				slug,
				oldVol,
				"img-tag",
				"sha256:new",
				tt.action,
				options.RunOptions{},
				&termio.Mock{},
			)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if newVol == oldVol {
				t.Errorf("%s should produce a new volume, got %q", tt.action, newVol)
			}
			if len(mock.CreatedSandboxes) != tt.wantSandboxes {
				t.Errorf(
					"expected %d sandboxes for %s, got %d",
					tt.wantSandboxes,
					tt.action,
					len(mock.CreatedSandboxes),
				)
			}
			if len(mock.RemovedVolumes) != 0 {
				t.Errorf("expected old volume to be kept, removed=%v", mock.RemovedVolumes)
			}

			st, err := state.ReadState(slug)
			if err != nil {
				t.Fatalf("ReadState: %v", err)
			}
			if st.HomeVolume != newVol {
				t.Errorf("state HomeVolume = %q, want %q", st.HomeVolume, newVol)
			}
			if st.ImageDigest != "sha256:new" {
				t.Errorf("state ImageDigest = %q, want %q", st.ImageDigest, "sha256:new")
			}
		})
	}
}

func TestApplyHomeAction_Reset_DryRun_NoWrites(t *testing.T) {
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	slug := "myproj"
	oldVol := "opencode-msb-home-myproj-old"
	state.WriteState(slug, state.HomeState{HomeVolume: oldVol, ImageDigest: "sha256:old"})

	mock := &msb.MockMsbClient{}
	vm := NewManager(&termio.Mock{})

	var createdVols int
	mock.CreateVolumeFn = func(_ context.Context, _ string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		createdVols++
		return &msb.MockVolumeHandle{}, nil
	}

	newVol, err := vm.ApplyHomeAction(
		context.Background(),
		mock,
		slug,
		oldVol,
		"img-tag",
		"sha256:new",
		actionReset,
		options.RunOptions{DryRun: true},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if newVol != oldVol {
		t.Errorf("dry-run should keep old volume, got %q", newVol)
	}
	if createdVols != 0 {
		t.Errorf("dry-run should not create volumes, got %d", createdVols)
	}

	st, err := state.ReadState(slug)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.HomeVolume != oldVol {
		t.Errorf("dry-run should not change state HomeVolume, got %q", st.HomeVolume)
	}
	if st.ImageDigest != "sha256:old" {
		t.Errorf("dry-run should not change state ImageDigest, got %q", st.ImageDigest)
	}
}

func TestApplyHomeAction_Migrate_DryRunVM_NoStateWrite(t *testing.T) {
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	slug := "myproj"
	oldVol := "opencode-msb-home-myproj-old"
	state.WriteState(slug, state.HomeState{HomeVolume: oldVol, ImageDigest: "sha256:old"})

	mock := &msb.MockMsbClient{}
	vm := NewManager(&termio.Mock{})

	newVol, err := vm.ApplyHomeAction(
		context.Background(),
		mock,
		slug,
		oldVol,
		"img-tag",
		"sha256:new",
		actionMigrate,
		options.RunOptions{DryRunVM: true},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if newVol != oldVol {
		t.Errorf("dry-run-vm should keep old volume, got %q", newVol)
	}
	if len(mock.CreatedSandboxes) != 0 {
		t.Errorf("dry-run-vm should not spawn VMs, got %v", mock.CreatedSandboxes)
	}

	st, err := state.ReadState(slug)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.HomeVolume != oldVol {
		t.Errorf("dry-run-vm should not write state HomeVolume, got %q", st.HomeVolume)
	}
	if st.ImageDigest != "sha256:old" {
		t.Errorf("dry-run-vm should not write state ImageDigest, got %q", st.ImageDigest)
	}
}

func TestApplyHomeAction_Migrate_CopyFails_RemovesNewVolume(t *testing.T) {
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	slug := "myproj"
	oldVol := "opencode-msb-home-myproj-old"
	state.WriteState(slug, state.HomeState{HomeVolume: oldVol, ImageDigest: "sha256:old"})

	mock := &msb.MockMsbClient{}
	var createdVol string
	sandboxCount := 0
	mock.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		sandboxCount++
		if sandboxCount == 2 {
			return nil, errors.New("create copy sandbox failed")
		}
		return msb.NewMockSandbox(msb.SandboxOpts{}), nil
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		createdVol = name
		return &msb.MockVolumeHandle{Name_: name}, nil
	}
	vm := NewManager(&termio.Mock{})

	_, err := vm.ApplyHomeAction(
		context.Background(),
		mock,
		slug,
		oldVol,
		"img-tag",
		"sha256:new",
		actionMigrate,
		options.RunOptions{},
		&termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "create copy sandbox") {
		t.Errorf("unexpected error: %v", err)
	}
	var cleanedUp bool
	for _, v := range mock.RemovedVolumes {
		if v == createdVol {
			cleanedUp = true
		}
	}
	if !cleanedUp {
		t.Errorf("expected new volume %q to be removed on copy failure; removed=%v", createdVol, mock.RemovedVolumes)
	}
}

func TestActionConstantsHaveCorrectKeys(t *testing.T) {
	if actionKeep != "1" {
		t.Errorf("actionKeep = %q, want %q", actionKeep, "1")
	}
	if actionMigrate != "2" {
		t.Errorf("actionMigrate = %q, want %q", actionMigrate, "2")
	}
	if actionReset != "3" {
		t.Errorf("actionReset = %q, want %q", actionReset, "3")
	}
	if actionQuit != "4" {
		t.Errorf("actionQuit = %q, want %q", actionQuit, "4")
	}
}

func TestResolveHomeVolume_FoundInState(t *testing.T) {
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	state.WriteState("myproj", state.HomeState{
		HomeVolume:  "opencode-msb-home-myproj-20260806T143022",
		ImageDigest: "sha256:abc",
	})

	vm := NewManager(&termio.Mock{})
	volName, st, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		"myproj",
		"sha256:abc",
		"",
		options.RunOptions{},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if volName != "opencode-msb-home-myproj-20260806T143022" {
		t.Errorf("volume = %q, want %q", volName, "opencode-msb-home-myproj-20260806T143022")
	}
	if st.ImageDigest != "sha256:abc" {
		t.Errorf("digest = %q, want %q", st.ImageDigest, "sha256:abc")
	}
}

func TestResolveHomeVolume_NoStateFile(t *testing.T) {
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	mock := &msb.MockMsbClient{}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	vm := NewManager(&termio.Mock{})
	volName, st, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		"testproj",
		"sha256:def",
		"",
		options.RunOptions{},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(volName, "opencode-msb-home-testproj-") {
		t.Errorf("volume = %q, expected prefix %q", volName, "opencode-msb-home-testproj-")
	}
	if st.ImageDigest != "sha256:def" {
		t.Errorf("digest = %q, want %q", st.ImageDigest, "sha256:def")
	}
}

func TestResolveHomeVolume_VolumeNotFoundInSandbox(t *testing.T) {
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		var vh msb.VolumeHandle
		return vh, fmt.Errorf("volume %s not found", name)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	state.WriteState("orphanproj", state.HomeState{
		HomeVolume:  "opencode-msb-home-orphanproj-20260806T143022",
		ImageDigest: "sha256:abc",
	})

	vm := NewManager(&termio.Mock{})
	volName, st, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		"orphanproj",
		"sha256:def",
		"latest-docker-image",
		options.RunOptions{},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(volName, "opencode-msb-home-orphanproj-") {
		t.Errorf("volume = %q, expected prefix %q", volName, "opencode-msb-home-orphanproj-")
	}
	if st.ImageDigest != "sha256:def" {
		t.Errorf("digest = %q, want %q", st.ImageDigest, "sha256:def")
	}
}
