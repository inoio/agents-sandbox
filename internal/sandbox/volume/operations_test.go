package volume

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestCheckForActiveVMs_ListSandboxesError(t *testing.T) {
	mock, _ := setupVolumeOpsFixtures(t)
	mock.ListSandboxesErr = errors.New("boom")

	err := checkForActiveVMs(context.Background(), mock, state.Key{Slug: "someproj", Agent: "opencode"})
	if err == nil || !strings.Contains(err.Error(), "list sandboxes") {
		t.Fatalf("expected list sandboxes error, got: %v", err)
	}
}

func TestCheckForActiveVMs_IgnoresNonVMSandboxes(t *testing.T) {
	mock, _ := setupVolumeOpsFixtures(t)
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{Name_: "agents-sandbox-task-someproj", Status_: msbSdk.SandboxStatusRunning},
	}

	if err := checkForActiveVMs(
		context.Background(),
		mock,
		state.Key{Slug: "someproj", Agent: "opencode"},
	); err != nil {
		t.Fatalf("expected no error for non-VM sandbox, got: %v", err)
	}
}

func TestCheckForActiveVMs_MatchesPrefixFreeProjectVM(t *testing.T) {
	mock, _ := setupVolumeOpsFixtures(t)
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{Name_: "someproj-1mjusbm3wikhb0-opencode", Status_: msbSdk.SandboxStatusRunning},
	}

	err := checkForActiveVMs(context.Background(), mock, state.Key{Slug: "someproj-1mjusbm3wikhb0", Agent: "opencode"})
	if err == nil {
		t.Fatal("expected a running prefix-free project VM to block volume operations")
	}
}

func TestCheckForActiveVMs_IgnoresPrefixFreeTaskSandbox(t *testing.T) {
	mock, _ := setupVolumeOpsFixtures(t)
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{
			Name_:   "someproj-1mjusbm3wikhb0-opencode-1729000000000000000",
			Status_: msbSdk.SandboxStatusRunning,
		},
	}

	if err := checkForActiveVMs(
		context.Background(),
		mock,
		state.Key{Slug: "someproj-1mjusbm3wikhb0", Agent: "opencode"},
	); err != nil {
		t.Fatalf("a prefix-free task sandbox must not block volume operations, got: %v", err)
	}
}

// TestCheckForActiveVMs_UsesLabelsForTaskSandbox pins that a sandbox whose name
// looks like a project VM but whose labels mark it a task does not block volume
// operations: the label is authoritative over the name shape.
func TestCheckForActiveVMs_UsesLabelsForTaskSandbox(t *testing.T) {
	mock, _ := setupVolumeOpsFixtures(t)
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{
			Name_:   "someproj-1mjusbm3wikhb0-opencode",
			Status_: msbSdk.SandboxStatusRunning,
			Cfg: &msbSdk.SandboxConfig{
				Labels: map[string]string{naming.LabelType: naming.SandboxTypeTask},
			},
		},
	}

	if err := checkForActiveVMs(
		context.Background(),
		mock,
		state.Key{Slug: "someproj-1mjusbm3wikhb0", Agent: "opencode"},
	); err != nil {
		t.Fatalf("a label-identified task sandbox must not block volume operations, got: %v", err)
	}
}

func TestCheckForActiveVMs_OtherSlugVMIsIgnored(t *testing.T) {
	mock, _ := setupVolumeOpsFixtures(t)
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{Name_: "agents-sandbox-vm-otherproj", Status_: msbSdk.SandboxStatusRunning},
	}

	if err := checkForActiveVMs(
		context.Background(),
		mock,
		state.Key{Slug: "someproj", Agent: "opencode"},
	); err != nil {
		t.Fatalf("expected no error for a VM of another slug, got: %v", err)
	}
}

func TestCheckForActiveVMs_InactiveVMSameSlugIsIgnored(t *testing.T) {
	mock, _ := setupVolumeOpsFixtures(t)
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{Name_: "agents-sandbox-vm-someproj", Status_: msbSdk.SandboxStatusStopped},
	}

	if err := checkForActiveVMs(
		context.Background(),
		mock,
		state.Key{Slug: "someproj", Agent: "opencode"},
	); err != nil {
		t.Fatalf("expected no error for an inactive VM, got: %v", err)
	}
}

