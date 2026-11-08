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

	"github.com/stretchr/testify/require"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/atomicwrite"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestHomeVolumeNameIsPrefixFree(t *testing.T) {
	got := HomeVolumeName(state.Key{Slug: "myproj-1mjusbm3wikhb0", Agent: "opencode"})
	require.NotContains(t, got, naming.LegacyHomePrefix)
	require.Regexp(t, `^myproj-1mjusbm3wikhb0-opencode-\d{8}T\d{6}$`, got)
}

func TestListVolumesParseRoundTrip(t *testing.T) {
	name := HomeVolumeName(state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"})
	info := naming.ParseHomeVolumeName(name)
	if info.Slug != "proj-1mjusbm3wikhb0" || info.Agent != "opencode" {
		t.Errorf("ParseHomeVolumeName(%q) = %+v, want slug=proj-1mjusbm3wikhb0 agent=opencode", name, info)
	}
}

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

func TestPrefillVolume_LoadImageFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithDefaultErrorDockerMock(t)

	mock := &msb.MockMsbClient{}
	vm := NewManager(docker.Get())
	err := vm.PrefillVolume(
		context.Background(),
		mock,
		state.Key{Slug: "myproject", Agent: "opencode"},
		"vol",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag"},
		&termio.Mock{},
	)
	if err == nil || !strings.Contains(err.Error(), "load runner image") || errors.Unwrap(err) == nil {
		t.Fatalf("expected load runner image error, got: %v", err)
	}
}

func TestCopyVolume_DestroyFailureWarns(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	mock := &msb.MockMsbClient{}
	mock.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return &msb.MockSandbox{DestroyErr: errors.New("destroy failed")}, nil
	}
	ui := &termio.Mock{}
	vm := NewManager(docker.Get())

	err := vm.CopyVolume(
		context.Background(),
		mock,
		state.Key{Slug: "myproject", Agent: "opencode"},
		"old-vol",
		"new-vol",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag"},
		ui,
	)
	if err != nil {
		t.Fatalf("CopyVolume failed: %v", err)
	}
	var warned bool
	for _, c := range ui.WarnCalls {
		if strings.Contains(c, "failed to destroy copy sandbox") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("expected a copy destroy-failure warning, got %v", ui.WarnCalls)
	}
}

