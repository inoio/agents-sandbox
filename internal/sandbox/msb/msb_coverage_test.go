package msb

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

func TestRealSandboxFS(t *testing.T) {
	sb := &realSandbox{sandbox: &msbSdk.Sandbox{}}
	fs := sb.FS()
	if fs == nil {
		t.Fatal("FS() returned nil")
	}
}

func TestMockMsbClientEnsureInstalled(t *testing.T) {
	called := false
	m := &MockMsbClient{EnsureInstalledFn: func(_ context.Context) error {
		called = true
		return errors.New("boom")
	}}
	if err := m.EnsureInstalled(context.Background()); err == nil {
		t.Fatal("expected error from EnsureInstalledFn")
	}
	if !called {
		t.Fatal("EnsureInstalledFn not called")
	}
}

func TestRuntimePreparationHelpers(t *testing.T) {
	previousGet := Get
	Get = realGetFactory
	t.Cleanup(func() { Get = previousGet })
	if !IsRealClient() {
		t.Fatal("default client should be real")
	}
	previous := skipRuntimeInstall
	t.Cleanup(func() { skipRuntimeInstall = previous })
	SkipRuntimeInstall()
	if err := (&realMsbClient{}).EnsureInstalled(context.Background()); err != nil {
		t.Fatalf("EnsureInstalled after skip = %v", err)
	}
}

// TestValidateInstalledPassesPrivateHome asserts the resolve call names the
// private home explicitly instead of relying on the process-global MSB_HOME.
func TestValidateInstalledPassesPrivateHome(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantHome := configpaths.MSBHome()

	orig := resolveRuntime
	resolveRuntime = func(config msbSdk.RuntimeConfig) (msbSdk.ResolvedRuntime, error) {
		if config.Home != wantHome {
			t.Errorf("RuntimeConfig.Home = %q, want %q", config.Home, wantHome)
		}
		return msbSdk.ResolvedRuntime{}, errors.New("stop after capture")
	}
	t.Cleanup(func() { resolveRuntime = orig })

	if err := ValidateInstalled(context.Background()); err == nil {
		t.Fatal("ValidateInstalled() error = nil, want captured seam error")
	}
}

// TestRealClientEnsureInstalledPassesPrivateHome asserts the ensure call names
// the private home explicitly.
func TestRealClientEnsureInstalledPassesPrivateHome(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	previous := skipRuntimeInstall
	skipRuntimeInstall = false
	t.Cleanup(func() { skipRuntimeInstall = previous })

	wantHome := configpaths.MSBHome()
	orig := ensureRuntime
	ensureRuntime = func(
		_ context.Context, config msbSdk.RuntimeConfig, _ msbSdk.InstallOptions,
	) (msbSdk.ResolvedRuntime, error) {
		if config.Home != wantHome {
			t.Errorf("RuntimeConfig.Home = %q, want %q", config.Home, wantHome)
		}
		return msbSdk.ResolvedRuntime{}, nil
	}
	t.Cleanup(func() { ensureRuntime = orig })

	if err := (&realMsbClient{}).EnsureInstalled(context.Background()); err != nil {
		t.Fatalf("EnsureInstalled() error = %v", err)
	}
}

func TestMockMsbClientGetSandbox(t *testing.T) {
	ctx := context.Background()

	m := &MockMsbClient{}
	if _, err := m.GetSandbox(ctx, "missing"); err == nil {
		t.Fatal("expected not-found error for empty mock")
	}

	h := &MockSandboxHandle{Name_: "s1"}
	m.Sandboxes = []SandboxHandle{h}
	got, err := m.GetSandbox(ctx, "s1")
	if err != nil || got != h {
		t.Fatalf("GetSandbox via collection = %v, %v; want %v", got, err, h)
	}

	m.SetGetSandboxErr(errors.New("fail"))
	if _, err := m.GetSandbox(ctx, "s1"); err == nil {
		t.Fatal("expected error from SetGetSandboxErr")
	}

	m2 := &MockMsbClient{}
	m2.SetGotSandbox(h)
	if got, err := m2.GetSandbox(ctx, "x"); err != nil || got != h {
		t.Fatalf("GetSandbox via SetGotSandbox = %v, %v", got, err)
	}

	m3 := &MockMsbClient{}
	m3.SetGotSandbox(nil)
	if _, err := m3.GetSandbox(ctx, "x"); err == nil {
		t.Fatal("SetGotSandbox(nil) should clear override")
	}

	m4 := &MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (SandboxHandle, error) {
		return nil, errors.New("fn")
	}}
	if _, err := m4.GetSandbox(ctx, "x"); err == nil {
		t.Fatal("expected error from GetSandboxFn")
	}
}

