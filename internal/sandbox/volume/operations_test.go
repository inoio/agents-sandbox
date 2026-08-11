package volume

import (
	"context"
	"errors"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/state"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

func TestVolumeOps_DryRun(t *testing.T) {
	tests := []struct {
		name string
		run  func(ctx context.Context, slug, imageTag string, rmOld, dryRun bool, ui *termio.Mock) error
		want []string
	}{
		{
			name: "migrate",
			run: func(ctx context.Context, slug, imageTag string, rmOld, dryRun bool, ui *termio.Mock) error {
				return CmdMigrate(ctx, slug, "", imageTag, rmOld, dryRun, ui)
			},
			want: []string{"dry-run: Would create volume", "old-vol"},
		},
		{
			name: "reset",
			run: func(ctx context.Context, slug, imageTag string, rmOld, dryRun bool, ui *termio.Mock) error {
				return CmdReset(ctx, slug, "", imageTag, rmOld, dryRun, ui)
			},
			want: []string{"dry-run: Would create fresh volume", "old-vol"},
		},
		{
			name: "edit",
			run: func(ctx context.Context, slug, imageTag string, rmOld, dryRun bool, ui *termio.Mock) error {
				return CmdEdit(ctx, slug, "", imageTag, rmOld, dryRun, ui)
			},
			want: []string{"dry-run: Would create volume", "alongside", "old-vol"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state.SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

			slug := "testproj-aBc1234D"
			state.WriteState(slug, state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"})

			mock := &msb.MockMsbClient{}
			msb.WithMsbMock(t, mock)

			ui := &termio.Mock{}
			err := tt.run(context.Background(), slug, "img-tag", false, true, ui)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(ui.InfoCalls) != 1 {
				t.Fatalf("expected 1 Info call, got %d: %v", len(ui.InfoCalls), ui.InfoCalls)
			}
			for _, frag := range tt.want {
				if !strings.Contains(ui.InfoCalls[0], frag) {
					t.Errorf("message %q missing %q", ui.InfoCalls[0], frag)
				}
			}
		})
	}
}

func TestCmdReset_Success(t *testing.T) {
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	slug := "testproj-aBc1234D"
	oldVol := "opencode-msb-home-" + slug + "-old"
	state.WriteState(slug, state.HomeState{HomeVolume: oldVol, ImageDigest: "sha256:old"})

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

	st, err := state.ReadState(slug)
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
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

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
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	slug := "testproj-aBc1234D"
	state.WriteState(slug, state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"})

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
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	slug := "testproj-aBc1234D"
	state.WriteState(slug, state.HomeState{HomeVolume: "from-state-vol", ImageDigest: "sha256:abc"})

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
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	slug := "testproj-aBc1234D"
	state.WriteState(slug, state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"})

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
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	slug := "testproj-aBc1234D"
	state.WriteState(slug, state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"})

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
