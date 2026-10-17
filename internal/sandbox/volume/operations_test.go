package volume

import (
	"context"
	"errors"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
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
				return CmdMigrate(
					ctx,
					state.Key{Slug: slug, Agent: "opencode"},
					"",
					image.ImageInfo{Tag: imageTag, Digest: "sha256:abc"},
					rmOld,
					dryRun,
					ui,
				)
			},
			want: []string{"dry-run: Would create volume", "old-vol"},
		},
		{
			name: "reset",
			run: func(ctx context.Context, slug, imageTag string, rmOld, dryRun bool, ui *termio.Mock) error {
				return CmdReset(
					ctx,
					state.Key{Slug: slug, Agent: "opencode"},
					"",
					image.ImageInfo{Tag: imageTag, Digest: "sha256:abc"},
					rmOld,
					dryRun,
					ui,
				)
			},
			want: []string{"dry-run: Would create fresh volume", "old-vol"},
		},
		{
			name: "edit",
			run: func(ctx context.Context, slug, imageTag string, rmOld, dryRun bool, ui *termio.Mock) error {
				return CmdEdit(
					ctx,
					state.Key{Slug: slug, Agent: "opencode"},
					"",
					image.ImageInfo{Tag: imageTag, Digest: "sha256:abc"},
					rmOld,
					dryRun,
					ui,
				)
			},
			want: []string{"dry-run: Would create volume", "alongside", "old-vol"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ui := setupVolumeOpsFixtures(t)

			slug := "testproj-aBc1234D"
			seedHomeState(
				state.Key{Slug: slug, Agent: "opencode"},
				state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"},
			)

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
	oldVol := "agents-sandbox-home-" + slug + "-old"
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: oldVol, ImageDigest: "sha256:old"},
	)
	err := CmdReset(
		context.Background(),
		state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{Tag: "img-tag", Digest: "sha256:new"},
		false,
		false,
		ui,
	)
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

	st, err := state.ReadState(state.Key{Slug: slug, Agent: "opencode"})
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
	oldVol := "agents-sandbox-home-" + slug + "-old"
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: oldVol, ImageDigest: "sha256:old"},
	)
	err := CmdReset(
		context.Background(),
		state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{Tag: "img-tag", Digest: "sha256:new"},
		false,
		false,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	st, err := state.ReadState(state.Key{Slug: slug, Agent: "opencode"})
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
	oldVol := "agents-sandbox-home-" + slug + "-old"
	oldState := state.HomeState{
		HomeVolume:      oldVol,
		ImageDigest:     "sha256:old",
		AgentVersion:    "1.2.3",
		AgentSource:     "tool",
		DockerSource:    "user",
		OfferedVersions: []string{"2.0.0"},
		EnvState:        state.EnvState{Hash: "envhash", Names: []string{"FOO"}},
		SecretState:     state.SecretState{Hash: "sechash", Names: []string{"TOKEN"}},
	}
	if err := seedHomeState(state.Key{Slug: slug, Agent: "opencode"}, oldState); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	err := CmdReset(
		context.Background(),
		state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{Tag: "img-tag", Digest: "sha256:new"},
		false,
		false,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	st, err := state.ReadState(state.Key{Slug: slug, Agent: "opencode"})
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.EnvState.Hash != "envhash" || len(st.EnvState.Names) != 1 || st.EnvState.Names[0] != "FOO" {
		t.Errorf("EnvState not preserved after volume op: %+v", st.EnvState)
	}
	if st.SecretState.Hash != "sechash" || len(st.SecretState.Names) != 1 || st.SecretState.Names[0] != "TOKEN" {
		t.Errorf("SecretState not preserved after volume op: %+v", st.SecretState)
	}
	if st.AgentVersion != "1.2.3" || st.AgentSource != "tool" || st.DockerSource != "user" ||
		len(st.OfferedVersions) != 1 || st.OfferedVersions[0] != "2.0.0" {
		t.Errorf("upgrade fields not preserved after volume op: %+v", st)
	}
}

func TestCmdMigrate_NoStateFile(t *testing.T) {
	_, ui := setupVolumeOpsFixtures(t)
	err := CmdMigrate(
		context.Background(),
		state.Key{Slug: "noproject", Agent: "opencode"},
		"",
		image.ImageInfo{Tag: "img-tag", Digest: "sha256:new"},
		false,
		false,
		ui,
	)
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
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"},
	)
	mock.CreateVolumeFn = func(_ context.Context, _ string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return nil, errors.New("create failed")
	}
	err := CmdReset(
		context.Background(),
		state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{Tag: "img-tag", Digest: "sha256:new"},
		false,
		false,
		ui,
	)
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
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "from-state-vol", ImageDigest: "sha256:abc"},
	)
	err := CmdReset(
		context.Background(),
		state.Key{Slug: slug, Agent: "opencode"},
		"explicit-vol",
		image.ImageInfo{Tag: "img-tag", Digest: "sha256:new"},
		false,
		true,
		ui,
	)
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
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"},
	)
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{Name_: "agents-sandbox-vm-testproj-aBc1234D", Status_: msbSdk.SandboxStatusRunning},
	}
	err := CmdReset(
		context.Background(),
		state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{Tag: "img-tag", Digest: "sha256:new"},
		false,
		false,
		ui,
	)
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
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"},
	)

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
	err := CmdMigrate(
		context.Background(),
		state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{Tag: "img-tag", Digest: "sha256:new"},
		false,
		false,
		ui,
	)
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
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"},
	)

	err := CmdEdit(
		context.Background(),
		state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{Tag: "img-tag", Digest: "sha256:abc"},
		false,
		true,
		ui,
	)
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
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"},
	)

	mock.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return msb.NewMockSandbox(msb.SandboxOpts{}), nil
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return &msb.MockVolumeHandle{Name_: name}, nil
	}

	err := CmdEdit(
		context.Background(),
		state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{Tag: "img-tag", Digest: "sha256:abc"},
		false,
		false,
		ui,
	)
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
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"},
	)

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

	err := CmdEdit(
		context.Background(),
		state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{Tag: "img-tag", Digest: "sha256:abc"},
		false,
		false,
		ui,
	)
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
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"},
	)

	mock.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return msb.NewMockSandbox(msb.SandboxOpts{
			AttachErr: errors.New("attach failed"),
		}), nil
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return &msb.MockVolumeHandle{Name_: name}, nil
	}

	err := CmdEdit(
		context.Background(),
		state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{Tag: "img-tag", Digest: "sha256:abc"},
		false,
		false,
		ui,
	)
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