func TestMockMsbClientCreateSandbox(t *testing.T) {
	ctx := context.Background()

	m := &MockMsbClient{}
	sb, err := m.CreateSandbox(ctx, "default")
	if err != nil {
		t.Fatalf("CreateSandbox default error = %v", err)
	}
	if sb == nil {
		t.Fatal("CreateSandbox default returned nil sandbox")
	}
	if m.CreatedSandboxes[0] != "default" {
		t.Fatalf("CreatedSandboxes = %v", m.CreatedSandboxes)
	}
	if len(m.CreatedSandboxCalls) != 1 || m.CreatedSandboxCalls[0].Name != "default" {
		t.Fatalf("CreatedSandboxCalls = %v", m.CreatedSandboxCalls)
	}

	m2 := &MockMsbClient{CreateSandboxErr: errors.New("fail")}
	if _, err := m2.CreateSandbox(ctx, "x"); err == nil {
		t.Fatal("expected CreateSandboxErr")
	}

	want := &MockSandbox{Name_: "custom"}
	m3 := &MockMsbClient{CreatedSandbox: want}
	got, err := m3.CreateSandbox(ctx, "custom")
	if err != nil || got != want {
		t.Fatalf("CreateSandbox via CreatedSandbox = %v, %v", got, err)
	}

	m4 := &MockMsbClient{
		CreateSandboxFn: func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (Sandbox, error) {
			return nil, errors.New("fn")
		},
	}
	if _, err := m4.CreateSandbox(ctx, "x"); err == nil {
		t.Fatal("expected CreateSandboxFn error")
	}
}

func TestMockMsbClientListSandboxes(t *testing.T) {
	ctx := context.Background()
	h := &MockSandboxHandle{Name_: "a"}
	m := &MockMsbClient{Sandboxes: []SandboxHandle{h}}
	if got, err := m.ListSandboxes(ctx, nil); err != nil || len(got) != 1 || got[0] != h {
		t.Fatalf("ListSandboxes = %v, %v", got, err)
	}
	m.ListSandboxesErr = errors.New("fail")
	if _, err := m.ListSandboxes(ctx, nil); err == nil {
		t.Fatal("expected ListSandboxesErr")
	}
	m2 := &MockMsbClient{ListSandboxesFn: func(_ context.Context, _ map[string]string) ([]SandboxHandle, error) {
		return nil, errors.New("fn")
	}}
	if _, err := m2.ListSandboxes(ctx, nil); err == nil {
		t.Fatal("expected ListSandboxesFn error")
	}
}

func TestMockMsbClientGetVolume(t *testing.T) {
	ctx := context.Background()
	m := &MockMsbClient{}
	if _, err := m.GetVolume(ctx, "missing"); err == nil {
		t.Fatal("expected volume not-found error")
	}
	v := &MockVolumeHandle{Name_: "vol"}
	m.Volumes = []VolumeHandle{v}
	if got, err := m.GetVolume(ctx, "vol"); err != nil || got != v {
		t.Fatalf("GetVolume via collection = %v, %v", got, err)
	}
	m.SetGetVolumeErr(errors.New("fail"))
	if _, err := m.GetVolume(ctx, "vol"); err == nil {
		t.Fatal("expected SetGetVolumeErr error")
	}
	m2 := &MockMsbClient{}
	m2.SetGetVolumeErr(nil)
	if _, err := m2.GetVolume(ctx, "vol"); err == nil {
		t.Fatal("SetGetVolumeErr(nil) should restore not-found behavior")
	}
	m3 := &MockMsbClient{GetVolumeFn: func(_ context.Context, _ string) (VolumeHandle, error) {
		return nil, errors.New("fn")
	}}
	if _, err := m3.GetVolume(ctx, "x"); err == nil {
		t.Fatal("expected GetVolumeFn error")
	}
}

func TestMockMsbClientCreateVolume(t *testing.T) {
	ctx := context.Background()
	m := &MockMsbClient{}
	v, err := m.CreateVolume(ctx, "v1")
	if err != nil || v == nil {
		t.Fatalf("CreateVolume = %v, %v", v, err)
	}
	m2 := &MockMsbClient{
		CreateVolumeFn: func(_ context.Context, _ string, _ ...msbSdk.VolumeOption) (VolumeHandle, error) {
			return nil, errors.New("fn")
		},
	}
	if _, err := m2.CreateVolume(ctx, "x"); err == nil {
		t.Fatal("expected CreateVolumeFn error")
	}
	m3 := &MockMsbClient{}
	m3.createVolumeErr = errors.New("fail")
	if _, err := m3.CreateVolume(ctx, "x"); err == nil {
		t.Fatal("expected createVolumeErr")
	}
}