func TestCopyVolume_LoadImageFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithDefaultErrorDockerMock(t)

	mock := &msb.MockMsbClient{}
	vm := NewManager(docker.Get())
	err := vm.CopyVolume(
		context.Background(),
		mock,
		state.Key{Slug: "myproject", Agent: "opencode"},
		"old-vol",
		"new-vol",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag"},
		&termio.Mock{},
	)
	if err == nil || !strings.Contains(err.Error(), "load runner image") || errors.Unwrap(err) == nil {
		t.Fatalf("expected load runner image error, got: %v", err)
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
	vm := NewManager(docker.Get())

	vol, err := vm.ApplyHomeAction(
		context.Background(),
		mock,
		state.Key{Slug: slug, Agent: "opencode"},
		"old-vol",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
		ActionKeep,
		false,
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
	vm := NewManager(docker.Get())

	_, err := vm.ApplyHomeAction(
		context.Background(),
		mock,
		state.Key{Slug: "myproj", Agent: "opencode"},
		"old-vol",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
		ActionReset,
		false,
		&termio.Mock{},
	)
	if err == nil || !strings.Contains(err.Error(), "create volume") || errors.Unwrap(err) == nil {
		t.Fatalf("expected create volume error, got: %v", err)
	}
}

func TestApplyHomeAction_WriteStateFails_ReturnsError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	slug := "writefail-apply"
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

	mock := &msb.MockMsbClient{}
	var createdVol string
	mock.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return msb.NewMockSandbox(msb.SandboxOpts{}), nil
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		createdVol = name
		return &msb.MockVolumeHandle{Name_: name}, nil
	}

	ui := &termio.Mock{}
	vm := NewManager(docker.Get())
	_, err := vm.ApplyHomeAction(
		context.Background(),
		mock,
		state.Key{Slug: slug, Agent: "opencode"},
		"old-vol",
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
		ActionReset,
		false,
		ui,
	)
	if err == nil {
		t.Fatal("expected a state-write error, got nil")
	}
	if !strings.Contains(err.Error(), "write state") {
		t.Errorf("unexpected error: %v", err)
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

// TestPrefillSandboxNameIsPrefixFreeAndLabelled pins the task sandbox shape: no
// vendor prefix on the name, and the task label that makes it classifiable
// without one.
func TestPrefillSandboxNameIsPrefixFreeAndLabelled(t *testing.T) {
	docker.WithNoopDockerMock(t)
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{CreatedSandbox: &msb.MockSandbox{Name_: "prefill"}}
	vm := NewManager(docker.Get())

	err := vm.PrefillVolume(
		context.Background(),
		client,
		state.Key{Slug: "myproject", Agent: "opencode"},
		"test-home-vol",
		image.ImageInfo{DockerTag: "agents-sandbox/runner-test:latest", Ref: "agents-sandbox/runner-test:latest"},
		&ui,
	)
	require.NoError(t, err)
	require.Len(t, client.CreatedSandboxCalls, 1)
	name := client.CreatedSandboxCalls[0].Name
	require.True(t, strings.HasPrefix(name, "myproject-opencode-"), "name = %q", name)
	require.NotContains(t, name, naming.LegacyTaskPrefix)

	var cfg msbSdk.SandboxConfig
	for _, opt := range client.CreatedSandboxCalls[0].Opts {
		opt(&cfg)
	}
	require.Equal(t, naming.SandboxTypeTask, cfg.Labels[naming.LabelType])
	require.Equal(t, "myproject", cfg.Labels[naming.LabelSlug])
	require.Equal(t, "opencode", cfg.Labels[naming.LabelAgent])
}

// TestCopySandboxNameIsPrefixFreeAndLabelled pins the second creation site.
func TestCopySandboxNameIsPrefixFreeAndLabelled(t *testing.T) {
	docker.WithNoopDockerMock(t)
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{CreatedSandbox: &msb.MockSandbox{Name_: "copy"}}
	vm := NewManager(docker.Get())

	err := vm.CopyVolume(
		context.Background(),
		client,
		state.Key{Slug: "myproject", Agent: "opencode"},
		"old-home-vol",
		"new-home-vol",
		image.ImageInfo{DockerTag: "agents-sandbox/runner-test:latest", Ref: "agents-sandbox/runner-test:latest"},
		&ui,
	)
	require.NoError(t, err)
	require.Len(t, client.CreatedSandboxCalls, 1)
	name := client.CreatedSandboxCalls[0].Name
	require.True(t, strings.HasPrefix(name, "myproject-opencode-"), "name = %q", name)
	require.NotContains(t, name, naming.LegacyTaskPrefix)

	var cfg msbSdk.SandboxConfig
	for _, opt := range client.CreatedSandboxCalls[0].Opts {
		opt(&cfg)
	}
	require.Equal(t, naming.SandboxTypeTask, cfg.Labels[naming.LabelType])
	require.Equal(t, "myproject", cfg.Labels[naming.LabelSlug])
	require.Equal(t, "opencode", cfg.Labels[naming.LabelAgent])
}

func TestPrefillVolumeRunsCopyCommand(t *testing.T) {
	docker.WithNoopDockerMock(t)
	testUI := termio.NewTestMock(t)
	ui := &testUI
	prefillSandbox := &msb.MockSandbox{Name_: "prefill"}
	client := &msb.MockMsbClient{CreatedSandbox: prefillSandbox}
	vm := NewManager(docker.Get())

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
	vm := NewManager(docker.Get())

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
	vm := NewManager(docker.Get())

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
	vm := NewManager(docker.Get())

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
	vm := NewManager(docker.Get())

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
			vm := NewManager(docker.Get())

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
	vm := NewManager(docker.Get())

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
	vm := NewManager(docker.Get())

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
	vm := NewManager(docker.Get())

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

func TestCleanupVolume_Success(t *testing.T) {
	mock := &msb.MockMsbClient{}
	vm := NewManager(docker.Get())

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
	vm := NewManager(docker.Get())

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
			vm := NewManager(docker.Get())

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
	vm := NewManager(docker.Get())

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
	vm := NewManager(docker.Get())

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
	if !strings.Contains(err.Error(), "prefill cp") || errors.Unwrap(err) == nil {
		t.Errorf("error = %v, want the prefill copy failure wrapping its cause", err)
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
	vm := NewManager(docker.Get())

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
		t.Errorf("error = %v, want the prefill copy failure with its exit status", err)
	}
}

func TestCopyVolume_CreateSandboxFails(t *testing.T) {
	docker.WithNoopDockerMock(t)
	mock := &msb.MockMsbClient{}
	mock.CreateSandboxErr = errors.New("sandbox creation failed")
	vm := NewManager(docker.Get())

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
	vm := NewManager(docker.Get())

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
	vm := NewManager(docker.Get())

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
	vm := NewManager(docker.Get())

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
			EnvState:        state.EnvState{Hash: "envhash", Entries: []string{"FOO"}},
			SecretState:     state.SecretState{Hash: "sechash", Entries: []string{"TOKEN"}},
			NetworkState:    state.NetworkState{Hash: "nethash", Entries: []string{"none"}},
			MountState:      state.MountState{Hash: "mounthash", Entries: []string{"/mnt/x"}},
		},
	)

	mock := &msb.MockMsbClient{}
	vm := NewManager(docker.Get())

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
	vm := NewManager(docker.Get())

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
