package sandbox

import (
	"context"
	"errors"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

func TestCmdMigrate_DryRun(t *testing.T) {
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	slug := "testproj-aBc1234D"
	WriteState(slug, HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"})

	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)

	ui := &termio.Mock{}
	err := CmdMigrate(context.Background(), slug, "", "img-tag", false, true, ui)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ui.InfoCalls) != 1 {
		t.Fatalf("expected 1 Info call, got %d: %v", len(ui.InfoCalls), ui.InfoCalls)
	}
	if !strings.Contains(ui.InfoCalls[0], "dry-run: Would create volume") {
		t.Errorf("unexpected message: %q", ui.InfoCalls[0])
	}
	if !strings.Contains(ui.InfoCalls[0], "old-vol") {
		t.Errorf("expected message to contain old volume name: %q", ui.InfoCalls[0])
	}
}

func TestCmdReset_DryRun(t *testing.T) {
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	slug := "testproj-aBc1234D"
	WriteState(slug, HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"})

	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)

	ui := &termio.Mock{}
	err := CmdReset(context.Background(), slug, "", "img-tag", false, true, ui)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ui.InfoCalls) != 1 {
		t.Fatalf("expected 1 Info call, got %d: %v", len(ui.InfoCalls), ui.InfoCalls)
	}
	if !strings.Contains(ui.InfoCalls[0], "dry-run: Would create fresh volume") {
		t.Errorf("unexpected message: %q", ui.InfoCalls[0])
	}
	if !strings.Contains(ui.InfoCalls[0], "old-vol") {
		t.Errorf("expected message to contain old volume name: %q", ui.InfoCalls[0])
	}
}

func TestCmdEdit_DryRun(t *testing.T) {
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	slug := "testproj-aBc1234D"
	WriteState(slug, HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"})

	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)

	ui := &termio.Mock{}
	err := CmdEdit(context.Background(), slug, "", "img-tag", false, true, ui)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ui.InfoCalls) != 1 {
		t.Fatalf("expected 1 Info call, got %d: %v", len(ui.InfoCalls), ui.InfoCalls)
	}
	if !strings.Contains(ui.InfoCalls[0], "dry-run: Would create volume") {
		t.Errorf("unexpected message: %q", ui.InfoCalls[0])
	}
	if !strings.Contains(ui.InfoCalls[0], "alongside") {
		t.Errorf("expected message to contain alongside: %q", ui.InfoCalls[0])
	}
}

func TestCmdReset_Success(t *testing.T) {
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	slug := "testproj-aBc1234D"
	oldVol := "opencode-msb-home-" + slug + "-old"
	WriteState(slug, HomeState{HomeVolume: oldVol, ImageDigest: "sha256:old"})

	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)

	ui := &termio.Mock{}
	err := CmdReset(context.Background(), slug, "", "img-tag", false, false, ui)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var foundResetMsg bool
	for _, call := range ui.InfoCalls {
		if strings.Contains(call, "reset to new home volume") {
			foundResetMsg = true
		}
	}
	if !foundResetMsg {
		t.Errorf("expected reset message in InfoCalls: %v", ui.InfoCalls)
	}

	st, err := ReadState(slug)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st == nil {
		t.Fatal("expected state to be written")
	}
	if st.HomeVolume == "" {
		t.Error("expected HomeVolume to be set in new state")
	}
}

func TestCmdMigrate_NoStateFile(t *testing.T) {
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)

	ui := &termio.Mock{}
	err := CmdMigrate(context.Background(), "noproject", "", "img-tag", false, false, ui)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no state file found") {
		t.Errorf("expected 'no state file found' error, got: %v", err)
	}
}

func TestVolumeOp_CreateVolumeFails(t *testing.T) {
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	slug := "testproj-aBc1234D"
	WriteState(slug, HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"})

	mock := &msb.MockMsbClient{}
	mock.CreateVolumeFn = func(_ context.Context, _ string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return nil, errors.New("create failed")
	}
	msb.WithMsbMock(t, mock)

	ui := &termio.Mock{}
	err := CmdReset(context.Background(), slug, "", "img-tag", false, false, ui)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "create") || !strings.Contains(err.Error(), "failed") {
		t.Errorf("expected volume creation error, got: %v", err)
	}
}

func TestCmdReset_WithExplicitOldVolume(t *testing.T) {
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	slug := "testproj-aBc1234D"
	WriteState(slug, HomeState{HomeVolume: "from-state-vol", ImageDigest: "sha256:abc"})

	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)

	ui := &termio.Mock{}
	err := CmdReset(context.Background(), slug, "explicit-vol", "img-tag", false, true, ui)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// dry-run explicit volume: the log should contain "explicit-vol" as the source
	if len(ui.InfoCalls) != 1 {
		t.Fatalf("expected 1 Info call, got %d: %v", len(ui.InfoCalls), ui.InfoCalls)
	}
	if !strings.Contains(ui.InfoCalls[0], "explicit-vol") {
		t.Errorf("expected explicit volume name in message: %q", ui.InfoCalls[0])
	} else if strings.Contains(ui.InfoCalls[0], "from-state-vol") {
		t.Errorf("should use explicit volume, not state volume: %q", ui.InfoCalls[0])
	}
}

func TestVolumeOp_ActiveVM_ReturnsError(t *testing.T) {
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	slug := "testproj-aBc1234D"
	WriteState(slug, HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"})

	mock := &msb.MockMsbClient{}
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{Name_: "opencode-msb-vm-testproj-aBc1234D", Status_: msbSdk.SandboxStatusRunning},
	}
	msb.WithMsbMock(t, mock)

	ui := &termio.Mock{}
	err := CmdReset(context.Background(), slug, "", "img-tag", false, false, ui)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "session still running") {
		t.Errorf("expected session still running error, got: %v", err)
	}
}

func TestVolumeOp_MainFails_RemovesNewVolume(t *testing.T) {
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	slug := "testproj-aBc1234D"
	WriteState(slug, HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"})

	var createdVol string
	mock := &msb.MockMsbClient{}
	var sandboxCount int
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
	msb.WithMsbMock(t, mock)

	ui := &termio.Mock{}
	err := CmdMigrate(context.Background(), slug, "", "img-tag", false, false, ui)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "create copy sandbox") {
		t.Errorf("unexpected error: %v", err)
	}
	if createdVol == "" {
		t.Fatal("expected new volume to have been created")
	}
	var cleanedUp bool
	for _, v := range mock.RemovedVolumes {
		if v == createdVol {
			cleanedUp = true
		}
	}
	if !cleanedUp {
		t.Errorf("expected new volume %q to be removed on main failure; removed=%v", createdVol, mock.RemovedVolumes)
	}
}