func TestMockMsbClientListVolumes(t *testing.T) {
	ctx := context.Background()
	v := &MockVolumeHandle{Name_: "v"}
	m := &MockMsbClient{Volumes: []VolumeHandle{v}}
	if got, err := m.ListVolumes(ctx); err != nil || len(got) != 1 || got[0] != v {
		t.Fatalf("ListVolumes = %v, %v", got, err)
	}
	m.ListVolumesErr = errors.New("fail")
	if _, err := m.ListVolumes(ctx); err == nil {
		t.Fatal("expected ListVolumesErr")
	}
	m2 := &MockMsbClient{ListVolumesFn: func(_ context.Context) ([]VolumeHandle, error) {
		return nil, errors.New("fn")
	}}
	if _, err := m2.ListVolumes(ctx); err == nil {
		t.Fatal("expected ListVolumesFn error")
	}
}

func TestMockMsbClientRemoveVolume(t *testing.T) {
	ctx := context.Background()
	m := &MockMsbClient{}
	if err := m.RemoveVolume(ctx, "rv"); err != nil {
		t.Fatalf("RemoveVolume error = %v", err)
	}
	if len(m.RemovedVolumes) != 1 || m.RemovedVolumes[0] != "rv" {
		t.Fatalf("RemovedVolumes = %v", m.RemovedVolumes)
	}
	m.removeVolumeErr = errors.New("fail")
	if err := m.RemoveVolume(ctx, "rv2"); err == nil {
		t.Fatal("expected removeVolumeErr")
	}
	m2 := &MockMsbClient{RemoveVolumeFn: func(_ context.Context, _ string) error {
		return errors.New("fn")
	}}
	if err := m2.RemoveVolume(ctx, "x"); err == nil {
		t.Fatal("expected RemoveVolumeFn error")
	}
}

func TestMockMsbClientImage(t *testing.T) {
	ctx := context.Background()

	m := &MockMsbClient{ImageGetFn: func(_ context.Context, _ string) error {
		return errors.New("fn")
	}}
	if err := m.ImageGet(ctx, "ref"); err == nil {
		t.Fatal("expected ImageGetFn error")
	}
	m2 := &MockMsbClient{}
	m2.imageGetErr = errors.New("fail")
	if err := m2.ImageGet(ctx, "ref"); err == nil {
		t.Fatal("expected imageGetErr")
	}

	img := &MockImageHandle{Reference_: "img"}
	m3 := &MockMsbClient{Images: []ImageHandle{img}}
	if got, err := m3.ImageList(ctx); err != nil || len(got) != 1 || got[0] != img {
		t.Fatalf("ImageList = %v, %v", got, err)
	}
	m3.ListImagesErr = errors.New("fail")
	if _, err := m3.ImageList(ctx); err == nil {
		t.Fatal("expected ListImagesErr")
	}
	m4 := &MockMsbClient{ImageListFn: func(_ context.Context) ([]ImageHandle, error) {
		return nil, errors.New("fn")
	}}
	if _, err := m4.ImageList(ctx); err == nil {
		t.Fatal("expected ImageListFn error")
	}

	m5 := &MockMsbClient{}
	if err := m5.ImageRemove(ctx, "ref", true); err != nil {
		t.Fatalf("ImageRemove error = %v", err)
	}
	if len(m5.RemovedImages) != 1 || m5.RemovedImages[0].Ref != "ref" || !m5.RemovedImages[0].Force {
		t.Fatalf("RemovedImages = %v", m5.RemovedImages)
	}
	m5.removeImageErr = errors.New("fail")
	if err := m5.ImageRemove(ctx, "ref", false); err == nil {
		t.Fatal("expected removeImageErr")
	}
	m6 := &MockMsbClient{ImageRemoveFn: func(_ context.Context, _ string, _ bool) error {
		return errors.New("fn")
	}}
	if err := m6.ImageRemove(ctx, "ref", false); err == nil {
		t.Fatal("expected ImageRemoveFn error")
	}

	m7 := &MockMsbClient{}
	if err := m7.ImageLoad(ctx, "ref", strings.NewReader("x")); err != nil {
		t.Fatalf("ImageLoad error = %v", err)
	}
	if len(m7.LoadedImages) != 1 || m7.LoadedImages[0] != "ref" {
		t.Fatalf("LoadedImages = %v", m7.LoadedImages)
	}
	m7.imageLoadErr = errors.New("fail")
	if err := m7.ImageLoad(ctx, "ref", strings.NewReader("x")); err == nil {
		t.Fatal("expected imageLoadErr")
	}
	m8 := &MockMsbClient{ImageLoadFn: func(_ context.Context, _ string, _ io.Reader) error {
		return errors.New("fn")
	}}
	if err := m8.ImageLoad(ctx, "ref", strings.NewReader("x")); err == nil {
		t.Fatal("expected ImageLoadFn error")
	}

	m9 := &MockMsbClient{ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
		return &msbSdk.ImageConfig{Digest: "sha256:x"}, nil
	}}
	cfg, err := m9.ImageInspect(ctx, "ref")
	if err != nil || cfg == nil || cfg.Digest != "sha256:x" {
		t.Fatalf("ImageInspect via fn = %v, %v", cfg, err)
	}
	m10 := &MockMsbClient{}
	if cfg, err := m10.ImageInspect(ctx, "ref"); err != nil || cfg == nil {
		t.Fatalf("ImageInspect default = %v, %v", cfg, err)
	}

	m11 := &MockMsbClient{ImagePruneFn: func(context.Context) (*msbSdk.ImagePruneReport, error) {
		return &msbSdk.ImagePruneReport{ManifestsRemoved: 1}, nil
	}}
	report, err := m11.ImagePrune(ctx)
	if err != nil || report == nil || report.ManifestsRemoved != 1 {
		t.Fatalf("ImagePrune via fn = %v, %v", report, err)
	}
	m12 := &MockMsbClient{}
	report, err = m12.ImagePrune(ctx)
	if err != nil || report == nil || report.ManifestsRemoved != 0 {
		t.Fatalf("ImagePrune default = %v, %v", report, err)
	}
}

