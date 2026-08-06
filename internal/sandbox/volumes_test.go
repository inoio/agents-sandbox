package sandbox

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
)

func TestHomeVolumeName(t *testing.T) {
	got := HomeVolumeName("myproj-aBc1234D", "")
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
	got := HomeVolumeName("myproj-aBc1234D", "sha256:abc123def456")
	if !strings.HasPrefix(got, "opencode-msb-home-myproj-aBc1234D-") {
		t.Errorf("unexpected name format: %q", got)
	}
}

func TestHomeVolumeNameTimestamp(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	got := HomeVolumeName("myproject", "")
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

func TestHomeVolumeNameDigestIgnored(t *testing.T) {
	got1 := HomeVolumeName("proj", "sha256:abc123")
	got2 := HomeVolumeName("proj", "")
	got3 := HomeVolumeName("proj", "different")
	if !strings.HasPrefix(got1, "opencode-msb-home-proj-") {
		t.Errorf("got1 prefix wrong: %q", got1)
	}
	if !strings.HasPrefix(got2, "opencode-msb-home-proj-") {
		t.Errorf("got2 prefix wrong: %q", got2)
	}
	if !strings.HasPrefix(got3, "opencode-msb-home-proj-") {
		t.Errorf("got3 prefix wrong: %q", got3)
	}
}

func TestNewVolumeManager(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	vm := NewVolumeManager(&testUI)
	if vm.ui == nil {
		t.Error("expected ui to be set")
	}
}

func TestPrefillVolumeRunsCopyCommand(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	ui := &testUI
	client := &MockMsbClient{}
	vm := NewVolumeManager(ui)

	err := vm.prefillVolume(
		context.Background(),
		client,
		"myproject",
		"test-home-vol",
		"opencode-msb/runner-test:latest",
		ui,
	)
	if err != nil {
		t.Fatalf("prefillVolume failed: %v", err)
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
	vm := NewVolumeManager(&ui)
	action := vm.ResolveHomeAction(&ui, "same-digest", "same-digest")
	if action != actionKeep {
		t.Errorf("expected actionKeep for matching digests, got %q", action)
	}
}

func TestResolveHomeAction_DifferentDigestInNonInteractiveReturnsKeep(t *testing.T) {
	ui := testutil.TermUIMock(t)
	ui.IsInteractiveResult = false
	vm := NewVolumeManager(&ui)
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
	vm := NewVolumeManager(ui)
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
	vm := NewVolumeManager(ui)
	action := vm.ResolveHomeAction(ui, "old", "new")
	if action != actionQuit {
		t.Errorf("expected actionQuit, got %q", action)
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
	old := stateDirSuffix
	defer func() { stateDirSuffix = old }()
	stateDirSuffix = t.TempDir() + "/opencode-msb"

	mock := &MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (VolumeHandle, error) {
		return MockVolumeHandle{Name_: name}, nil
	}

	WriteState("myproj", HomeState{
		HomeVolume:  "opencode-msb-home-myproj-20260806T143022",
		ImageDigest: "sha256:abc",
	})

	vm := NewVolumeManager(&termio.Mock{})
	volName, state, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		"myproj",
		"sha256:abc",
		"",
		RunOptions{},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if volName != "opencode-msb-home-myproj-20260806T143022" {
		t.Errorf("volume = %q, want %q", volName, "opencode-msb-home-myproj-20260806T143022")
	}
	if state.ImageDigest != "sha256:abc" {
		t.Errorf("digest = %q, want %q", state.ImageDigest, "sha256:abc")
	}
}

func TestResolveHomeVolume_NoStateFile(t *testing.T) {
	old := stateDirSuffix
	defer func() { stateDirSuffix = old }()
	stateDirSuffix = t.TempDir() + "/opencode-msb"

	mock := &MockMsbClient{}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (VolumeHandle, error) {
		return MockVolumeHandle{Name_: name}, nil
	}

	vm := NewVolumeManager(&termio.Mock{})
	volName, state, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		"testproj",
		"sha256:def",
		"",
		RunOptions{},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(volName, "opencode-msb-home-testproj-") {
		t.Errorf("volume = %q, expected prefix %q", volName, "opencode-msb-home-testproj-")
	}
	if state.ImageDigest != "sha256:def" {
		t.Errorf("digest = %q, want %q", state.ImageDigest, "sha256:def")
	}
}