// TestVolumeOpAbortsWhenHomeVolumeChangedConcurrently pins the compare-and-set
// guard: volumeOp snapshots the home volume, does its msb I/O outside the home
// lock, then commits under the lock. If another writer changed home_volume in
// the meantime, volumeOp must fail with a concurrent-change error and leave the
// other writer's value in place instead of clobbering it.
func TestVolumeOpAbortsWhenHomeVolumeChangedConcurrently(t *testing.T) {
	mock, ui := setupVolumeOpsFixtures(t)

	key := state.Key{Slug: "testproj-aBc1234D", Agent: "opencode"}
	seedHomeState(key, state.HomeState{HomeVolume: "vol-1", ImageDigest: "sha256:old"})

	var createdVol string
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		createdVol = name
		return &msb.MockVolumeHandle{Name_: name}, nil
	}

	err := volumeOp(
		context.Background(), key, "",
		image.ImageInfo{Tag: "img-tag", Digest: "sha256:new"},
		false, false,
		volCallbacks{
			dryRun: func(string) string { return "" },
			main: func(oldVolume, _ string) error {
				if oldVolume != "vol-1" {
					t.Fatalf("snapshot home volume = %q, want vol-1", oldVolume)
				}
				return state.WithHomeState(key, func(cur *state.HomeState, _ bool) (bool, error) {
					cur.HomeVolume = "vol-2"
					return true, nil
				})
			},
		},
		ui,
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
	if st.HomeVolume != "vol-2" {
		t.Errorf("HomeVolume = %q, want vol-2 (concurrent writer must not be clobbered)", st.HomeVolume)
	}
	if createdVol == "" {
		t.Fatal("expected the new volume to have been created before the CAS failure")
	}
	var cleanedUp bool
	for _, v := range mock.RemovedVolumes {
		if v == createdVol {
			cleanedUp = true
		}
	}
	if !cleanedUp {
		t.Errorf("expected uncommitted volume %q to be cleaned up; removed=%v", createdVol, mock.RemovedVolumes)
	}
}