// TestMockMsbClientPruneStorage pins that the mock records every
// StoragePruneOptions it receives and honours StoragePruneFn.
func TestMockMsbClientPruneStorage(t *testing.T) {
	ctx := context.Background()
	physical := uint64(2048)
	m := &MockMsbClient{
		StoragePruneFn: func(_ context.Context, _ msbSdk.StoragePruneOptions) (*msbSdk.StoragePruneReport, error) {
			return &msbSdk.StoragePruneReport{
				FilesRemoved:           3,
				LogicalBytesRemoved:    4096,
				PhysicalBytesReclaimed: &physical,
			}, nil
		},
	}
	report, err := m.PruneStorage(ctx, msbSdk.StoragePruneOptions{DryRun: true, OlderThanSeconds: 600})
	if err != nil || report.FilesRemoved != 3 || report.LogicalBytesRemoved != 4096 {
		t.Fatalf("PruneStorage via fn = %+v, %v", report, err)
	}
	if len(m.StoragePruneCalls) != 1 || !m.StoragePruneCalls[0].DryRun ||
		m.StoragePruneCalls[0].OlderThanSeconds != 600 {
		t.Fatalf("StoragePruneCalls = %+v", m.StoragePruneCalls)
	}

	empty := &MockMsbClient{}
	report, err = empty.PruneStorage(ctx, msbSdk.StoragePruneOptions{OlderThanSeconds: 60})
	if err != nil || report == nil || report.FilesRemoved != 0 {
		t.Fatalf("PruneStorage default = %+v, %v", report, err)
	}
	if len(empty.StoragePruneCalls) != 1 || empty.StoragePruneCalls[0].OlderThanSeconds != 60 {
		t.Fatalf("StoragePruneCalls = %+v", empty.StoragePruneCalls)
	}

	failing := &MockMsbClient{
		StoragePruneFn: func(context.Context, msbSdk.StoragePruneOptions) (*msbSdk.StoragePruneReport, error) {
			return nil, errors.New("prune failed")
		},
	}
	if _, err := failing.PruneStorage(ctx, msbSdk.StoragePruneOptions{}); err == nil {
		t.Fatal("expected StoragePruneFn error")
	}
}

