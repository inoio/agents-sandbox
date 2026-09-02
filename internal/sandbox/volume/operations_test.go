package volume

import (
	"context"
	"errors"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	"github.com/inoio/opencode-sandbox/internal/termio"
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
				return CmdMigrate(ctx, slug, "", imageTag, "sha256:abc", rmOld, dryRun, ui)
			},
			want: []string{"dry-run: Would create volume", "old-vol"},
		},
		{
			name: "reset",
			run: func(ctx context.Context, slug, imageTag string, rmOld, dryRun bool, ui *termio.Mock) error {
				return CmdReset(ctx, slug, "", imageTag, "sha256:abc", rmOld, dryRun, ui)
			},
			want: []string{"dry-run: Would create fresh volume", "old-vol"},
		},
		{
			name: "edit",
			run: func(ctx context.Context, slug, imageTag string, rmOld, dryRun bool, ui *termio.Mock) error {
				return CmdEdit(ctx, slug, "", imageTag, "sha256:abc", rmOld, dryRun, ui)
			},
			want: []string{"dry-run: Would create volume", "alongside", "old-vol"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ui := setupVolumeOpsFixtures(t)

			slug := "testproj-aBc1234D"
			state.WriteState(slug, state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"})

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
	_, ui := setupVolumeOpsFixtures(t)

	slug := "testproj-aBc1234D"
	oldVol := "opencode-sandbox-home-" + slug + "-old"
	state.WriteState(slug, state.HomeState{HomeVolume: oldVol, ImageDigest: "sha256:old"})
	err := CmdReset(context.Background(), slug, "", "img-tag", "sha256:new", false, false, ui)
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

func TestVolumeOp_RecordsCurrentImageDigest(t *testing.T) {
	_, ui := setupVolumeOpsFixtures(t)

	slug := "testproj-aBc1234D"
	oldVol := "opencode-sandbox-home-" + slug + "-old"
	state.WriteState(slug, state.HomeState{HomeVolume: oldVol, ImageDigest: "sha256:old"})
	err := CmdReset(context.Background(), slug, "", "img-tag", "sha256:new", false, false, ui)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	st, err := state.ReadState(slug)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.ImageDigest != "sha256:new" {
		t.Errorf(
			"ImageDigest = %q, want %q (volume op must record the current image digest)",
			st.ImageDigest,
			"sha256:new",
		)
	}
}

func TestVolumeOp_PreservesEnvSecretFingerprints(t *testing.T) {
	_, ui := setupVolumeOpsFixtures(t)

	slug := "testproj-aBc1234D"
	oldVol := "opencode-sandbox-home-" + slug + "-old"
	oldState := state.HomeState{
		HomeVolume:  oldVol,
		ImageDigest: "sha256:old",
		EnvState:    state.EnvState{Hash: "envhash", Names: []string{"FOO"}},
		SecretState: state.SecretState{Hash: "sechash", Names: []string{"TOKEN"}},
	}
	if err := state.WriteState(slug, oldState); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	err := CmdReset(context.Background(), slug, "", "img-tag", "sha256:new", false, false, ui)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	st, err := state.ReadState(slug)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.EnvState.Hash != "envhash" || len(st.EnvState.Names) != 1 || st.EnvState.Names[0] != "FOO" {
		t.Errorf("EnvState not preserved after volume op: %+v", st.EnvState)
	}
	if st.SecretState.Hash != "sechash" || len(st.SecretState.Names) != 1 || st.SecretState.Names[0] != "TOKEN" {
		t.Errorf("SecretState not preserved after volume op: %+v", st.SecretState)
	}
}

func TestCmdMigrate_NoStateFile(t *testing.T) {
	_, ui := setupVolumeOpsFixtures(t)
	err := CmdMigrate(context.Background(), "noproject", "", "img-tag", "sha256:new", false, false, ui)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no state file found") {
		t.Errorf("expected 'no state file found' error, got: %v", err)
	}
}

func TestVolumeOp_CreateVolumeFails(t *testing.T) {
	mock, ui := setupVolumeOpsFixtures(t)

	slug := "testproj-aBc1234D"
	state.WriteState(slug, state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"})
	mock.CreateVolumeFn = func(_ context.Context, _ string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return nil, errors.New("create failed")
	}
	err := CmdReset(context.Background(), slug, "", "img-tag", "sha256:new", false, false, ui)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "create") || !strings.Contains(err.Error(), "failed") {
		t.Errorf("expected volume creation error, got: %v", err)
	}
}

func TestCmdReset_WithExplicitOldVolume(t *testing.T) {
	_, ui := setupVolumeOpsFixtures(t)

	slug := "testproj-aBc1234D"
	state.WriteState(slug, state.HomeState{HomeVolume: "from-state-vol", ImageDigest: "sha256:abc"})
	err := CmdReset(context.Background(), slug, "explicit-vol", "img-tag", "sha256:new", false, true, ui)
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
	mock, ui := setupVolumeOpsFixtures(t)

	slug := "testproj-aBc1234D"
	state.WriteState(slug, state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"})
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{Name_: "opencode-sandbox-vm-testproj-aBc1234D", Status_: msbSdk.SandboxStatusRunning},
	}
	err := CmdReset(context.Background(), slug, "", "img-tag", "sha256:new", false, false, ui)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "VM still running") {
		t.Errorf("expected VM still running error, got: %v", err)
	}
}

func TestVolumeOp_MainFails_RemovesNewVolume(t *testing.T) {
	mock, ui := setupVolumeOpsFixtures(t)

	slug := "testproj-aBc1234D"
	state.WriteState(slug, state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"})

	var createdVol string
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
	err := CmdMigrate(context.Background(), slug, "", "img-tag", "sha256:new", false, false, ui)
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

func TestCmdEdit_DryRun(t *testing.T) {
	_, ui := setupVolumeOpsFixtures(t)

	slug := "testproj-aBc1234D"
	state.WriteState(slug, state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"})

	err := CmdEdit(context.Background(), slug, "", "img-tag", "sha256:abc", false, true, ui)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ui.InfoCalls) != 1 {
		t.Fatalf("expected 1 Info call, got %d: %v", len(ui.InfoCalls), ui.InfoCalls)
	}
	if !strings.Contains(ui.InfoCalls[0], "dry-run") {
		t.Errorf("unexpected dry-run message: %q", ui.InfoCalls[0])
	}
	if !strings.Contains(ui.InfoCalls[0], "alongside") {
		t.Errorf("expected 'alongside' in message: %q", ui.InfoCalls[0])
	}
}

func TestCmdEdit_MainPath(t *testing.T) {
	mock, ui := setupVolumeOpsFixtures(t)

	slug := "testproj-aBc1234D"
	state.WriteState(slug, state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"})

	mock.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return msb.NewMockSandbox(msb.SandboxOpts{}), nil
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return &msb.MockVolumeHandle{Name_: name}, nil
	}

	err := CmdEdit(context.Background(), slug, "", "img-tag", "sha256:abc", false, false, ui)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have created the home volume
	if len(mock.CreatedSandboxes) == 0 {
		t.Error("expected sandbox creation for edit")
	}
}

func TestCmdEdit_MainFails_RemovesVolume(t *testing.T) {
	mock, ui := setupVolumeOpsFixtures(t)

	slug := "testproj-aBc1234D"
	state.WriteState(slug, state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"})

	var createdVol string
	var sandboxCount int
	mock.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		sandboxCount++
		if sandboxCount == 1 {
			// First call is from PrefillVolume — succeed
			return msb.NewMockSandbox(msb.SandboxOpts{}), nil
		}
		// Second call is from CmdEdit main — fail
		return nil, errors.New("create edit sandbox failed")
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		createdVol = name
		return &msb.MockVolumeHandle{Name_: name}, nil
	}

	err := CmdEdit(context.Background(), slug, "", "img-tag", "sha256:abc", false, false, ui)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "create edit sandbox") {
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
		t.Errorf("expected new volume %q to be removed on error; removed=%v", createdVol, mock.RemovedVolumes)
	}
}

func TestCmdEdit_AttachError_Warns(t *testing.T) {
	mock, ui := setupVolumeOpsFixtures(t)

	slug := "testproj-aBc1234D"
	state.WriteState(slug, state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"})

	mock.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return msb.NewMockSandbox(msb.SandboxOpts{
			AttachErr: errors.New("attach failed"),
		}), nil
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return &msb.MockVolumeHandle{Name_: name}, nil
	}

	err := CmdEdit(context.Background(), slug, "", "img-tag", "sha256:abc", false, false, ui)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// CmdEdit should return nil even if attach fails (it only warns)
	var foundWarn bool
	for _, call := range ui.WarnCalls {
		if strings.Contains(call, "shell exited") {
			foundWarn = true
		}
	}
	if !foundWarn {
		t.Errorf("expected warning about shell error, got: %v", ui.WarnCalls)
	}
}

func TestCmdMigrate_NonDryRun_Success(t *testing.T) {
	mock, ui := setupVolumeOpsFixtures(t)

	slug := "testproj-aBc1234D"
	state.WriteState(slug, state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:old"})

	mock.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return msb.NewMockSandbox(msb.SandboxOpts{
			ExecOut: map[string]msb.ShellResult{
				"sh -c cp -a /home/dev/. /mnt/home/ && chown -R dev:dev /mnt/home": msb.NewTestResult(
					true,
					0,
					"",
					"",
					nil,
				),
				"sh -c cp -a /src/. /dst/ && chown -R dev:dev /dst": msb.NewTestResult(
					true,
					0,
					"",
					"",
					nil,
				),
			},
		}), nil
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return &msb.MockVolumeHandle{Name_: name}, nil
	}

	err := CmdMigrate(context.Background(), slug, "", "img-tag", "sha256:new", false, false, ui)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.CreatedSandboxes) != 2 {
		t.Errorf("expected 2 sandboxes (prefill + copy), got %d", len(mock.CreatedSandboxes))
	}

	st, err := state.ReadState(slug)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.HomeVolume == "" {
		t.Error("expected HomeVolume to be set in new state")
	}
	if st.ImageDigest != "sha256:new" {
		t.Errorf("ImageDigest = %q, want %q", st.ImageDigest, "sha256:new")
	}
}

func TestCmdReset_RemOldVolume(t *testing.T) {
	mock, ui := setupVolumeOpsFixtures(t)

	slug := "testproj-aBc1234D"
	oldVol := "opencode-sandbox-home-" + slug + "-old"
	state.WriteState(slug, state.HomeState{HomeVolume: oldVol, ImageDigest: "sha256:old"})

	mock.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return msb.NewMockSandbox(msb.SandboxOpts{}), nil
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return &msb.MockVolumeHandle{Name_: name}, nil
	}

	err := CmdReset(context.Background(), slug, "", "img-tag", "sha256:new", true, false, ui)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// With rmOld=true, the old volume should be removed
	var foundOld bool
	for _, v := range mock.RemovedVolumes {
		if v == oldVol {
			foundOld = true
		}
	}
	if !foundOld {
		t.Errorf("expected old volume %q to be removed, got %v", oldVol, mock.RemovedVolumes)
	}
}

