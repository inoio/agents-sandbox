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
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestCheckForActiveVMs_ListSandboxesError(t *testing.T) {
	mock, _ := setupVolumeOpsFixtures(t)
	mock.ListSandboxesErr = errors.New("boom")

	err := checkForActiveVMs(context.Background(), state.Key{Slug: "someproj", Agent: "opencode"})
	if err == nil || !strings.Contains(err.Error(), "list sandboxes") {
		t.Fatalf("expected list sandboxes error, got: %v", err)
	}
}

func TestCheckForActiveVMs_IgnoresNonVMSandboxes(t *testing.T) {
	mock, _ := setupVolumeOpsFixtures(t)
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{Name_: "agents-sandbox-task-someproj", Status_: msbSdk.SandboxStatusRunning},
	}

	if err := checkForActiveVMs(context.Background(), state.Key{Slug: "someproj", Agent: "opencode"}); err != nil {
		t.Fatalf("expected no error for non-VM sandbox, got: %v", err)
	}
}

func TestCheckForActiveVMs_OtherSlugVMIsIgnored(t *testing.T) {
	mock, _ := setupVolumeOpsFixtures(t)
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{Name_: "agents-sandbox-vm-otherproj", Status_: msbSdk.SandboxStatusRunning},
	}

	if err := checkForActiveVMs(context.Background(), state.Key{Slug: "someproj", Agent: "opencode"}); err != nil {
		t.Fatalf("expected no error for a VM of another slug, got: %v", err)
	}
}

func TestCheckForActiveVMs_InactiveVMSameSlugIsIgnored(t *testing.T) {
	mock, _ := setupVolumeOpsFixtures(t)
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{Name_: "agents-sandbox-vm-someproj", Status_: msbSdk.SandboxStatusStopped},
	}

	if err := checkForActiveVMs(context.Background(), state.Key{Slug: "someproj", Agent: "opencode"}); err != nil {
		t.Fatalf("expected no error for an inactive VM, got: %v", err)
	}
}