func TestMockSandboxHandleMethods(t *testing.T) {
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	h := &MockSandboxHandle{
		Name_:        "n",
		Status_:      msbSdk.SandboxStatusRunning,
		UpdatedAt_:   now,
		CreatedAt_:   now,
		BackendKind_: msbSdk.BackendLocal,
		Image_:       "img",
	}
	if h.Name() != "n" || h.Status() != msbSdk.SandboxStatusRunning ||
		!h.UpdatedAt().Equal(now) || !h.CreatedAt().Equal(now) ||
		h.BackendKind() != msbSdk.BackendLocal || h.Image() != "img" {
		t.Fatal("MockSandboxHandle accessors wrong")
	}

	if got, err := h.Refresh(context.Background()); err != nil || got != h {
		t.Fatalf("Refresh = %v, %v", got, err)
	}
	refreshed := &MockSandboxHandle{Name_: "refreshed"}
	h.RefreshFn = func(ctx context.Context) (SandboxHandle, error) {
		if ctx == nil {
			t.Fatal("RefreshFn received nil context")
		}
		return refreshed, nil
	}
	if got, err := h.Refresh(context.Background()); err != nil || got != refreshed {
		t.Fatalf("Refresh via fn = %v, %v", got, err)
	}
	if h.DidRemove() {
		t.Fatal("DidRemove should be false")
	}

	if err := h.Stop(context.Background()); err != nil || !h.DidStop {
		t.Fatal("Stop should mark DidStop")
	}
	if err := h.Kill(context.Background()); err != nil || !h.DidKill {
		t.Fatal("Kill should mark DidKill")
	}
	if err := h.Remove(context.Background()); err != nil || !h.DidRmv || !h.DidRemove() {
		t.Fatal("Remove should mark DidRmv")
	}
	if err := h.Destroy(context.Background()); err != nil || !h.Destroyed || !h.DidDestroy() {
		t.Fatal("Destroy should mark Destroyed")
	}
	failing := &MockSandboxHandle{DestroyErr: errors.New("destroy failed")}
	if err := failing.Destroy(context.Background()); err == nil || failing.DidDestroy() {
		t.Fatal("Destroy should surface DestroyErr without marking Destroyed")
	}
	if cfg, err := h.Config(); err != nil || cfg != nil {
		t.Fatalf("Config = %v, %v", cfg, err)
	}

	plan := &msbSdk.SandboxModificationPlan{}
	h.Plan = plan
	opts := msbSdk.ModifyOptions{}
	if got, err := h.Modify(context.Background(), opts); err != nil || got != plan {
		t.Fatalf("Modify = %v, %v", got, err)
	}
	if len(h.ModifiedOptions) != 1 {
		t.Fatalf("ModifiedOptions = %v", h.ModifiedOptions)
	}
}

func TestMockSandboxHandleCompact(t *testing.T) {
	h := &MockSandboxHandle{}
	result := &msbSdk.DiskCompactionResult{}
	h.CompactResult = result
	got, err := h.Compact(context.Background(), msbSdk.DiskCompactionOptions{DryRun: true})
	if err != nil || got != result {
		t.Fatalf("Compact = %v, %v", got, err)
	}
	if len(h.CompactedOptions) != 1 || !h.CompactedOptions[0].DryRun {
		t.Fatalf("CompactedOptions = %v", h.CompactedOptions)
	}
	h.CompactErr = errors.New("c")
	if _, err := h.Compact(context.Background(), msbSdk.DiskCompactionOptions{}); err == nil {
		t.Fatal("expected CompactErr")
	}
}

func TestMockSandboxHandleConnectStart(t *testing.T) {
	ctx := context.Background()
	sb := &MockSandbox{Name_: "sb"}

	h := &MockSandboxHandle{ConnectSb: sb, StartSb: sb}
	if got, err := h.Connect(ctx); err != nil || got != sb {
		t.Fatalf("Connect = %v, %v", got, err)
	}
	if got, err := h.Start(ctx); err != nil || got != sb {
		t.Fatalf("Start = %v, %v", got, err)
	}
	startFnCalled := false
	h.StartFn = func(gotCtx context.Context) (Sandbox, error) {
		startFnCalled = gotCtx != nil
		return sb, nil
	}
	if got, err := h.Start(ctx); err != nil || got != sb || !startFnCalled {
		t.Fatalf("Start via fn = %v, %v, called=%v", got, err, startFnCalled)
	}

	hErr := &MockSandboxHandle{ConnectErr: errors.New("e"), StartErr: errors.New("e2")}
	if _, err := hErr.Connect(ctx); err == nil {
		t.Fatal("expected ConnectErr")
	}
	if _, err := hErr.Start(ctx); err == nil {
		t.Fatal("expected StartErr")
	}

	def := &MockSandboxHandle{Name_: "x"}
	if got, err := def.Connect(ctx); err != nil || got == nil {
		t.Fatalf("Connect default = %v, %v", got, err)
	}
	if got, err := def.Start(ctx); err != nil || got == nil {
		t.Fatalf("Start default = %v, %v", got, err)
	}
}

