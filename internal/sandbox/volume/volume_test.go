package volume

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/opencode-sandbox/internal/configpaths"

	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

func TestHomeVolumeName(t *testing.T) {
	got := HomeVolumeName("myproj-aBc1234D")
	expectedPrefix := "opencode-sandbox-home-myproj-aBc1234D-"
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
	if !strings.HasPrefix(got, "opencode-sandbox-home-myproj-aBc1234D-") {
		t.Errorf("unexpected name format: %q", got)
	}
}

func TestHomeVolumeNameTimestamp(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	got := HomeVolumeName("myproject")
	after := time.Now().UTC().Add(time.Second)

	if !strings.HasPrefix(got, "opencode-sandbox-home-myproject-") {
		t.Fatalf("expected prefix, got %q", got)
	}
	suffix := strings.TrimPrefix(got, "opencode-sandbox-home-myproject-")
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
	testUI := termio.NewTestMock(t)
	vm := NewManager(&testUI)
	if vm.ui == nil {
		t.Error("expected ui to be set")
	}
}

func TestPrefillVolumeRunsCopyCommand(t *testing.T) {
	docker.WithNoopDockerMock(t)
	testUI := termio.NewTestMock(t)
	ui := &testUI
	client := &msb.MockMsbClient{}
	vm := NewManager(ui)

	err := vm.PrefillVolume(
		context.Background(),
		client,
		state.Key{Slug: "myproject", Agent: "opencode"},
		"test-home-vol",
		"opencode-sandbox/runner-test:latest",
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
	ui := termio.NewTestMock(t)
	vm := NewManager(&ui)
	action := vm.ResolveHomeAction(&ui, "same-digest", "same-digest")
	if action != ActionKeep {
		t.Errorf("expected ActionKeep for matching digests, got %v", action)
	}
}

func TestResolveHomeAction_DifferentDigestInNonInteractiveReturnsKeep(t *testing.T) {
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = false
	vm := NewManager(&ui)
	action := vm.ResolveHomeAction(&ui, "old", "new")
	if action != ActionKeep {
		t.Errorf("expected ActionKeep in non-interactive mode, got %v", action)
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
			return "m", nil
		},
	}
	vm := NewManager(ui)
	action := vm.ResolveHomeAction(ui, "old", "new")
	if action != ActionMigrate {
		t.Errorf("expected ActionMigrate, got %v", action)
	}
}

func TestResolveHomeAction_NonInteractiveExplains(t *testing.T) {
	vm := NewManager(&termio.Mock{})
	ui := &termio.Mock{IsInteractiveResult: false}
	action := vm.ResolveHomeAction(ui, "old", "new")
	if action != ActionKeep {
		t.Fatalf("expected ActionKeep, got %v", action)
	}
	if len(ui.InfoCalls) == 0 || !strings.Contains(ui.InfoCalls[0], "image changed") ||
		!strings.Contains(ui.InfoCalls[0], "keeping") {
		t.Errorf("expected an explanatory keep message, got %v", ui.InfoCalls)
	}
}

func TestResolveHomeAction_ActionQuitReturnsQuit(t *testing.T) {
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "q", nil
		},
	}
	vm := NewManager(ui)
	action := vm.ResolveHomeAction(ui, "old", "new")
	if action != ActionQuit {
		t.Errorf("expected ActionQuit, got %v", action)
	}
}

func TestRecordHomeImage_UpdatesDigestInState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	state.WriteState(state.Key{Slug: "myproj", Agent: "opencode"}, state.HomeState{
		HomeVolume:  "opencode-sandbox-home-myproj-20260806T143022",
		ImageDigest: "sha256:old",
	})

	vm := NewManager(&termio.Mock{})
	if err := vm.RecordHomeImage(
		state.Key{Slug: "myproj", Agent: "opencode"},
		"sha256:new",
		&termio.Mock{},
	); err != nil {
		t.Fatalf("RecordHomeImage: %v", err)
	}

	st, err := state.ReadState(state.Key{Slug: "myproj", Agent: "opencode"})
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.ImageDigest != "sha256:new" {
		t.Errorf("ImageDigest = %q, want %q", st.ImageDigest, "sha256:new")
	}
	if st.HomeVolume != "opencode-sandbox-home-myproj-20260806T143022" {
		t.Errorf("HomeVolume changed to %q, want unchanged", st.HomeVolume)
	}
}