func TestCheckForActiveVMs_StartingVMSameSlugIsBlocked(t *testing.T) {
	mock, _ := setupVolumeOpsFixtures(t)
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{Name_: "agents-sandbox-vm-someproj", Status_: msbSdk.SandboxStatusStarting},
	}

	if err := checkForActiveVMs(
		context.Background(),
		mock,
		state.Key{Slug: "someproj", Agent: "opencode"},
	); err == nil {
		t.Fatal("expected a starting VM to block volume operations")
	}
}

func TestCheckForActiveVMs_UnknownVMSameSlugIsBlocked(t *testing.T) {
	mock, _ := setupVolumeOpsFixtures(t)
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{Name_: "agents-sandbox-vm-someproj", Status_: msbSdk.SandboxStatus("future-status")},
	}

	if err := checkForActiveVMs(
		context.Background(),
		mock,
		state.Key{Slug: "someproj", Agent: "opencode"},
	); err == nil {
		t.Fatal("expected an unknown VM status to block volume operations")
	}
}

func TestVolumeOp_MainFails_CleanupVolumeFailsWarns(t *testing.T) {
	mock, ui := setupVolumeOpsFixtures(t)
	slug := "testproj-aBc1234D"
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"},
	)

	var sandboxCount int
	mock.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		sandboxCount++
		if sandboxCount == 2 {
			return nil, errors.New("create copy sandbox failed")
		}
		return msb.NewMockSandbox(msb.SandboxOpts{}), nil
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return &msb.MockVolumeHandle{Name_: name}, nil
	}
	mock.RemoveVolumeFn = func(_ context.Context, _ string) error {
		return errors.New("volume busy")
	}

	err := CmdMigrate(context.Background(), docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
		false,
		false,
		ui,
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var warned bool
	for _, c := range ui.WarnCalls {
		if strings.Contains(c, "failed to remove new volume") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("expected cleanup warning, got: %v", ui.WarnCalls)
	}
}

