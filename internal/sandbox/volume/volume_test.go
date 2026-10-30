package volume

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/atomicwrite"
	"github.com/inoio/agents-sandbox/internal/configpaths"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestHomeVolumeNameScopesAgent(t *testing.T) {
	got := HomeVolumeName(state.Key{Slug: "proj-abc123", Agent: "pi"})
	prefix := "proj-abc123-pi-"
	if !strings.HasPrefix(got, prefix) {
		t.Errorf("HomeVolumeName = %q, want prefix %q", got, prefix)
	}
	if len(got) != len(prefix)+15 {
		t.Errorf("HomeVolumeName = %q, want 15-char timestamp suffix", got)
	}
}

func TestHomeVolumeName(t *testing.T) {
	got := HomeVolumeName(state.Key{Slug: "myproj-aBc1234D", Agent: "opencode"})
	expectedPrefix := "myproj-aBc1234D-opencode-"
	if !strings.HasPrefix(got, expectedPrefix) {
		t.Errorf("expected prefix %q, got %q", expectedPrefix, got)
	}
	suffix := strings.TrimPrefix(got, expectedPrefix)
	if len(suffix) != 15 {
		t.Errorf("expected 15-char timestamp, got %d chars: %q", len(suffix), suffix)
	}
}

func TestHomeVolumeNameDifferentInputs(t *testing.T) {
	got := HomeVolumeName(state.Key{Slug: "myproj-aBc1234D", Agent: "opencode"})
	if !strings.HasPrefix(got, "myproj-aBc1234D-opencode-") {
		t.Errorf("unexpected name format: %q", got)
	}
}

func TestHomeVolumeNameTimestamp(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	got := HomeVolumeName(state.Key{Slug: "myproject", Agent: "opencode"})
	after := time.Now().UTC().Add(time.Second)

	if !strings.HasPrefix(got, "myproject-opencode-") {
		t.Fatalf("expected prefix, got %q", got)
	}
	suffix := strings.TrimPrefix(got, "myproject-opencode-")
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
	prefillSandbox := &msb.MockSandbox{Name_: "prefill"}
	client := &msb.MockMsbClient{CreatedSandbox: prefillSandbox}
	vm := NewManager(ui)

	err := vm.PrefillVolume(
		context.Background(),
		client,
		state.Key{Slug: "myproject", Agent: "opencode"},
		"test-home-vol",
		image.ImageInfo{DockerTag: "agents-sandbox/runner-test:latest", Ref: "agents-sandbox/runner-test:latest"},
		ui,
	)
	if err != nil {
		t.Fatalf("PrefillVolume failed: %v", err)
	}
	if len(client.CreatedSandboxes) != 1 {
		t.Fatalf("expected 1 created prefill sandbox, got %d", len(client.CreatedSandboxes))
	}
	if !prefillSandbox.DidDestroy() {
		t.Fatal("expected the prefill sandbox to be destroyed through its live handle")
	}
}

func TestPrefillVolume_DestroyToleratesReplaced(t *testing.T) {
	docker.WithNoopDockerMock(t)
	ui := &termio.Mock{}
	prefillSandbox := &msb.MockSandbox{
		Name_:      "prefill",
		DestroyErr: &msbSdk.Error{Kind: msbSdk.ErrSandboxReplaced, Message: "replaced"},
	}
	client := &msb.MockMsbClient{CreatedSandbox: prefillSandbox}
	vm := NewManager(ui)

	err := vm.PrefillVolume(
		context.Background(),
		client,
		state.Key{Slug: "myproject", Agent: "opencode"},
		"test-home-vol",
		image.ImageInfo{DockerTag: "agents-sandbox/runner-test:latest", Ref: "agents-sandbox/runner-test:latest"},
		ui,
	)
	if err != nil {
		t.Fatalf("PrefillVolume failed: %v", err)
	}
	if len(ui.WarnCalls) != 0 {
		t.Errorf("a replaced throwaway sandbox must not warn, got %v", ui.WarnCalls)
	}
}