func TestRecordHomeImage_MissingStateIsNoop(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	vm := NewManager(&termio.Mock{})
	if err := vm.RecordHomeImage(
		state.Key{Slug: "nosuchproj", Agent: "opencode"},
		"sha256:new",
		&termio.Mock{},
	); err != nil {
		t.Fatalf("RecordHomeImage should not error on missing state, got: %v", err)
	}
}

func TestApplyHomeAction_KeepReturnsOldVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

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
		state.Key{Slug: "myproj", Agent: "opencode"},
		"opencode-sandbox-home-myproj-old",
		"img-tag",
		"sha256:new",
		ActionKeep,
		options.RunOptions{},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vol != "opencode-sandbox-home-myproj-old" {
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
		action        VolumeAction
		wantSandboxes int
	}{
		{name: "reset", action: ActionReset, wantSandboxes: 1},
		{name: "migrate", action: ActionMigrate, wantSandboxes: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			docker.WithNoopDockerMock(t)

			slug := "myproj"
			oldVol := "opencode-sandbox-home-myproj-old"
			state.WriteState(
				state.Key{Slug: slug, Agent: "opencode"},
				state.HomeState{HomeVolume: oldVol, ImageDigest: "sha256:old"},
			)

			mock := &msb.MockMsbClient{}
			vm := NewManager(&termio.Mock{})

			newVol, err := vm.ApplyHomeAction(
				context.Background(),
				mock,
				state.Key{Slug: slug, Agent: "opencode"},
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

			st, err := state.ReadState(state.Key{Slug: slug, Agent: "opencode"})
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
	configpaths.WithMockConfigPaths(t)

	slug := "myproj"
	oldVol := "opencode-sandbox-home-myproj-old"
	state.WriteState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: oldVol, ImageDigest: "sha256:old"},
	)

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
		state.Key{Slug: slug, Agent: "opencode"},
		oldVol,
		"img-tag",
		"sha256:new",
		ActionReset,
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

	st, err := state.ReadState(state.Key{Slug: slug, Agent: "opencode"})
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
	configpaths.WithMockConfigPaths(t)

	slug := "myproj"
	oldVol := "opencode-sandbox-home-myproj-old"
	state.WriteState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: oldVol, ImageDigest: "sha256:old"},
	)

	mock := &msb.MockMsbClient{}
	vm := NewManager(&termio.Mock{})

	newVol, err := vm.ApplyHomeAction(
		context.Background(),
		mock,
		state.Key{Slug: slug, Agent: "opencode"},
		oldVol,
		"img-tag",
		"sha256:new",
		ActionMigrate,
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

	st, err := state.ReadState(state.Key{Slug: slug, Agent: "opencode"})
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
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	slug := "myproj"
	oldVol := "opencode-sandbox-home-myproj-old"
	state.WriteState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: oldVol, ImageDigest: "sha256:old"},
	)

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
		state.Key{Slug: slug, Agent: "opencode"},
		oldVol,
		"img-tag",
		"sha256:new",
		ActionMigrate,
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

func TestFromKeyMapsValidKeys(t *testing.T) {
	tests := []struct {
		key  string
		want VolumeAction
	}{
		{"k", ActionKeep},
		{"m", ActionMigrate},
		{"r", ActionReset},
		{"q", ActionQuit},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got, err := FromKey(tt.key)
			if err != nil {
				t.Errorf("FromKey(%q) returned error: %v", tt.key, err)
			}
			if got != tt.want {
				t.Errorf("FromKey(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestFromKeyReturnsErrorForInvalidKey(t *testing.T) {
	_, err := FromKey("5")
	if err == nil {
		t.Errorf("FromKey(\"5\") returned nil error, want error")
	}
	_, err = FromKey("keep")
	if err == nil {
		t.Errorf("FromKey(\"keep\") returned nil error, want error")
	}
}

func TestVolumeActionString(t *testing.T) {
	tests := []struct {
		action VolumeAction
		want   string
	}{
		{ActionKeep, "keep"},
		{ActionMigrate, "migrate"},
		{ActionReset, "reset"},
		{ActionQuit, "quit"},
		{VolumeAction(99), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.action.String()
			if got != tt.want {
				t.Errorf("VolumeAction(%d).String() = %q, want %q", tt.action, got, tt.want)
			}
		})
	}
}

func TestResolveHomeVolume_FoundInState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	state.WriteState(state.Key{Slug: "myproj", Agent: "opencode"}, state.HomeState{
		HomeVolume:  "opencode-sandbox-home-myproj-20260806T143022",
		ImageDigest: "sha256:abc",
	})

	vm := NewManager(&termio.Mock{})
	volName, st, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		state.Key{Slug: "myproj", Agent: "opencode"},
		"sha256:abc",
		"",
		false,
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if volName != "opencode-sandbox-home-myproj-20260806T143022" {
		t.Errorf("volume = %q, want %q", volName, "opencode-sandbox-home-myproj-20260806T143022")
	}
	if st.ImageDigest != "sha256:abc" {
		t.Errorf("digest = %q, want %q", st.ImageDigest, "sha256:abc")
	}
}

func TestResolveHomeVolume_NoStateFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	mock := &msb.MockMsbClient{}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	vm := NewManager(&termio.Mock{})
	volName, st, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		state.Key{Slug: "testproj", Agent: "opencode"},
		"sha256:def",
		"",
		false,
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(volName, "opencode-sandbox-home-testproj-") {
		t.Errorf("volume = %q, expected prefix %q", volName, "opencode-sandbox-home-testproj-")
	}
	if st.ImageDigest != "sha256:def" {
		t.Errorf("digest = %q, want %q", st.ImageDigest, "sha256:def")
	}
}