func TestVolumeOps_DryRun(t *testing.T) {
	tests := []struct {
		name string
		run  func(ctx context.Context, slug, imageTag string, rmOld, dryRun bool, ui *termio.Mock) error
		want []string
	}{
		{
			name: "migrate",
			run: func(ctx context.Context, slug, imageTag string, rmOld, dryRun bool, ui *termio.Mock) error {
				return CmdMigrate(ctx, docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
					"",
					image.ImageInfo{DockerTag: imageTag, Ref: imageTag, Digest: "sha256:abc"},
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
				return CmdReset(ctx, docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
					"",
					image.ImageInfo{DockerTag: imageTag, Ref: imageTag, Digest: "sha256:abc"},
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
				return CmdEdit(ctx, docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
					"",
					image.ImageInfo{DockerTag: imageTag, Ref: imageTag, Digest: "sha256:abc"},
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

func TestCmdMigrate_NoStateFile(t *testing.T) {
	_, ui := setupVolumeOpsFixtures(t)
	err := CmdMigrate(context.Background(), docker.Get(), state.Key{Slug: "noproject", Agent: "opencode"},
		"",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
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
	err := CmdMigrate(context.Background(), docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
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

func TestCmdMigrate_NonDryRun_Success(t *testing.T) {
	mock, ui := setupVolumeOpsFixtures(t)
	resolutions := msb.WithCountingMsbMock(t, mock)

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

	err := CmdMigrate(context.Background(), docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
		false,
		false,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *resolutions != 1 {
		t.Errorf("msb.Get resolved %d times, want once per CmdMigrate", *resolutions)
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

func TestVolumeOp_ReadStateCorruptError(t *testing.T) {
	_, ui := setupVolumeOpsFixtures(t)
	slug := "corruptproj-read"
	statePath := filepath.Join(configpaths.Get().UserStateDir(), slug, "opencode", "state.yaml")
	if err := os.MkdirAll(statePath, 0o700); err != nil {
		t.Fatal(err)
	}

	err := CmdReset(context.Background(), docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
		false,
		false,
		ui,
	)
	if err == nil || !strings.Contains(err.Error(), "read state") {
		t.Fatalf("expected read state error, got: %v", err)
	}
}

func TestVolumeOp_NoVolumeToOperate(t *testing.T) {
	_, ui := setupVolumeOpsFixtures(t)
	slug := "novolproj"
	seedHomeState(state.Key{Slug: slug, Agent: "opencode"}, state.HomeState{})

	err := CmdReset(context.Background(), docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
		false,
		false,
		ui,
	)
	if err == nil || !strings.Contains(err.Error(), "no volume to operate") {
		t.Fatalf("expected no-volume error, got: %v", err)
	}
}

func TestVolumeOp_PrefillFails(t *testing.T) {
	mock, ui := setupVolumeOpsFixtures(t)
	slug := "testproj-aBc1234D"
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"},
	)
	mock.CreateSandboxErr = errors.New("prefill failed")

	err := CmdReset(context.Background(), docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
		false,
		false,
		ui,
	)
	if err == nil || !strings.Contains(err.Error(), "prefill new volume") {
		t.Fatalf("expected prefill error, got: %v", err)
	}
}

func TestVolumeOp_WriteStateFails_ReturnsError(t *testing.T) {
	mock, ui := setupVolumeOpsFixtures(t)
	slug := "testproj-aBc1234D"
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:old"},
	)

	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory does not block writes")
	}
	sdir := filepath.Join(configpaths.Get().UserStateDir(), slug, "opencode")
	// A read-only state dir makes the atomic temp-file creation fail, so the
	// state write fails while reads (needing only execute) still succeed.
	if err := os.Chmod(sdir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sdir, 0o700) })

	mock.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return msb.NewMockSandbox(msb.SandboxOpts{}), nil
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return &msb.MockVolumeHandle{Name_: name}, nil
	}

	err := CmdReset(context.Background(), docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
		false,
		false,
		ui,
	)
	if err == nil {
		t.Fatal("expected a state-write error, got nil")
	}
	if !strings.Contains(err.Error(), "write state") {
		t.Errorf("unexpected error: %v", err)
	}
	st, readErr := state.ReadState(state.Key{Slug: slug, Agent: "opencode"})
	if readErr != nil {
		t.Fatalf("ReadState: %v", readErr)
	}
	if st.HomeVolume != "old-vol" {
		t.Errorf("HomeVolume = %q, want the uncommitted write to leave old-vol in place", st.HomeVolume)
	}
}

func TestVolumeOp_RemoveOldFails_Warns(t *testing.T) {
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
	mock.RemoveVolumeFn = func(_ context.Context, name string) error {
		if name == oldVol {
			return errors.New("busy")
		}
		return nil
	}

	err := CmdReset(context.Background(), docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
		true,
		false,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var warned bool
	for _, c := range ui.WarnCalls {
		if strings.Contains(c, "failed to remove old volume") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("expected remove-old warning, got: %v", ui.WarnCalls)
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
	err := CmdReset(context.Background(), docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
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
	err := CmdReset(context.Background(), docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
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

	err := CmdReset(context.Background(), docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
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
	err := CmdReset(context.Background(), docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
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
	err := CmdReset(context.Background(), docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
		"explicit-vol",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
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
	err := CmdReset(context.Background(), docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
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

	err := CmdReset(context.Background(), docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
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

	err := CmdReset(context.Background(), docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
		"explicit-vol",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:abc"},
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

// TestVolumeOp_UsesRecoveredHomeVolumeAsSource covers the VM-only adoption marker
// (#144): a first volume op after adoption has only RecoveredHomeVolume set, so
// volumeOp must use it as the source volume instead of failing with "state has no
// home_volume set", and must clear the marker once it commits.
func TestVolumeOp_UsesRecoveredHomeVolumeAsSource(t *testing.T) {
	mock, ui := setupVolumeOpsFixtures(t)

	key := state.Key{Slug: "testproj-aBc1234D", Agent: "opencode"}
	const recovered = "agents-sandbox-home-widget-aaa-opencode-7"
	seedHomeState(key, state.HomeState{RecoveredHomeVolume: recovered})
	seedAdoptionClaim(t, "widget-aaa", key.Slug, recovered)

	err := CmdReset(context.Background(), docker.Get(), key,
		"",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
		true,
		false,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var removed bool
	for _, v := range mock.RemovedVolumes {
		if v == recovered {
			removed = true
		}
	}
	if !removed {
		t.Errorf("recovered volume %q not used as the source, removed %v", recovered, mock.RemovedVolumes)
	}

	st, err := state.ReadState(key)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.HomeVolume == "" {
		t.Error("expected HomeVolume to be set after the operation")
	}
	if st.RecoveredHomeVolume != "" {
		t.Errorf("RecoveredHomeVolume = %q, want the marker cleared", st.RecoveredHomeVolume)
	}
	root, err := state.ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	if _, ok := root.AdoptedHomeVolume("widget-aaa", "opencode"); ok {
		t.Errorf("adoption claim not cleared after the volume op: %+v", root.AdoptedHomeVolumes)
	}
}

// TestVolumeOp_ClearsRecoveredHomeVolumeWithExplicitSource covers the marker left
// next to an explicit source volume: the commit must clear it, or the next home
// resolution would recover the old volume and undo the operation.
func TestVolumeOp_ClearsRecoveredHomeVolumeWithExplicitSource(t *testing.T) {
	_, ui := setupVolumeOpsFixtures(t)

	key := state.Key{Slug: "testproj-aBc1234D", Agent: "opencode"}
	seedHomeState(key, state.HomeState{RecoveredHomeVolume: "stale-marker"})

	err := CmdReset(context.Background(), docker.Get(), key,
		"explicit-vol",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
		false,
		false,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	st, err := state.ReadState(key)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.RecoveredHomeVolume != "" {
		t.Errorf("RecoveredHomeVolume = %q, want the marker cleared", st.RecoveredHomeVolume)
	}
}

func TestCmdEdit_LoadImageFails(t *testing.T) {
	_, ui := setupVolumeOpsFixtures(t)
	docker.WithDefaultErrorDockerMock(t)

	slug := "testproj-aBc1234D"
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"},
	)

	err := CmdEdit(context.Background(), docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:abc"},
		false,
		false,
		ui,
	)
	if err == nil || !strings.Contains(err.Error(), "load runner image") {
		t.Fatalf("expected load runner image error, got: %v", err)
	}
}

func TestCmdEdit_DestroyFailureWarns(t *testing.T) {
	mock, ui := setupVolumeOpsFixtures(t)
	slug := "testproj-aBc1234D"
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"},
	)

	callCount := 0
	mock.CreateSandboxFn = func(_ context.Context, name string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		callCount++
		if callCount == 2 {
			return &msb.MockSandbox{Name_: name, DestroyErr: errors.New("destroy failed")}, nil
		}
		return msb.NewMockSandbox(msb.SandboxOpts{}), nil
	}

	err := CmdEdit(context.Background(), docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:abc"},
		false,
		false,
		ui,
	)
	if err != nil {
		t.Fatalf("CmdEdit: %v", err)
	}
	var warned bool
	for _, c := range ui.WarnCalls {
		if strings.Contains(c, "failed to destroy edit sandbox") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("expected an edit destroy-failure warning, got %v", ui.WarnCalls)
	}
}

func TestCmdEdit_LoadImageFailsInMain(t *testing.T) {
	mock, ui := setupVolumeOpsFixtures(t)

	slug := "testproj-aBc1234D"
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"},
	)

	loadCalls := 0
	mock.ImageLoadFn = func(_ context.Context, _ string, _ io.Reader) error {
		loadCalls++
		if loadCalls == 2 {
			return errors.New("image load failed")
		}
		return nil
	}

	err := CmdEdit(context.Background(), docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:abc"},
		false,
		false,
		ui,
	)
	if err == nil || !strings.Contains(err.Error(), "load runner image") {
		t.Fatalf("expected load runner image error, got: %v", err)
	}
}

func TestCmdEdit_DryRun(t *testing.T) {
	_, ui := setupVolumeOpsFixtures(t)

	slug := "testproj-aBc1234D"
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"},
	)

	err := CmdEdit(context.Background(), docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:abc"},
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
	resolutions := msb.WithCountingMsbMock(t, mock)

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

	err := CmdEdit(context.Background(), docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:abc"},
		false,
		false,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *resolutions != 1 {
		t.Errorf("msb.Get resolved %d times, want once per CmdEdit", *resolutions)
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

	err := CmdEdit(context.Background(), docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:abc"},
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

	err := CmdEdit(context.Background(), docker.Get(), state.Key{Slug: slug, Agent: "opencode"},
		"",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:abc"},
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

	err := volumeOp(context.Background(), docker.Get(), key, "",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
		false, false,
		volCallbacks{
			dryRun: func(string) string { return "" },
			main: func(_ msb.Client, _ docker.Client, oldVolume, _ string) error {
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

// setupVolumeOpsFixtures installs the isolate state dir and the default msb
// mock needed by the volume operations tests and returns the mock handles.
// Tests that need a pre-existing home state or a customized msb mock declare
// it after the call.
func setupVolumeOpsFixtures(t *testing.T) (*msb.MockMsbClient, *termio.Mock) {
	t.Helper()
	configpaths.WithMockConfigPaths(t)
	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)
	docker.WithNoopDockerMock(t)
	return mock, &termio.Mock{}
}