func TestVolumeOp_ReadStateCorruptError(t *testing.T) {
	_, ui := setupVolumeOpsFixtures(t)
	slug := "corruptproj-read"
	statePath := filepath.Join(configpaths.Get().UserStateDir(), slug, "opencode", "state.yaml")
	if err := os.MkdirAll(statePath, 0o700); err != nil {
		t.Fatal(err)
	}

	err := CmdReset(
		context.Background(),
		state.Key{Slug: slug, Agent: "opencode"},
		"",
		"img-tag",
		"sha256:new",
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
	state.WriteState(state.Key{Slug: slug, Agent: "opencode"}, state.HomeState{})

	err := CmdReset(
		context.Background(),
		state.Key{Slug: slug, Agent: "opencode"},
		"",
		"img-tag",
		"sha256:new",
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
	state.WriteState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"},
	)
	mock.CreateSandboxErr = errors.New("prefill failed")

	err := CmdReset(
		context.Background(),
		state.Key{Slug: slug, Agent: "opencode"},
		"",
		"img-tag",
		"sha256:new",
		false,
		false,
		ui,
	)
	if err == nil || !strings.Contains(err.Error(), "prefill new volume") {
		t.Fatalf("expected prefill error, got: %v", err)
	}
}

func TestVolumeOp_MainFails_CleanupVolumeFailsWarns(t *testing.T) {
	mock, ui := setupVolumeOpsFixtures(t)
	slug := "testproj-aBc1234D"
	state.WriteState(
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

	err := CmdMigrate(
		context.Background(),
		state.Key{Slug: slug, Agent: "opencode"},
		"",
		"img-tag",
		"sha256:new",
		false,
		false,
		ui,
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var warned bool
	for _, c := range ui.WarnCalls {
		if strings.Contains(c, "failed to clean up") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("expected cleanup warning, got: %v", ui.WarnCalls)
	}
}

func TestVolumeOp_WriteStateFails_Warns(t *testing.T) {
	mock, ui := setupVolumeOpsFixtures(t)
	slug := "testproj-aBc1234D"
	state.WriteState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:old"},
	)

	sdir := filepath.Join(configpaths.Get().UserStateDir(), slug, "opencode")
	if err := os.MkdirAll(filepath.Join(sdir, ".state.yaml.tmp"), 0o700); err != nil {
		t.Fatal(err)
	}

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
		"img-tag",
		"sha256:new",
		false,
		false,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var warned bool
	for _, c := range ui.WarnCalls {
		if strings.Contains(c, "failed to write state file") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("expected write-state warning, got: %v", ui.WarnCalls)
	}
}

func TestVolumeOp_RemoveOldFails_Warns(t *testing.T) {
	mock, ui := setupVolumeOpsFixtures(t)
	slug := "testproj-aBc1234D"
	oldVol := "agents-sandbox-home-" + slug + "-old"
	state.WriteState(
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

	err := CmdReset(
		context.Background(),
		state.Key{Slug: slug, Agent: "opencode"},
		"",
		"img-tag",
		"sha256:new",
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

func TestCmdEdit_LoadImageFails(t *testing.T) {
	_, ui := setupVolumeOpsFixtures(t)
	docker.WithDefaultErrorDockerMock(t)

	slug := "testproj-aBc1234D"
	state.WriteState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:abc"},
	)

	err := CmdEdit(
		context.Background(),
		state.Key{Slug: slug, Agent: "opencode"},
		"",
		"img-tag",
		"sha256:abc",
		false,
		false,
		ui,
	)
	if err == nil || !strings.Contains(err.Error(), "load runner image") {
		t.Fatalf("expected load runner image error, got: %v", err)
	}
}

func TestPrefillVolume_LoadImageFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithDefaultErrorDockerMock(t)

	mock := &msb.MockMsbClient{}
	vm := NewManager(&termio.Mock{})
	err := vm.PrefillVolume(
		context.Background(),
		mock,
		state.Key{Slug: "myproject", Agent: "opencode"},
		"vol",
		"img-tag",
		&termio.Mock{},
	)
	if err == nil || !strings.Contains(err.Error(), "load runner image") {
		t.Fatalf("expected load runner image error, got: %v", err)
	}
}

func TestCopyVolume_LoadImageFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithDefaultErrorDockerMock(t)

	mock := &msb.MockMsbClient{}
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
	if err == nil || !strings.Contains(err.Error(), "load runner image") {
		t.Fatalf("expected load runner image error, got: %v", err)
	}
}

func TestEnsureNewHome_CreateVolumeFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := &msb.MockMsbClient{}
	mock.CreateVolumeFn = func(_ context.Context, _ string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return nil, errors.New("create failed")
	}
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
	if err == nil || !strings.Contains(err.Error(), "create volume") {
		t.Fatalf("expected create volume error, got: %v", err)
	}
}

func TestRecordHomeImage_ReadStateError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "corrupt-rh"
	statePath := filepath.Join(configpaths.Get().UserStateDir(), slug, "opencode", "state.yaml")
	if err := os.MkdirAll(statePath, 0o700); err != nil {
		t.Fatal(err)
	}

	vm := NewManager(&termio.Mock{})
	err := vm.RecordHomeImage(state.Key{Slug: slug, Agent: "opencode"}, "sha256:new", &termio.Mock{})
	if err == nil {
		t.Fatal("expected error for unreadable state, got nil")
	}
}

func TestRecordHomeImage_WriteStateFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "writefail-rh"
	state.WriteState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "vol", ImageDigest: "sha256:old"},
	)
	sdir := filepath.Join(configpaths.Get().UserStateDir(), slug, "opencode")
	if err := os.MkdirAll(filepath.Join(sdir, ".state.yaml.tmp"), 0o700); err != nil {
		t.Fatal(err)
	}

	ui := &termio.Mock{}
	vm := NewManager(ui)
	err := vm.RecordHomeImage(state.Key{Slug: slug, Agent: "opencode"}, "sha256:new", ui)
	if err == nil {
		t.Fatal("expected write-state error, got nil")
	}
	var warned bool
	for _, c := range ui.WarnCalls {
		if strings.Contains(c, "failed to write state file") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("expected write-state warning, got: %v", ui.WarnCalls)
	}
}