func TestCmdMigrate_NonDryRun_Success(t *testing.T) {
	mock, ui := setupVolumeOpsFixtures(t)

	slug := "testproj-aBc1234D"
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:old"},
	)

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

	err := CmdMigrate(
		context.Background(),
		state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{Tag: "img-tag", Digest: "sha256:new"},
		false,
		false,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.CreatedSandboxes) != 2 {
		t.Errorf("expected 2 sandboxes (prefill + copy), got %d", len(mock.CreatedSandboxes))
	}

	st, err := state.ReadState(state.Key{Slug: slug, Agent: "opencode"})
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
	oldVol := "agents-sandbox-home-" + slug + "-old"
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: oldVol, ImageDigest: "sha256:old"},
	)

	mock.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return msb.NewMockSandbox(msb.SandboxOpts{}), nil
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return &msb.MockVolumeHandle{Name_: name}, nil
	}

	err := CmdReset(
		context.Background(),
		state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{Tag: "img-tag", Digest: "sha256:new"},
		true,
		false,
		ui,
	)
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
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "state-vol", ImageDigest: "sha256:abc"},
	)

	mock.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return msb.NewMockSandbox(msb.SandboxOpts{}), nil
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return &msb.MockVolumeHandle{Name_: name}, nil
	}

	err := CmdReset(
		context.Background(),
		state.Key{Slug: slug, Agent: "opencode"},
		"explicit-vol",
		image.ImageInfo{Tag: "img-tag", Digest: "sha256:abc"},
		false,
		false,
		ui,
	)
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
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "agents-sandbox-home-myproj-old", ImageDigest: "sha256:abc"},
	)

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
		state.Key{Slug: slug, Agent: "opencode"},
		image.ImageInfo{Tag: "latest", Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		"",
		mockUI,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mockUI.WarnCalls) == 0 {
		t.Error("expected warning about volume not found")
	}
}

// TestResolveHomeVolume_MissingRecordedVolumeStartsFresh pins the clean-break
// stale-state contract: after the private-home change, a project's recorded home
// volume no longer resolves. ResolveHomeVolume must detect the missing volume,
// create a fresh one, rewrite state to the new name, and return no error —
// rather than failing the session.
func TestResolveHomeVolume_MissingRecordedVolumeStartsFresh(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	key := state.Key{Slug: "stalehome", Agent: "opencode"}
	const recordedVolume = "agents-sandbox-home-stalehome-legacy"
	seedHomeState(key, state.HomeState{
		HomeVolume:  recordedVolume,
		ImageDigest: "sha256:legacy",
	})

	var createdName string
	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, _ string) (msb.VolumeHandle, error) {
		var vh msb.VolumeHandle
		return vh, errors.New("volume not found in private home")
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		createdName = name
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	ui := &termio.Mock{}
	vm := NewManager(ui)
	volName, st, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		key,
		image.ImageInfo{Tag: "latest", Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		"",
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if createdName == "" {
		t.Fatal("expected a fresh volume to be created")
	}
	if volName != createdName {
		t.Errorf("returned volume = %q, want created volume %q", volName, createdName)
	}
	if volName == recordedVolume {
		t.Errorf("returned volume = %q, must not reuse the stale recorded name", recordedVolume)
	}

	persisted, err := state.ReadState(key)
	if err != nil {
		t.Fatalf("state not rewritten: %v", err)
	}
	if persisted.HomeVolume != volName {
		t.Errorf("persisted home volume = %q, want %q", persisted.HomeVolume, volName)
	}
	if st.ImageDigest != "sha256:def" {
		t.Errorf("digest = %q, want %q", st.ImageDigest, "sha256:def")
	}
}