// TestResolveHomeVolume_CorruptStateFile covers the branch where the state file
// exists but cannot be read (a non-not-found error): the caller warns about the
// missing state and creates a fresh home volume.
func TestResolveHomeVolume_CorruptStateFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	// A directory in place of the state file makes os.ReadFile fail with a
	// non-not-found error, unlike a genuinely absent file.
	statePath := filepath.Join(configpaths.Get().UserStateDir(), "corruptproj", "opencode", "state.yaml")
	if err := os.MkdirAll(statePath, 0o700); err != nil {
		t.Fatal(err)
	}

	mock := &msb.MockMsbClient{}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	ui := &termio.Mock{}
	vm := NewManager(ui)
	volName, st, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		state.Key{Slug: "corruptproj", Agent: "opencode"},
		"sha256:def",
		"",
		false,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(volName, "opencode-sandbox-home-corruptproj-") {
		t.Errorf("volume = %q, expected prefix %q", volName, "opencode-sandbox-home-corruptproj-")
	}
	if st.ImageDigest != "sha256:def" {
		t.Errorf("digest = %q, want %q", st.ImageDigest, "sha256:def")
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, " "), "missing state file") {
		t.Errorf("expected a 'missing state file' warning, got %v", ui.WarnCalls)
	}
}

func TestResolveHomeVolume_VolumeNotFoundInSandbox(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		var vh msb.VolumeHandle
		return vh, fmt.Errorf("volume %s not found", name)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	state.WriteState(state.Key{Slug: "orphanproj", Agent: "opencode"}, state.HomeState{
		HomeVolume:  "opencode-sandbox-home-orphanproj-20260806T143022",
		ImageDigest: "sha256:abc",
	})

	vm := NewManager(&termio.Mock{})
	volName, st, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		state.Key{Slug: "orphanproj", Agent: "opencode"},
		"sha256:def",
		"latest-docker-image",
		false,
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(volName, "opencode-sandbox-home-orphanproj-") {
		t.Errorf("volume = %q, expected prefix %q", volName, "opencode-sandbox-home-orphanproj-")
	}
	if st.ImageDigest != "sha256:def" {
		t.Errorf("digest = %q, want %q", st.ImageDigest, "sha256:def")
	}
}

func TestInvalidKeyError(t *testing.T) {
	e := &invalidKeyError{key: "z"}
	if got := e.Error(); got != "invalid action key: z" {
		t.Errorf("invalidKeyError.Error() = %q, want %q", got, "invalid action key: z")
	}
}

func TestFromKey(t *testing.T) {
	if a, err := FromKey("k"); err != nil || a != ActionKeep {
		t.Errorf("FromKey(k) = %v, %v", a, err)
	}
	if a, err := FromKey("m"); err != nil || a != ActionMigrate {
		t.Errorf("FromKey(m) = %v, %v", a, err)
	}
	if a, err := FromKey("r"); err != nil || a != ActionReset {
		t.Errorf("FromKey(r) = %v, %v", a, err)
	}
	if a, err := FromKey("q"); err != nil || a != ActionQuit {
		t.Errorf("FromKey(q) = %v, %v", a, err)
	}
	if _, err := FromKey("x"); err == nil {
		t.Error("FromKey(x) should error")
	}
}