func TestPrefillVolume_DestroyFailureWarns(t *testing.T) {
	docker.WithNoopDockerMock(t)
	ui := &termio.Mock{}
	prefillSandbox := &msb.MockSandbox{Name_: "prefill", DestroyErr: errors.New("destroy failed")}
	client := &msb.MockMsbClient{CreatedSandbox: prefillSandbox}
	vm := NewManager(ui)

	err := vm.PrefillVolume(
		context.Background(),
		client,
		state.Key{Slug: "myproject", Agent: "opencode"},
		"test-home-vol",
		image.ImageInfo{DockerTag: "agents-sandbox/runner-test:latest", Ref: "agents-sandbox/runner-test:latest"},
		ui,
	)
	if err != nil {
		t.Fatalf("PrefillVolume failed: %v", err)
	}
	if len(ui.WarnCalls) != 1 || !strings.Contains(ui.WarnCalls[0], "failed to destroy prefill sandbox") {
		t.Errorf("expected a destroy-failure warning, got %v", ui.WarnCalls)
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

	seedHomeState(state.Key{Slug: "myproj", Agent: "opencode"}, state.HomeState{
		HomeVolume:  "agents-sandbox-home-myproj-20260806T143022",
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
	if st.HomeVolume != "agents-sandbox-home-myproj-20260806T143022" {
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

// TestApplyHomeActionAbortsWhenHomeVolumeChangedConcurrently pins the same
// compare-and-set guard for the run-time home action: after its msb I/O it
// commits under the home lock only if home_volume is still the resolved one.
func TestApplyHomeActionAbortsWhenHomeVolumeChangedConcurrently(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	key := state.Key{Slug: "myproj", Agent: "opencode"}
	oldVol := "agents-sandbox-home-myproj-old"
	seedHomeState(key, state.HomeState{HomeVolume: oldVol, ImageDigest: "sha256:old"})

	mock := &msb.MockMsbClient{}
	mock.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		if err := state.WithHomeState(key, func(cur *state.HomeState, _ bool) (bool, error) {
			cur.HomeVolume = "agents-sandbox-home-myproj-other"
			return true, nil
		}); err != nil {
			t.Fatalf("concurrent write: %v", err)
		}
		return msb.NewMockSandbox(msb.SandboxOpts{}), nil
	}
	vm := NewManager(&termio.Mock{})

	_, err := vm.ApplyHomeAction(
		context.Background(),
		mock,
		key,
		oldVol,
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
		ActionReset,
		false,
		&termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected a concurrent-change error, got nil")
	}
	if !strings.Contains(err.Error(), "home volume changed concurrently") {
		t.Errorf("unexpected error: %v", err)
	}

	st, err := state.ReadState(key)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.HomeVolume != "agents-sandbox-home-myproj-other" {
		t.Errorf("HomeVolume = %q, want the concurrent writer's value", st.HomeVolume)
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
		"agents-sandbox-home-myproj-old",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
		ActionKeep,
		false,
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vol != "agents-sandbox-home-myproj-old" {
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
			oldVol := "agents-sandbox-home-myproj-old"
			seedHomeState(
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
				image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
				tt.action,
				false,
				&termio.Mock{},
			)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if newVol == oldVol {
				t.Errorf("%s should produce a new volume, got %q", actionLabel(tt.action), newVol)
			}
			if len(mock.CreatedSandboxes) != tt.wantSandboxes {
				t.Errorf(
					"expected %d sandboxes for %s, got %d",
					tt.wantSandboxes,
					actionLabel(tt.action),
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

// TestApplyHomeAction_Reset_SkipVM_NoWrites pins the skip-VM gate: the chosen
// reset is deferred without a VM, so the old volume and the state are untouched.
func TestApplyHomeAction_Reset_SkipVM_NoWrites(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "myproj"
	oldVol := "agents-sandbox-home-myproj-old"
	seedHomeState(
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
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
		ActionReset,
		true,
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if newVol != oldVol {
		t.Errorf("skip-VM should keep old volume, got %q", newVol)
	}
	if createdVols != 0 {
		t.Errorf("skip-VM should not create volumes, got %d", createdVols)
	}

	st, err := state.ReadState(state.Key{Slug: slug, Agent: "opencode"})
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.HomeVolume != oldVol {
		t.Errorf("skip-VM should not change state HomeVolume, got %q", st.HomeVolume)
	}
	if st.ImageDigest != "sha256:old" {
		t.Errorf("skip-VM should not change state ImageDigest, got %q", st.ImageDigest)
	}
}

// TestApplyHomeAction_Migrate_SkipVM_NoStateWrite pins the migrate variant of
// the skip-VM gate: the copy would spawn a VM, so it is deferred and the state
// keeps the old volume and digest.
func TestApplyHomeAction_Migrate_SkipVM_NoStateWrite(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "myproj"
	oldVol := "agents-sandbox-home-myproj-old"
	seedHomeState(
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
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
		ActionMigrate,
		true,
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if newVol != oldVol {
		t.Errorf("skip-VM should keep old volume, got %q", newVol)
	}
	if len(mock.CreatedSandboxes) != 0 {
		t.Errorf("skip-VM should not spawn VMs, got %v", mock.CreatedSandboxes)
	}

	st, err := state.ReadState(state.Key{Slug: slug, Agent: "opencode"})
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.HomeVolume != oldVol {
		t.Errorf("skip-VM should not write state HomeVolume, got %q", st.HomeVolume)
	}
	if st.ImageDigest != "sha256:old" {
		t.Errorf("skip-VM should not write state ImageDigest, got %q", st.ImageDigest)
	}
}

func TestApplyHomeAction_Migrate_CopyFails_RemovesNewVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	slug := "myproj"
	oldVol := "agents-sandbox-home-myproj-old"
	seedHomeState(
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
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
		ActionMigrate,
		false,
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

func TestResolveHomeVolume_FoundInState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	seedHomeState(state.Key{Slug: "myproj", Agent: "opencode"}, state.HomeState{
		HomeVolume:  "agents-sandbox-home-myproj-20260806T143022",
		ImageDigest: "sha256:abc",
	})

	vm := NewManager(&termio.Mock{})
	volName, st, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		state.Key{Slug: "myproj", Agent: "opencode"},
		image.ImageInfo{DockerTag: "", Ref: "", Digest: "sha256:abc"},
		false,
		state.Fingerprints{},
		"",
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if volName != "agents-sandbox-home-myproj-20260806T143022" {
		t.Errorf("volume = %q, want %q", volName, "agents-sandbox-home-myproj-20260806T143022")
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
		image.ImageInfo{DockerTag: "", Ref: "", Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		"",
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(volName, "testproj-") {
		t.Errorf("volume = %q, expected prefix %q", volName, "testproj-")
	}
	if st.ImageDigest != "sha256:def" {
		t.Errorf("digest = %q, want %q", st.ImageDigest, "sha256:def")
	}
}

// TestResolveHomeVolume_CorruptStateFile covers an unreadable state file (a
// non-not-found read error): the caller warns about it and falls back to a
// fresh home volume. A directory in place of the state file cannot be repaired
// by the fallback's wholesale write either, so the write error now surfaces
// (#428) instead of the run proceeding against a volume with no state record;
// the uncommitted volume is removed.
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
	_, _, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		state.Key{Slug: "corruptproj", Agent: "opencode"},
		image.ImageInfo{DockerTag: "", Ref: "", Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		"",
		ui,
	)
	if err == nil {
		t.Fatal("expected the unrepairable state write to surface, got nil")
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, " "), "could not read state file") {
		t.Errorf("expected a 'could not read state file' warning, got %v", ui.WarnCalls)
	}
	if len(mock.RemovedVolumes) != 1 {
		t.Errorf("expected the uncommitted volume to be removed, got %v", mock.RemovedVolumes)
	}
}

func TestResolveHomeVolume_VolumeNotFoundInSandbox(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		var vh msb.VolumeHandle
		return vh, volumeNotFoundError(name)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	seedHomeState(state.Key{Slug: "orphanproj", Agent: "opencode"}, state.HomeState{
		HomeVolume:  "agents-sandbox-home-orphanproj-20260806T143022",
		ImageDigest: "sha256:abc",
	})

	vm := NewManager(&termio.Mock{})
	volName, st, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		state.Key{Slug: "orphanproj", Agent: "opencode"},
		image.ImageInfo{DockerTag: "latest-docker-image", Ref: "latest-docker-image", Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		"",
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(volName, "orphanproj-") {
		t.Errorf("volume = %q, expected prefix %q", volName, "orphanproj-")
	}
	if st.ImageDigest != "sha256:def" {
		t.Errorf("digest = %q, want %q", st.ImageDigest, "sha256:def")
	}
}

// TestResolveHomeVolume_TransientRecordedErrorFailsClosed pins that a lookup
// error that is not true absence (a daemon, timeout, or permission failure) is
// never mistaken for a missing volume: resolution fails closed rather than
// minting a fresh volume over the still-valid one and rewriting the state.
func TestResolveHomeVolume_TransientRecordedErrorFailsClosed(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	key := state.Key{Slug: "transientproj", Agent: "opencode"}
	seedHomeState(key, state.HomeState{
		HomeVolume:  "agents-sandbox-home-transientproj-20260806T143022",
		ImageDigest: "sha256:abc",
	})

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		var vh msb.VolumeHandle
		return vh, fmt.Errorf("daemon unavailable looking up %s", name)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		t.Fatalf("CreateVolume must not be called on a transient lookup error (got %q)", name)
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	ui := &termio.Mock{}
	vm := NewManager(ui)
	_, _, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		key,
		image.ImageInfo{Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		"",
		ui,
	)
	if err == nil {
		t.Fatal("ResolveHomeVolume returned no error for a transient lookup failure, want fail-closed")
	}

	persisted, readErr := state.ReadState(key)
	if readErr != nil {
		t.Fatalf("state not readable after fail-closed: %v", readErr)
	}
	if persisted.HomeVolume != "agents-sandbox-home-transientproj-20260806T143022" {
		t.Errorf("state was rewritten on a transient error: HomeVolume = %q", persisted.HomeVolume)
	}
}

// TestResolveHomeVolume_TransientRecoveryErrorFailsClosed pins that a transient
// error while confirming the recovered candidate also fails closed: recovery
// must not fall back to a fresh prefill when the candidate's existence cannot
// be established.
func TestResolveHomeVolume_TransientRecoveryErrorFailsClosed(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const recovered = "agents-sandbox-home-transientrecover-20260101T000000"
	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		var vh msb.VolumeHandle
		return vh, fmt.Errorf("daemon unavailable looking up %s", name)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		t.Fatalf("CreateVolume must not be called on a transient lookup error (got %q)", name)
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	ui := &termio.Mock{}
	vm := NewManager(ui)
	_, _, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		state.Key{Slug: "transientrecover", Agent: "opencode"},
		image.ImageInfo{Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		recovered,
		ui,
	)
	if err == nil {
		t.Fatal("ResolveHomeVolume returned no error for a transient recovery lookup, want fail-closed")
	}
}

// TestConfirmedRecoveredVolume pins the three outcomes that drive recovery:
// an empty candidate and a genuinely absent volume report "not confirmed" with
// no error (fall back to a fresh prefill), a present volume is confirmed, and
// any other lookup error is returned so the caller fails closed.
func TestConfirmedRecoveredVolume(t *testing.T) {
	const present = "agents-sandbox-home-confirm-20260101T000000"

	tests := []struct {
		name      string
		candidate string
		getErr    error
		wantName  string
		wantOK    bool
		wantErr   bool
	}{
		{"empty candidate", "", nil, "", false, false},
		{"present", present, nil, present, true, false},
		{"absent", present, volumeNotFoundError(present), "", false, false},
		{"transient", present, errors.New("daemon unavailable"), "", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &msb.MockMsbClient{}
			mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
				if tt.getErr != nil {
					var vh msb.VolumeHandle
					return vh, tt.getErr
				}
				return msb.MockVolumeHandle{Name_: name}, nil
			}

			name, ok, err := confirmedRecoveredVolume(context.Background(), mock, tt.candidate)
			if name != tt.wantName || ok != tt.wantOK {
				t.Errorf("confirmedRecoveredVolume() = (%q, %v), want (%q, %v)", name, ok, tt.wantName, tt.wantOK)
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("confirmedRecoveredVolume() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// when a project's state file is lost but its VM still mounts a home volume,
// the existing volume must be reused instead of a duplicate being created and
// the previous one orphaned.
func TestResolveHomeVolume_RecoversLiveVMVolumeOnMissingState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const recovered = "agents-sandbox-home-recoverproj-20260101T000000"
	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		if name == recovered {
			return msb.MockVolumeHandle{Name_: name}, nil
		}
		var vh msb.VolumeHandle
		return vh, volumeNotFoundError(name)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		t.Fatalf("CreateVolume must not be called when a live home volume can be recovered (got %q)", name)
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	key := state.Key{Slug: "recoverproj", Agent: "opencode"}
	ui := &termio.Mock{}
	vm := NewManager(ui)
	volName, st, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		key,
		image.ImageInfo{Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		recovered,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if volName != recovered {
		t.Errorf("volume = %q, want recovered %q", volName, recovered)
	}
	if st.HomeVolume != recovered {
		t.Errorf("state volume = %q, want %q", st.HomeVolume, recovered)
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, " "), "recovering home volume") {
		t.Errorf("expected a recovery warning, got %v", ui.WarnCalls)
	}
	persisted, err := state.ReadState(key)
	if err != nil {
		t.Fatalf("state not written: %v", err)
	}
	if persisted.HomeVolume != recovered {
		t.Errorf("persisted volume = %q, want %q", persisted.HomeVolume, recovered)
	}
}

// TestResolveHomeVolume_RecoversPersistedAdoptionVolume covers the durable
// VM-only identity adoption (#144): the checkout state records the home volume a
// destroyed legacy VM mounted, and resolution finishes the recovery into
// HomeVolume with the current digest, seeds fingerprints, and clears the marker.
func TestResolveHomeVolume_RecoversPersistedAdoptionVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const recovered = "agents-sandbox-home-widget-aaa-opencode-9"
	key := state.Key{Slug: "adoptedwidget", Agent: "opencode"}
	seedHomeState(key, state.HomeState{RecoveredHomeVolume: recovered})

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		if name == recovered {
			return msb.MockVolumeHandle{Name_: name}, nil
		}
		var vh msb.VolumeHandle
		return vh, volumeNotFoundError(name)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		t.Fatalf("CreateVolume must not be called during recovery (got %q)", name)
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	ui := &termio.Mock{}
	vm := NewManager(ui)
	volName, hs, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		key,
		image.ImageInfo{Digest: "sha256:new"},
		false,
		state.Fingerprints{Env: state.EnvState{Hash: "envhash"}},
		"",
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if volName != recovered {
		t.Errorf("volume = %q, want recovered %q", volName, recovered)
	}
	if hs.HomeVolume != recovered || hs.ImageDigest != "sha256:new" || hs.RecoveredHomeVolume != "" {
		t.Errorf("state = %+v, want the marker recovered into HomeVolume with the digest and cleared", hs)
	}
	if hs.EnvState.Hash != "envhash" {
		t.Errorf("fingerprints used to seed = %+v, want envhash", hs.EnvState)
	}
	persisted, err := state.ReadState(key)
	if err != nil {
		t.Fatalf("state not written: %v", err)
	}
	if persisted.HomeVolume != recovered || persisted.RecoveredHomeVolume != "" {
		t.Errorf("persisted state = %+v, want the marker cleared", persisted)
	}
}

// TestResolveHomeVolume_RecoveredVolumeMissingStartsFresh verifies that a live
// VM mount that no longer resolves (e.g. the volume was removed) does not wedge
// resolution: it falls back to a fresh prefill.
func TestResolveHomeVolume_RecoveredVolumeMissingStartsFresh(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	const recovered = "agents-sandbox-home-goneproj-20260101T000000"
	var createdName string
	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		var vh msb.VolumeHandle
		return vh, volumeNotFoundError(name)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		createdName = name
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	ui := &termio.Mock{}
	vm := NewManager(ui)
	volName, _, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		state.Key{Slug: "goneproj", Agent: "opencode"},
		image.ImageInfo{Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		recovered,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if createdName == "" {
		t.Fatal("expected a fresh volume to be created when the recovered volume is gone")
	}
	if volName != createdName {
		t.Errorf("returned volume = %q, want created volume %q", volName, createdName)
	}
	if volName == recovered {
		t.Errorf("returned volume = %q, must not reuse the missing recovered name", recovered)
	}
}

// TestResolveHomeVolume_RecoversWhenRecordedVolumeGone covers the case where
// state exists but its recorded volume vanished while the VM still mounts a
// different, existing home volume: the VM's volume is adopted.
func TestResolveHomeVolume_RecoversWhenRecordedVolumeGone(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const recorded = "agents-sandbox-home-drifted-20250101T000000"
	const recovered = "agents-sandbox-home-drifted-20260101T000000"
	key := state.Key{Slug: "drifted", Agent: "opencode"}
	seedHomeState(key, state.HomeState{HomeVolume: recorded, ImageDigest: "sha256:old"})

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		if name == recovered {
			return msb.MockVolumeHandle{Name_: name}, nil
		}
		var vh msb.VolumeHandle
		return vh, volumeNotFoundError(name)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		t.Fatalf("CreateVolume must not be called when the live volume can be recovered (got %q)", name)
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	ui := &termio.Mock{}
	vm := NewManager(ui)
	volName, _, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		key,
		image.ImageInfo{Digest: "sha256:new"},
		false,
		state.Fingerprints{},
		recovered,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if volName != recovered {
		t.Errorf("volume = %q, want recovered %q", volName, recovered)
	}
}

// TestResolveHomeVolume_RecoveryStateFails covers the branch where the recovery
// transaction cannot run (e.g. a directory sits at the state path, so the state
// read fails): the volume is still returned and a warning is emitted.
func TestResolveHomeVolume_RecoveryStateFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const recovered = "agents-sandbox-home-writefail-20260101T000000"
	key := state.Key{Slug: "writefail", Agent: "opencode"}
	statePath := filepath.Join(configpaths.Get().UserStateDir(), "writefail", "opencode", "state.yaml")
	if err := os.MkdirAll(statePath, 0o700); err != nil {
		t.Fatal(err)
	}

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		if name == recovered {
			return msb.MockVolumeHandle{Name_: name}, nil
		}
		var vh msb.VolumeHandle
		return vh, volumeNotFoundError(name)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		t.Fatalf("CreateVolume must not be called during recovery (got %q)", name)
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	ui := &termio.Mock{}
	vm := NewManager(ui)
	volName, _, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		key,
		image.ImageInfo{Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		recovered,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if volName != recovered {
		t.Errorf("volume = %q, want recovered %q", volName, recovered)
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, " "), "failed to recover state file") {
		t.Errorf("expected a state-recovery warning, got %v", ui.WarnCalls)
	}
}

// TestResolveHomeVolume_StateWinsOverRecoveredVolume pins the precedence rule:
// when state resolves to an existing volume, the live VM's volume is never
// consulted, even if it differs.
func TestResolveHomeVolume_StateWinsOverRecoveredVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const stateVol = "agents-sandbox-home-precedence-20250101T000000"
	const liveVol = "agents-sandbox-home-precedence-20260101T000000"
	key := state.Key{Slug: "precedence", Agent: "opencode"}
	seedHomeState(key, state.HomeState{HomeVolume: stateVol, ImageDigest: "sha256:abc"})

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		if name != stateVol {
			t.Errorf("GetVolume called with %q; the live volume must not be consulted when state resolves", name)
		}
		return msb.MockVolumeHandle{Name_: name}, nil
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		t.Fatalf("CreateVolume must not be called (got %q)", name)
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	ui := &termio.Mock{}
	vm := NewManager(ui)
	volName, _, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		key,
		image.ImageInfo{Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		liveVol,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if volName != stateVol {
		t.Errorf("volume = %q, want state volume %q", volName, stateVol)
	}
}

// TestResolveHomeVolume_RecoveryPreservesFingerprints pins that recovering a
// live volume from a still-readable prior state carries the env/secret/network/
// mount fingerprints forward, so the recovered state does not itself trigger a
// spurious rebuild.
func TestResolveHomeVolume_RecoveryPreservesFingerprints(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const recorded = "agents-sandbox-home-fp-20250101T000000"
	const recovered = "agents-sandbox-home-fp-20260101T000000"
	key := state.Key{Slug: "fp", Agent: "opencode"}
	seedHomeState(key, state.HomeState{
		HomeVolume:      recorded,
		ImageDigest:     "sha256:old",
		AgentVersion:    "1.2.3",
		AgentSource:     "tool",
		DockerSource:    "user",
		OfferedVersions: []string{"2.0.0"},
		EnvState:        state.EnvState{Hash: "envhash", Names: []string{"A"}},
		SecretState:     state.SecretState{Hash: "secrethash", Names: []string{"B"}},
		NetworkState:    state.NetworkState{Hash: "nethash", Names: []string{"example.com"}},
		MountState:      state.MountState{Hash: "mountHash", Names: []string{"/x"}},
	})

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		if name == recovered {
			return msb.MockVolumeHandle{Name_: name}, nil
		}
		var vh msb.VolumeHandle
		return vh, volumeNotFoundError(name)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		t.Fatalf("CreateVolume must not be called during recovery (got %q)", name)
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	ui := &termio.Mock{}
	vm := NewManager(ui)
	volName, hs, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		key,
		image.ImageInfo{Digest: "sha256:new"},
		false,
		state.Fingerprints{},
		recovered,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if volName != recovered {
		t.Errorf("volume = %q, want recovered %q", volName, recovered)
	}
	if hs.EnvState.Hash != "envhash" || hs.NetworkState.Hash != "nethash" ||
		hs.SecretState.Hash != "secrethash" || hs.MountState.Hash != "mountHash" {
		t.Errorf("fingerprints not preserved: %+v", hs)
	}
	if hs.AgentVersion != "1.2.3" || hs.AgentSource != "tool" || hs.DockerSource != "user" ||
		len(hs.OfferedVersions) != 1 || hs.OfferedVersions[0] != "2.0.0" {
		t.Errorf("upgrade fields not preserved: %+v", hs)
	}
	if hs.ImageDigest != "sha256:new" {
		t.Errorf("digest = %q, want %q", hs.ImageDigest, "sha256:new")
	}
	persisted, err := state.ReadState(key)
	if err != nil {
		t.Fatalf("state not written: %v", err)
	}
	if persisted.NetworkState.Hash != "nethash" {
		t.Errorf("persisted network fingerprint = %q, want %q", persisted.NetworkState.Hash, "nethash")
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

// TestCleanupUncommittedVolume pins the post-rename contract: when the commit
// error means the state rename already installed the volume's name, the new
// volume must be kept (removing it would strand the saved state on a deleted
// volume); a pre-rename failure still cleans it up.
func TestCleanupUncommittedVolume(t *testing.T) {
	tests := []struct {
		name       string
		commitErr  error
		wantRemove bool
	}{
		{
			name:       "installed write keeps the volume",
			commitErr:  fmt.Errorf("write state file: %w: sync dir boom", atomicwrite.ErrInstalled),
			wantRemove: false,
		},
		{
			name:       "pre-rename failure removes the volume",
			commitErr:  errors.New("write state file: rename boom"),
			wantRemove: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &msb.MockMsbClient{}
			vm := NewManager(&termio.Mock{})

			vm.cleanupUncommittedVolume(context.Background(), mock, "new-vol", tt.commitErr, &termio.Mock{})

			removed := len(mock.RemovedVolumes) == 1
			if removed != tt.wantRemove {
				t.Errorf("removed = %v (RemovedVolumes %v), want %v", removed, mock.RemovedVolumes, tt.wantRemove)
			}
		})
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
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag"},
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
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag"},
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
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag"},
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
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag"},
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
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag"},
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
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag"},
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
	oldVol := "agents-sandbox-home-myproj-old"
	seedHomeState(
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
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
		ActionReset,
		false,
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

// TestApplyHomeAction_PreservesFingerprints pins that a migrate/reset home
// action keeps the env/secret/network/mount fingerprints. They describe the
// live VM, not the home volume, so discarding them would make the next run see
// every fingerprint as changed and blame a spurious VM rebuild on (e.g.) the
// network policy. This mirrors TestVolumeOp_PreservesEnvSecretFingerprints.
func TestApplyHomeAction_PreservesFingerprints(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	slug := "myproj"
	oldVol := "agents-sandbox-home-myproj-old"
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{
			HomeVolume:      oldVol,
			ImageDigest:     "sha256:old",
			AgentVersion:    "1.2.3",
			AgentSource:     "tool",
			DockerSource:    "user",
			OfferedVersions: []string{"2.0.0"},
			EnvState:        state.EnvState{Hash: "envhash", Names: []string{"FOO"}},
			SecretState:     state.SecretState{Hash: "sechash", Names: []string{"TOKEN"}},
			NetworkState:    state.NetworkState{Hash: "nethash", Names: []string{"none"}},
			MountState:      state.MountState{Hash: "mounthash", Names: []string{"/mnt/x"}},
		},
	)

	mock := &msb.MockMsbClient{}
	vm := NewManager(&termio.Mock{})

	if _, err := vm.ApplyHomeAction(
		context.Background(),
		mock,
		state.Key{Slug: slug, Agent: "opencode"},
		oldVol,
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
		ActionReset,
		false,
		&termio.Mock{},
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	st, err := state.ReadState(state.Key{Slug: slug, Agent: "opencode"})
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.EnvState.Hash != "envhash" || st.SecretState.Hash != "sechash" ||
		st.NetworkState.Hash != "nethash" || st.MountState.Hash != "mounthash" {
		t.Errorf("fingerprints not preserved after home action: %+v", st)
	}
	if st.AgentVersion != "1.2.3" || st.AgentSource != "tool" || st.DockerSource != "user" ||
		len(st.OfferedVersions) != 1 || st.OfferedVersions[0] != "2.0.0" {
		t.Errorf("upgrade fields not preserved after home action: %+v", st)
	}
	if st.ImageDigest != "sha256:new" {
		t.Errorf("state ImageDigest = %q, want %q", st.ImageDigest, "sha256:new")
	}
}

func TestApplyHomeAction_PrefillFails_RemovesVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	slug := "myproj"
	oldVol := "agents-sandbox-home-myproj-old"
	seedHomeState(
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
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
		ActionReset,
		false,
		&termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(mock.RemovedVolumes) == 0 {
		t.Errorf("expected new volume to be cleaned up, got %v", mock.RemovedVolumes)
	}
}

func TestEnsureNewHome_PrefillFails_RemovesVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	mock := &msb.MockMsbClient{}
	mock.CreateSandboxErr = errors.New("prefill sandbox creation failed")
	var createdVolume string
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		createdVolume = name
		return &msb.MockVolumeHandle{Name_: name}, nil
	}
	vm := NewManager(&termio.Mock{})

	_, _, err := vm.EnsureNewHome(
		context.Background(),
		mock,
		state.Key{Slug: "testproj", Agent: "opencode"},
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:abc"},
		false,
		state.Fingerprints{},
		"",
		&termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "prefill") {
		t.Errorf("unexpected error: %v", err)
	}
	if len(mock.RemovedVolumes) != 1 || mock.RemovedVolumes[0] != createdVolume {
		t.Errorf("expected created volume %q to be removed, got %v", createdVolume, mock.RemovedVolumes)
	}
}

// TestEnsureNewHome_StateWriteFails covers #428: the state write is the record
// of the volume a run is about to use, so a failed write must surface an error
// and remove the uncommitted volume rather than warn and report success. A
// post-rename durability failure is the exception — the installed state already
// names the volume, so it must be kept.
func TestEnsureNewHome_StateWriteFails(t *testing.T) {
	tests := []struct {
		name       string
		writeErr   error
		wantRemove bool
	}{
		{
			name:       "pre-rename failure removes the uncommitted volume",
			writeErr:   errors.New("write state file: rename boom"),
			wantRemove: true,
		},
		{
			name:       "installed write keeps the volume",
			writeErr:   fmt.Errorf("write state file: %w: sync dir boom", atomicwrite.ErrInstalled),
			wantRemove: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			restore := recoverHomeState
			recoverHomeState = func(_ state.Key, _ func(*state.HomeState, bool) (bool, error)) error {
				return tt.writeErr
			}
			t.Cleanup(func() { recoverHomeState = restore })

			docker.WithNoopDockerMock(t)

			var createdVolume string
			mock := &msb.MockMsbClient{
				CreatedSandbox: &msb.MockSandbox{Name_: "prefill"},
				CreateVolumeFn: func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
					createdVolume = name
					return &msb.MockVolumeHandle{Name_: name}, nil
				},
			}
			ui := &termio.Mock{}

			_, _, err := NewManager(ui).EnsureNewHome(
				context.Background(),
				mock,
				state.Key{Slug: "statewritefail", Agent: "opencode"},
				image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:abc"},
				false, // the prefill succeeds; the state write is the failure under test
				state.Fingerprints{},
				"",
				ui,
			)
			if err == nil {
				t.Fatal("expected the state-write error to surface, got nil")
			}
			if !errors.Is(err, tt.writeErr) {
				t.Errorf("err = %v, want it to carry %v", err, tt.writeErr)
			}
			removed := len(mock.RemovedVolumes) == 1
			if removed != tt.wantRemove {
				t.Errorf("removed = %v (RemovedVolumes %v), want %v", removed, mock.RemovedVolumes, tt.wantRemove)
			}
			if tt.wantRemove && mock.RemovedVolumes[0] != createdVolume {
				t.Errorf("removed volume = %q, want the created volume %q", mock.RemovedVolumes[0], createdVolume)
			}
		})
	}
}

func TestEnsureNewHome_PrefillCancelled_RemovesVolumeWithLiveContext(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	ctx, cancel := context.WithCancel(context.Background())

	mock := &msb.MockMsbClient{}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return &msb.MockVolumeHandle{Name_: name}, nil
	}
	mock.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		cancel()
		return nil, errors.New("prefill sandbox creation failed")
	}
	var cleanupCtxErr error
	mock.RemoveVolumeFn = func(cleanupCtx context.Context, _ string) error {
		cleanupCtxErr = cleanupCtx.Err()
		return nil
	}
	vm := NewManager(&termio.Mock{})

	_, _, err := vm.EnsureNewHome(
		ctx,
		mock,
		state.Key{Slug: "testproj", Agent: "opencode"},
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:abc"},
		false,
		state.Fingerprints{},
		"",
		&termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if ctx.Err() == nil {
		t.Fatal("expected the caller context to be cancelled during prefill")
	}
	if len(mock.RemovedVolumes) != 1 {
		t.Fatalf("expected the created volume to be removed exactly once, got %v", mock.RemovedVolumes)
	}
	if cleanupCtxErr != nil {
		t.Errorf("cleanup received a cancelled context (%v); want a fresh live context", cleanupCtxErr)
	}
}

func TestEnsureNewHome_SkipVM_NoSandbox(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := &msb.MockMsbClient{}
	vm := NewManager(&termio.Mock{})

	volName, _, err := vm.EnsureNewHome(
		context.Background(),
		mock,
		state.Key{Slug: "testproj", Agent: "opencode"},
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:abc"},
		true, // skipVM
		state.Fingerprints{},
		"",
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(volName, "testproj-") {
		t.Errorf("expected home volume prefix, got %q", volName)
	}
	if len(mock.CreatedSandboxes) != 0 {
		t.Errorf("expected no sandboxes in dry-run-vm, got %d", len(mock.CreatedSandboxes))
	}
}

// TestEnsureNewHome_SkipVM_CreatesNothing pins #429: a skip-VM run cannot
// prefill, so it must neither create the volume nor record it in the state;
// otherwise a later real run reuses a never-prefilled home.
func TestEnsureNewHome_SkipVM_CreatesNothing(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := &msb.MockMsbClient{
		CreateVolumeFn: func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			t.Errorf("skip-VM run created volume %q", name)
			return &msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	k := state.Key{Slug: "testproj", Agent: "opencode"}
	ui := &termio.Mock{}

	volName, hs, err := NewManager(ui).EnsureNewHome(
		context.Background(),
		mock,
		k,
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:abc"},
		true, // skipVM
		state.Fingerprints{},
		"",
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if volName != HomeVolumeName(k) || hs.HomeVolume != volName {
		t.Errorf("volName = %q, hs.HomeVolume = %q, want %q", volName, hs.HomeVolume, HomeVolumeName(k))
	}
	if _, err := state.ReadState(k); !errors.Is(err, state.ErrStateNotFound) {
		t.Errorf("ReadState err = %v, want ErrStateNotFound (no state written under skip-VM)", err)
	}
	if len(ui.InfoCalls) != 1 ||
		!strings.Contains(ui.InfoCalls[0], "would create and prefill home volume "+strconv.Quote(volName)) {
		t.Errorf("InfoCalls = %v, want the deferred create-and-prefill notice for %q", ui.InfoCalls, volName)
	}
}

// TestResolveHomeVolume_SkipVMThenRealRun_Prefills is the #429 scenario: a
// --dry-run-vm run on a new project followed by a real run must prefill the
// home volume on the real run and only then record it.
func TestResolveHomeVolume_SkipVMThenRealRun_Prefills(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	var createdVolumes []string
	mock := &msb.MockMsbClient{
		CreatedSandbox: &msb.MockSandbox{Name_: "prefill"},
		CreateVolumeFn: func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			createdVolumes = append(createdVolumes, name)
			return &msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	k := state.Key{Slug: "testproj", Agent: "opencode"}
	info := image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:abc"}
	ui := &termio.Mock{}
	vm := NewManager(ui)

	if _, _, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		k,
		info,
		true,
		state.Fingerprints{},
		"",
		ui,
	); err != nil {
		t.Fatalf("skip-VM run: %v", err)
	}
	volName, _, err := vm.ResolveHomeVolume(context.Background(), mock, k, info, false, state.Fingerprints{}, "", ui)
	if err != nil {
		t.Fatalf("real run: %v", err)
	}

	if len(createdVolumes) != 1 || createdVolumes[0] != volName {
		t.Errorf("created volumes = %v, want exactly %q created by the real run", createdVolumes, volName)
	}
	if len(mock.CreatedSandboxes) != 1 {
		t.Errorf("prefill sandboxes = %d, want 1 (the real run must prefill)", len(mock.CreatedSandboxes))
	}
	onDisk, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if onDisk.HomeVolume != volName {
		t.Errorf("state HomeVolume = %q, want %q", onDisk.HomeVolume, volName)
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

// seedAdoptionClaim records a legacy identity's adoption claim (#144) through the
// locked root-state transaction, the way an adopting checkout does.
func seedAdoptionClaim(t *testing.T, legacySlug, agent, checkoutSlug, volume string) {
	t.Helper()
	if _, err := state.WithRootState(true, func(txn *state.RootTxn) error {
		txn.State().ClaimAdoptedHomeVolume(legacySlug, agent, checkoutSlug, volume)
		return txn.Commit()
	}); err != nil {
		t.Fatalf("seed adoption claim: %v", err)
	}
}

// TestResolveHomeVolume_ClearsAdoptionClaim covers pruning the ownership record:
// once recovery folds the preserved volume into the project's own state, the
// claim that guarded it is spent and must be retired.
func TestResolveHomeVolume_ClearsAdoptionClaim(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const recovered = "agents-sandbox-home-widget-aaa-opencode-9"
	key := state.Key{Slug: "adoptedwidget", Agent: "opencode"}
	seedHomeState(key, state.HomeState{RecoveredHomeVolume: recovered})
	seedAdoptionClaim(t, "widget-aaa", "opencode", key.Slug, recovered)

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		if name == recovered {
			return msb.MockVolumeHandle{Name_: name}, nil
		}
		var vh msb.VolumeHandle
		return vh, volumeNotFoundError(name)
	}

	ui := &termio.Mock{}
	vm := NewManager(ui)
	if _, _, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		key,
		image.ImageInfo{Digest: "sha256:new"},
		false,
		state.Fingerprints{},
		"",
		ui,
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	root, err := state.ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	if _, ok := root.AdoptedHomeVolume("widget-aaa", "opencode"); ok {
		t.Errorf("adoption claim survived recovery: %+v", root.AdoptedHomeVolumes)
	}
}

// TestResolveHomeVolume_AdoptionClaimClearErrorWarns covers the best-effort
// clear: a spent claim that cannot be retired (an unreadable root state document)
// only warns, because the recovery itself already succeeded.
func TestResolveHomeVolume_AdoptionClaimClearErrorWarns(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const recovered = "agents-sandbox-home-widget-aaa-opencode-9"
	key := state.Key{Slug: "adoptedwidget", Agent: "opencode"}
	seedHomeState(key, state.HomeState{RecoveredHomeVolume: recovered})
	seedAdoptionClaim(t, "widget-aaa", "opencode", key.Slug, recovered)

	// Corrupt the root state document after seeding the claim, so clearing the
	// claim's transaction fails to read it.
	rootStatePath := filepath.Join(configpaths.Get().UserStateDir(), "state.yaml")
	if err := os.WriteFile(rootStatePath, []byte("{ corrupt: yaml: ["), 0o600); err != nil {
		t.Fatal(err)
	}

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		if name == recovered {
			return msb.MockVolumeHandle{Name_: name}, nil
		}
		var vh msb.VolumeHandle
		return vh, volumeNotFoundError(name)
	}

	ui := &termio.Mock{}
	vm := NewManager(ui)
	if _, _, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		key,
		image.ImageInfo{Digest: "sha256:new"},
		false,
		state.Fingerprints{},
		"",
		ui,
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(strings.Join(ui.WarnCalls, "\n"), "failed to clear the adoption claim") {
		t.Errorf("expected a warning about the unretired adoption claim, got %v", ui.WarnCalls)
	}
}