func TestApplyHomeAction_KeepDryRun_ReturnsOldVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := &msb.MockMsbClient{}
	var createdVols int
	mock.CreateVolumeFn = func(_ context.Context, _ string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		createdVols++
		return &msb.MockVolumeHandle{}, nil
	}
	vm := NewManager(&termio.Mock{})

	vol, err := vm.ApplyHomeAction(
		context.Background(),
		mock,
		state.Key{Slug: "myproj", Agent: "opencode"},
		"old-vol",
		"img-tag",
		"sha256:new",
		ActionKeep,
		options.RunOptions{DryRun: true},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vol != "old-vol" {
		t.Errorf("volume = %q, want old-vol", vol)
	}
	if createdVols != 0 {
		t.Errorf("expected no volumes created, got %d", createdVols)
	}
}

func TestApplyHomeAction_KeepRecordImageFails_Warns(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "corrupt-keep"
	statePath := filepath.Join(configpaths.Get().UserStateDir(), slug, "opencode", "state.yaml")
	if err := os.MkdirAll(statePath, 0o700); err != nil {
		t.Fatal(err)
	}

	mock := &msb.MockMsbClient{}
	ui := &termio.Mock{}
	vm := NewManager(ui)

	vol, err := vm.ApplyHomeAction(
		context.Background(),
		mock,
		state.Key{Slug: slug, Agent: "opencode"},
		"old-vol",
		"img-tag",
		"sha256:new",
		ActionKeep,
		options.RunOptions{},
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vol != "old-vol" {
		t.Errorf("volume = %q, want old-vol", vol)
	}
	var warned bool
	for _, c := range ui.WarnCalls {
		if strings.Contains(c, "failed to record image digest") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("expected record-image warning, got: %v", ui.WarnCalls)
	}
}

func TestApplyHomeAction_CreateVolumeFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := &msb.MockMsbClient{}
	mock.CreateVolumeFn = func(_ context.Context, _ string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return nil, errors.New("create failed")
	}
	vm := NewManager(&termio.Mock{})

	_, err := vm.ApplyHomeAction(
		context.Background(),
		mock,
		state.Key{Slug: "myproj", Agent: "opencode"},
		"old-vol",
		"img-tag",
		"sha256:new",
		ActionReset,
		options.RunOptions{},
		&termio.Mock{},
	)
	if err == nil || !strings.Contains(err.Error(), "create volume") {
		t.Fatalf("expected create volume error, got: %v", err)
	}
}

func TestApplyHomeAction_WriteStateFails_Warns(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	slug := "writefail-apply"
	state.WriteState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "old-vol", ImageDigest: "sha256:old"},
	)
	sdir := filepath.Join(configpaths.Get().UserStateDir(), slug, "opencode")
	if err := os.MkdirAll(filepath.Join(sdir, ".state.yaml.tmp"), 0o700); err != nil {
		t.Fatal(err)
	}

	mock := &msb.MockMsbClient{}
	mock.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return msb.NewMockSandbox(msb.SandboxOpts{}), nil
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return &msb.MockVolumeHandle{Name_: name}, nil
	}

	ui := &termio.Mock{}
	vm := NewManager(ui)
	_, err := vm.ApplyHomeAction(
		context.Background(),
		mock,
		state.Key{Slug: slug, Agent: "opencode"},
		"old-vol",
		"img-tag",
		"sha256:new",
		ActionReset,
		options.RunOptions{},
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var warned bool
	for _, c := range ui.WarnCalls {
		if strings.Contains(c, "failed to write state file") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("expected write-state warning, got: %v", ui.WarnCalls)
	}
}

func TestCmdEdit_LoadImageFailsInMain(t *testing.T) {
	mock, ui := setupVolumeOpsFixtures(t)

	slug := "testproj-aBc1234D"
	state.WriteState(
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

	err := CmdEdit(
		context.Background(),
		state.Key{Slug: slug, Agent: "opencode"},
		"",
		"img-tag",
		"sha256:abc",
		false,
		false,
		ui,
	)
	if err == nil || !strings.Contains(err.Error(), "load runner image") {
		t.Fatalf("expected load runner image error, got: %v", err)
	}
}