func TestMockSandboxHandleErrors(t *testing.T) {
	ctx := context.Background()
	h := &MockSandboxHandle{StopErr: errors.New("s"), KillErr: errors.New("k"), RemoveErr: errors.New("r")}
	if err := h.Stop(ctx); err == nil {
		t.Fatal("expected StopErr")
	}
	if err := h.Kill(ctx); err == nil {
		t.Fatal("expected KillErr")
	}
	if err := h.Remove(ctx); err == nil {
		t.Fatal("expected RemoveErr")
	}
	if h.DidRmv {
		t.Fatal("Remove with error should not mark DidRmv")
	}
	h.ModifyErr = errors.New("m")
	if _, err := h.Modify(ctx, msbSdk.ModifyOptions{}); err == nil {
		t.Fatal("expected ModifyErr")
	}
}

func TestMockSandboxShellExec(t *testing.T) {
	ctx := context.Background()
	calls := &[]string{}
	success := NewTestResult(true, 0, "out", "", nil)
	fail := NewTestResult(false, 1, "", "err", nil)

	m := &MockSandbox{
		ShellOut:   map[string]ShellResult{"ok": success},
		ExecOut:    map[string]ShellResult{"cat foo": fail},
		ShellCalls: calls,
	}
	execCalls := &[]string{}
	m.ExecCalls = execCalls

	if got, err := m.Shell(ctx, "ok"); err != nil || got != success {
		t.Fatalf("Shell = %v, %v", got, err)
	}
	if len(*calls) != 1 || (*calls)[0] != "ok" {
		t.Fatalf("ShellCalls = %v", *calls)
	}
	if got, err := m.Shell(ctx, "default"); err != nil || got == nil {
		t.Fatalf("Shell default = %v, %v", got, err)
	}

	if got, err := m.Exec(ctx, "cat", []string{"foo"}); err != nil || got != fail {
		t.Fatalf("Exec = %v, %v", got, err)
	}
	if got, err := m.Exec(ctx, "echo", []string{"hi"}); err != nil || got == nil {
		t.Fatalf("Exec default = %v, %v", got, err)
	}
	if want := []string{"cat foo", "echo hi"}; !reflect.DeepEqual(*execCalls, want) {
		t.Fatalf("ExecCalls = %v, want %v", *execCalls, want)
	}

	errM := &MockSandbox{ShellErr: errors.New("s"), ExecErr: errors.New("e")}
	if _, err := errM.Shell(ctx, "x"); err == nil {
		t.Fatal("expected ShellErr")
	}
	if _, err := errM.Exec(ctx, "x", nil); err == nil {
		t.Fatal("expected ExecErr")
	}
}

func TestMockSandboxAttach(t *testing.T) {
	ctx := context.Background()
	m := &MockSandbox{AttachCode: 7, AttachErr: errors.New("a")}
	if code, err := m.Attach(ctx, "sh"); code != 7 || err == nil {
		t.Fatalf("Attach = %d, %v", code, err)
	}

	m2 := &MockSandbox{AttachCode: 3}
	if code, err := m2.AttachWith(ctx, "ls", []string{"-l"}, msbSdk.WithAttachUser("root")); code != 3 || err != nil {
		t.Fatalf("AttachWith = %d, %v", code, err)
	}
	if m2.AttachCmd != "ls" || len(m2.AttachArgs) != 1 || m2.AttachArgs[0] != "-l" || m2.AttachUser != "root" {
		t.Fatalf("AttachWith captured = %q %v user=%q", m2.AttachCmd, m2.AttachArgs, m2.AttachUser)
	}

	m3 := &MockSandbox{}
	if code, err := m3.AttachWith(ctx, "pwd", nil); code != 0 || err != nil {
		t.Fatalf("AttachWith no opts = %d, %v", code, err)
	}
}

func TestMockSandboxDetachStopClose(t *testing.T) {
	ctx := context.Background()
	m := &MockSandbox{DetachErr: errors.New("d"), StopErr: errors.New("st"), CloseErr: errors.New("c")}
	if err := m.Detach(ctx); err == nil {
		t.Fatal("expected DetachErr")
	}
	if err := m.Stop(ctx); err == nil {
		t.Fatal("expected StopErr")
	}
	if err := m.Close(); err == nil {
		t.Fatal("expected CloseErr")
	}

	ok := &MockSandbox{}
	if err := ok.Detach(ctx); err != nil {
		t.Fatalf("Detach = %v", err)
	}
	if err := ok.Stop(ctx); err != nil {
		t.Fatalf("Stop = %v", err)
	}
	if err := ok.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
}

func TestMockSandboxFS(t *testing.T) {
	fs := &TestFS{}
	m := &MockSandbox{FSValue_: fs}
	if got := m.FS(); got != fs {
		t.Fatalf("FS = %v, want %v", got, fs)
	}
	// Fallback to TestFS when FSValue_ is not a SandboxFS.
	m2 := &MockSandbox{FSValue_: "not-an-fs"}
	if got := m2.FS(); got == nil {
		t.Fatal("FS fallback returned nil")
	}
	m3 := &MockSandbox{}
	if got := m3.FS(); got == nil {
		t.Fatal("FS default returned nil")
	}
}