func TestVolumeOp_ExplicitOldVolume(t *testing.T) {
	mock, ui := setupVolumeOpsFixtures(t)

	slug := "testproj-aBc1234D"
	state.WriteState(slug, state.HomeState{HomeVolume: "state-vol", ImageDigest: "sha256:abc"})

	mock.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return msb.NewMockSandbox(msb.SandboxOpts{}), nil
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return &msb.MockVolumeHandle{Name_: name}, nil
	}

	err := CmdReset(context.Background(), slug, "explicit-vol", "img-tag", "sha256:abc", false, false, ui)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should use explicit volume, not state volume
	if len(ui.InfoCalls) == 0 {
		t.Fatal("expected info calls")
	}
	// The reset message should reference the new volume, not the explicit one (reset doesn't copy)
}

func TestResolveHomeVolume_VolumeNotFound_Warns(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	slug := "myproj"
	state.WriteState(slug, state.HomeState{HomeVolume: "opencode-sandbox-home-myproj-old", ImageDigest: "sha256:abc"})

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, _ string) (msb.VolumeHandle, error) {
		var vh msb.VolumeHandle
		return vh, errors.New("volume not found")
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	mockUI := &termio.Mock{}
	vm := NewManager(mockUI)
	_, _, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		slug,
		"sha256:def",
		"latest",
		false,
		mockUI,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mockUI.WarnCalls) == 0 {
		t.Error("expected warning about volume not found")
	}
}