func TestActionLabel(t *testing.T) {
	if got := actionLabel(ActionReset); got != "reset" {
		t.Errorf("actionLabel(reset) = %q, want reset", got)
	}
	if got := actionLabel(ActionMigrate); got != "migrate" {
		t.Errorf("actionLabel(migrate) = %q, want migrate", got)
	}
	if got := actionLabel(ActionKeep); got != "keep" {
		t.Errorf("actionLabel(keep) = %q, want keep", got)
	}
}

func TestCleanupVolume_Success(t *testing.T) {
	mock := &msb.MockMsbClient{}
	vm := NewManager(&termio.Mock{})

	vm.cleanupVolume(context.Background(), mock, "orphan-vol", &termio.Mock{})

	if len(mock.RemovedVolumes) != 1 {
		t.Fatalf("expected volume to be removed, got %v", mock.RemovedVolumes)
	}
}

func TestCleanupVolume_Failure(t *testing.T) {
	mock := &msb.MockMsbClient{}
	mock.RemoveVolumeFn = func(_ context.Context, _ string) error {
		return errors.New("volume busy")
	}
	ui := &termio.Mock{}
	vm := NewManager(ui)

	vm.cleanupVolume(context.Background(), mock, "orphan-vol", ui)

	if len(ui.WarnCalls) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(ui.WarnCalls))
	}
	if !strings.Contains(ui.WarnCalls[0], "failed to remove") {
		t.Errorf("unexpected warning: %q", ui.WarnCalls[0])
	}
}

func TestPrefillVolume_CreateSandboxFails(t *testing.T) {
	docker.WithNoopDockerMock(t)
	mock := &msb.MockMsbClient{}
	mock.CreateSandboxErr = errors.New("sandbox creation failed")
	vm := NewManager(&termio.Mock{})

	err := vm.PrefillVolume(
		context.Background(),
		mock,
		state.Key{Slug: "myproject", Agent: "opencode"},
		"test-home-vol",
		"img-tag",
		&termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "create prefill sandbox") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPrefillVolume_ExecFails(t *testing.T) {
	docker.WithNoopDockerMock(t)
	mock := &msb.MockMsbClient{}
	mock.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return msb.NewMockSandbox(msb.SandboxOpts{
			ExecErr: errors.New("exec failed"),
		}), nil
	}
	ui := &termio.Mock{}
	vm := NewManager(ui)

	err := vm.PrefillVolume(
		context.Background(),
		mock,
		state.Key{Slug: "myproject", Agent: "opencode"},
		"test-home-vol",
		"img-tag",
		ui,
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "prefill cp") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPrefillVolume_ExecExitFailure(t *testing.T) {
	docker.WithNoopDockerMock(t)
	mock := &msb.MockMsbClient{}
	mock.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return msb.NewMockSandbox(msb.SandboxOpts{
			ExecOut: map[string]msb.ShellResult{
				"sh -c cp -a /home/dev/. /mnt/home/ && chown -R dev:dev /mnt/home": msb.NewTestResult(
					false,
					1,
					"",
					"cp: permission denied",
					nil,
				),
			},
		}), nil
	}
	ui := &termio.Mock{}
	vm := NewManager(ui)

	err := vm.PrefillVolume(
		context.Background(),
		mock,
		state.Key{Slug: "myproject", Agent: "opencode"},
		"test-home-vol",
		"img-tag",
		ui,
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "prefill cp failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCopyVolume_CreateSandboxFails(t *testing.T) {
	docker.WithNoopDockerMock(t)
	mock := &msb.MockMsbClient{}
	mock.CreateSandboxErr = errors.New("sandbox creation failed")
	vm := NewManager(&termio.Mock{})

	err := vm.CopyVolume(
		context.Background(),
		mock,
		state.Key{Slug: "myproject", Agent: "opencode"},
		"old-vol",
		"new-vol",
		"img-tag",
		&termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "create copy sandbox") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCopyVolume_ExecFails(t *testing.T) {
	docker.WithNoopDockerMock(t)
	mock := &msb.MockMsbClient{}
	mock.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return msb.NewMockSandbox(msb.SandboxOpts{
			ExecErr: errors.New("exec failed during copy"),
		}), nil
	}
	ui := &termio.Mock{}
	vm := NewManager(ui)

	err := vm.CopyVolume(
		context.Background(),
		mock,
		state.Key{Slug: "myproject", Agent: "opencode"},
		"old-vol",
		"new-vol",
		"img-tag",
		ui,
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "copy files") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCopyVolume_ExecExitFailure(t *testing.T) {
	docker.WithNoopDockerMock(t)
	mock := &msb.MockMsbClient{}
	mock.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return msb.NewMockSandbox(msb.SandboxOpts{
			ExecOut: map[string]msb.ShellResult{
				"sh -c cp -a /src/. /dst/ && chown -R dev:dev /dst": msb.NewTestResult(
					false,
					1,
					"",
					"chown: permission denied",
					nil,
				),
			},
		}), nil
	}
	ui := &termio.Mock{}
	vm := NewManager(ui)

	err := vm.CopyVolume(
		context.Background(),
		mock,
		state.Key{Slug: "myproject", Agent: "opencode"},
		"old-vol",
		"new-vol",
		"img-tag",
		ui,
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "copy failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestApplyHomeAction_Reset_Success(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	slug := "myproj"
	oldVol := "opencode-sandbox-home-myproj-old"
	state.WriteState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: oldVol, ImageDigest: "sha256:old"},
	)

	mock := &msb.MockMsbClient{}
	vm := NewManager(&termio.Mock{})

	newVol, err := vm.ApplyHomeAction(
		context.Background(),
		mock,
		state.Key{Slug: slug, Agent: "opencode"},
		oldVol,
		"img-tag",
		"sha256:new",
		ActionReset,
		options.RunOptions{},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if newVol == oldVol {
		t.Errorf("expected new volume, got old volume %q", newVol)
	}
	if len(mock.CreatedSandboxes) != 1 {
		t.Errorf("expected 1 sandbox for reset, got %d", len(mock.CreatedSandboxes))
	}

	st, err := state.ReadState(state.Key{Slug: slug, Agent: "opencode"})
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.HomeVolume != newVol {
		t.Errorf("state HomeVolume = %q, want %q", st.HomeVolume, newVol)
	}
}

func TestApplyHomeAction_PrefillFails_RemovesVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	slug := "myproj"
	oldVol := "opencode-sandbox-home-myproj-old"
	state.WriteState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: oldVol, ImageDigest: "sha256:old"},
	)

	mock := &msb.MockMsbClient{}
	mock.CreateSandboxErr = errors.New("prefill sandbox creation failed")
	vm := NewManager(&termio.Mock{})

	_, err := vm.ApplyHomeAction(
		context.Background(),
		mock,
		state.Key{Slug: slug, Agent: "opencode"},
		oldVol,
		"img-tag",
		"sha256:new",
		ActionReset,
		options.RunOptions{},
		&termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(mock.RemovedVolumes) == 0 {
		t.Errorf("expected new volume to be cleaned up, got %v", mock.RemovedVolumes)
	}
}