func TestNewMockSandbox(t *testing.T) {
	sb := NewMockSandbox(SandboxOpts{AttachCode: 9})
	ms, ok := sb.(*MockSandbox)
	if !ok {
		t.Fatalf("NewMockSandbox returned %T", sb)
	}
	if ms.AttachCode != 9 {
		t.Fatalf("AttachCode = %d, want 9", ms.AttachCode)
	}
}

func TestTestFS(t *testing.T) {
	ctx := context.Background()
	fs := NewTestFS(map[string][]byte{"a.txt": []byte("hello")}, []msbSdk.FsEntry{})
	if fs == nil {
		t.Fatal("NewTestFS returned nil")
	}

	if ok, err := fs.Exists(ctx, "a.txt"); err != nil || !ok {
		t.Fatalf("Exists = %v, %v", ok, err)
	}
	if ok, _ := fs.Exists(ctx, "b.txt"); ok {
		t.Fatal("Exists(b.txt) should be false")
	}
	if stat, err := fs.Stat(ctx, "a.txt"); err != nil || stat == nil {
		t.Fatalf("Stat = %v, %v", stat, err)
	}
	if list, err := fs.List(ctx, "/"); err != nil || list == nil {
		t.Fatalf("List = %v, %v", list, err)
	}
	if s, err := fs.ReadString(ctx, "a.txt"); err != nil || s != "hello" {
		t.Fatalf("ReadString = %q, %v", s, err)
	}
	if _, err := fs.ReadString(ctx, "missing"); err == nil {
		t.Fatal("ReadString(missing) should error")
	}
	if stream, err := fs.ReadStream(ctx, "a.txt"); err != nil || stream == nil {
		t.Fatalf("ReadStream = %v, %v", stream, err)
	}
	if err := fs.Mkdir(ctx, "/dir"); err != nil {
		t.Fatalf("Mkdir = %v", err)
	}
	if len(fs.Mkdirs) != 1 || fs.Mkdirs[0] != "/dir" {
		t.Fatalf("Mkdirs = %v", fs.Mkdirs)
	}
	if err := fs.Write(ctx, "out.txt", []byte("data")); err != nil {
		t.Fatalf("Write = %v", err)
	}
	if string(fs.Writes["out.txt"]) != "data" {
		t.Fatalf("Writes = %v", fs.Writes)
	}
	if d, err := fs.Read(ctx, "a.txt"); err != nil || string(d) != "hello" {
		t.Fatalf("Read = %q, %v", d, err)
	}
	if _, err := fs.Read(ctx, "missing"); err == nil {
		t.Fatal("Read(missing) should error")
	}
	if err := fs.Remove(ctx, "a.txt"); err != nil {
		t.Fatalf("Remove = %v", err)
	}
}

func TestTestFSErrors(t *testing.T) {
	ctx := context.Background()
	fs := &TestFS{
		ListErr:  errors.New("l"),
		ReadErr:  errors.New("r"),
		WriteErr: errors.New("w"),
		MkdirErr: errors.New("m"),
	}
	if _, err := fs.List(ctx, "/"); err == nil {
		t.Fatal("expected ListErr")
	}
	if _, err := fs.ReadString(ctx, "x"); err == nil {
		t.Fatal("expected ReadErr")
	}
	if _, err := fs.ReadStream(ctx, "x"); err == nil {
		t.Fatal("expected ReadErr on ReadStream")
	}
	if err := fs.Write(ctx, "x", []byte("d")); err == nil {
		t.Fatal("expected WriteErr")
	}
	if err := fs.Mkdir(ctx, "/d"); err == nil {
		t.Fatal("expected MkdirErr")
	}
	if _, err := fs.Read(ctx, "x"); err == nil {
		t.Fatal("expected ReadErr on Read")
	}
}

func TestTestResult(t *testing.T) {
	r := NewTestResult(true, 0, "out", "err", []byte("bytes"))
	if !r.Success() || r.ExitCode() != 0 || r.Stdout() != "out" || r.Stderr() != "err" ||
		string(r.StdoutBytes()) != "bytes" {
		t.Fatalf("TestResult accessors wrong: %+v", r)
	}
	fallback := NewTestResult(false, 2, "so", "", nil)
	if string(fallback.StdoutBytes()) != "so" {
		t.Fatalf("StdoutBytes fallback = %q, want %q", fallback.StdoutBytes(), "so")
	}
}