func TestEnsureNewHome_PrefillFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	mock := &msb.MockMsbClient{}
	mock.CreateSandboxErr = errors.New("prefill sandbox creation failed")
	vm := NewManager(&termio.Mock{})

	_, _, err := vm.EnsureNewHome(
		context.Background(),
		mock,
		state.Key{Slug: "testproj", Agent: "opencode"},
		"sha256:abc",
		"img-tag",
		false,
		&termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "prefill") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEnsureNewHome_DryRunVM_NoSandbox(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := &msb.MockMsbClient{}
	vm := NewManager(&termio.Mock{})

	volName, _, err := vm.EnsureNewHome(
		context.Background(),
		mock,
		state.Key{Slug: "testproj", Agent: "opencode"},
		"sha256:abc",
		"img-tag",
		true, // dryRunVM
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(volName, "opencode-sandbox-home-testproj-") {
		t.Errorf("expected home volume prefix, got %q", volName)
	}
	if len(mock.CreatedSandboxes) != 0 {
		t.Errorf("expected no sandboxes in dry-run-vm, got %d", len(mock.CreatedSandboxes))
	}
}

func TestResolveHomeAction_PromptFails_ReturnsKeep(t *testing.T) {
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "", errors.New("select failed")
		},
	}
	vm := NewManager(ui)
	action := vm.ResolveHomeAction(ui, "old", "new")
	if action != ActionKeep {
		t.Errorf("expected ActionKeep on prompt failure, got %v", action)
	}
	if len(ui.WarnCalls) != 1 {
		t.Errorf("expected 1 warning on prompt failure, got %d", len(ui.WarnCalls))
	}
}