func TestMockVolumeHandle(t *testing.T) {
	q := uint32(100)
	capacity := uint64(1000)
	fmt := "raw"
	fst := "ext4"
	v := &MockVolumeHandle{
		Name_: "v", Path_: "/p", Kind_: msbSdk.VolumeKindDir,
		CreatedAt_: time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC), IsDefault_: true,
		QuotaMiB_: &q, UsedBytes_: 5, CapacityBytes_: &capacity, DiskFormat_: &fmt, DiskFstype_: &fst,
		Labels_: map[string]string{"k": "v"},
	}
	if v.Name() != "v" || v.Path() != "/p" || v.Kind() != msbSdk.VolumeKindDir ||
		!v.IsDefault() || v.UsedBytes() != 5 || v.QuotaMiB() != &q ||
		v.CapacityBytes() != &capacity || v.DiskFormat() != &fmt || v.DiskFstype() != &fst ||
		v.Labels()["k"] != "v" || !v.CreatedAt().Equal(time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("MockVolumeHandle accessors wrong")
	}

	def := &MockVolumeHandle{}
	if def.Kind() != msbSdk.VolumeKindDir {
		t.Fatalf("Kind default = %v, want VolumeKindDir", def.Kind())
	}
}

func TestMockImageHandle(t *testing.T) {
	sz := int64(42)
	now := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	h := MockImageHandle{Reference_: "r", ManifestDigest_: "d", LastUsedAt_: now, SizeBytes_: &sz, CreatedAt_: now}
	if h.Reference() != "r" || h.ManifestDigest() != "d" || !h.LastUsedAt().Equal(now) ||
		h.SizeBytes() != &sz || !h.CreatedAt().Equal(now) {
		t.Fatalf("MockImageHandle accessors wrong")
	}
}

func TestWithMsbMockAndResetGetFn(t *testing.T) {
	prevGet := Get
	defer func() { Get = prevGet }()

	mock := &MockMsbClient{}

	WithMsbMock(t, mock)
	if Get() != mock {
		t.Fatal("Get should return mock after WithMsbMock")
	}

	prev := ResetGetFn(func() Client { return mock })
	if prev == nil {
		t.Fatal("ResetGetFn should return previous factory")
	}
	ResetGetFn(prev)
}

func TestFailFastClientPanics(t *testing.T) {
	ctx := context.Background()
	f := &failFastMsbClient{}
	cases := []struct {
		name string
		fn   func()
	}{
		{"GetSandbox", func() { _, _ = f.GetSandbox(ctx, "x") }},
		{"CreateSandbox", func() { _, _ = f.CreateSandbox(ctx, "x") }},
		{"ListSandboxes", func() { _, _ = f.ListSandboxes(ctx, nil) }},
		{"GetVolume", func() { _, _ = f.GetVolume(ctx, "x") }},
		{"CreateVolume", func() { _, _ = f.CreateVolume(ctx, "x") }},
		{"ListVolumes", func() { _, _ = f.ListVolumes(ctx) }},
		{"RemoveVolume", func() { _ = f.RemoveVolume(ctx, "x") }},
		{"ImageGet", func() { _ = f.ImageGet(ctx, "x") }},
		{"ImageList", func() { _, _ = f.ImageList(ctx) }},
		{"ImageRemove", func() { _ = f.ImageRemove(ctx, "x", false) }},
		{"ImageLoad", func() { _ = f.ImageLoad(ctx, "x", strings.NewReader("")) }},
		{"ImageInspect", func() { _, _ = f.ImageInspect(ctx, "x") }},
		{"PruneStorage", func() { _, _ = f.PruneStorage(ctx, msbSdk.StoragePruneOptions{}) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic from fail-fast client")
				}
			}()
			tc.fn()
		})
	}
}

func TestMockSandboxHandleConfigMounts(t *testing.T) {
	want := map[string]msbSdk.MountConfig{
		"/tmp": msbSdk.Mount.Tmpfs(msbSdk.TmpfsOptions{SizeMiB: 2048}),
	}
	m := &MockSandboxHandle{Mounts: want}
	got, err := m.ConfigMounts()
	if err != nil {
		t.Fatalf("ConfigMounts() error = %v", err)
	}
	if got["/tmp"].SizeMiB != 2048 {
		t.Errorf("ConfigMounts() = %+v, want the seeded mounts", got)
	}

	failing := &MockSandboxHandle{MountsErr: errors.New("mounts boom")}
	if _, err := failing.ConfigMounts(); err == nil {
		t.Error("ConfigMounts() error = nil, want the configured MountsErr")
	}
}
