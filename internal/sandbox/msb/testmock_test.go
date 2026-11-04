package msb

import (
	"context"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

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

func TestMockSandboxShellStream(t *testing.T) {
	m := NewMockSandbox(SandboxOpts{})
	got, err := m.ShellStream(context.Background(), "curl -N http://x")
	if err != nil {
		t.Fatalf("ShellStream() error = %v", err)
	}
	if got == nil {
		t.Fatal("ShellStream() returned nil handle")
	}
	// The default (no StreamHandle_ configured) reports stream-closed on Recv.
	if _, err := got.Recv(context.Background()); err == nil {
		t.Error("empty handle Recv should report stream closed")
	}
}

func TestMockSandboxShellStreamReturnsConfiguredHandle(t *testing.T) {
	var cmds []string
	stub := &streamHandleStub{events: []StreamEvent{{Kind: StreamEventStdout, Data: []byte("x")}}}
	m := NewMockSandbox(SandboxOpts{StreamHandle_: stub, ShellStreamCmds: &cmds})
	got, err := m.ShellStream(context.Background(), "curl -N http://y")
	if err != nil {
		t.Fatalf("ShellStream() error = %v", err)
	}
	if got != stub {
		t.Fatal("ShellStream() did not return the configured StreamHandle_")
	}
	if len(cmds) != 1 || cmds[0] != "curl -N http://y" {
		t.Errorf("ShellStreamCmds = %v, want the command recorded", cmds)
	}
	ev, err := got.Recv(context.Background())
	if err != nil || ev.Kind != StreamEventStdout || string(ev.Data) != "x" {
		t.Errorf("Recv = %+v, %v; want the configured stdout event", ev, err)
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
	if err := fs.Remove(ctx, "a.txt"); err != nil || !slices.Equal(fs.Removed, []string{"a.txt"}) {
		t.Fatalf("Remove = %v, removed %v; want a.txt recorded", err, fs.Removed)
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

func TestTestResult(t *testing.T) {
	r := NewTestResult(true, 0, "out", "err", []byte("bytes"))
	if !r.Success() || r.ExitCode() != 0 || r.Stdout() != "out" || r.Stderr() != "err" ||
		string(r.StdoutBytes()) != "bytes" {
		t.Fatalf("TestResult accessors wrong: %+v", r)
	}
	fallback := NewTestResult(false, 2, "so", "", nil)
	if fallback.Success() || fallback.ExitCode() != 2 || string(fallback.StdoutBytes()) != "so" {
		t.Fatalf("failed result = success %v, exit %d, stdout %q; want false, 2, so",
			fallback.Success(), fallback.ExitCode(), fallback.StdoutBytes())
	}
}

// TestWithMsbMock pins that WithMsbMock installs the mock as msb.Get for one
// test and restores the previous factory when the test ends.
func TestWithMsbMock(t *testing.T) {
	mock := &MockMsbClient{}
	t.Run("install", func(t *testing.T) {
		WithMsbMock(t, mock)
		if Get() != mock {
			t.Fatal("Get should return the mock after WithMsbMock")
		}
	})
	if Get() == mock {
		t.Error("WithMsbMock must restore the previous factory on cleanup")
	}
}

// TestFailFastClientPanics pins that every method of the fail-fast client
// panics, so a test that reaches msb without installing a mock fails loudly
// instead of touching the real runtime.
func TestFailFastClientPanics(t *testing.T) {
	ctx := context.Background()
	f := &failFastMsbClient{}
	cases := []struct {
		name string
		fn   func()
	}{
		{"EnsureInstalled", func() { _ = f.EnsureInstalled(ctx) }},
		{"GetSandbox", func() { _, _ = f.GetSandbox(ctx, "x") }},
		{"CreateSandbox", func() { _, _ = f.CreateSandbox(ctx, "x") }},
		{"ListSandboxes", func() { _, _ = f.ListSandboxes(ctx, nil) }},
		{"GetVolume", func() { _, _ = f.GetVolume(ctx, "x") }},
		{"CreateVolume", func() { _, _ = f.CreateVolume(ctx, "x") }},
		{"ListVolumes", func() { _, _ = f.ListVolumes(ctx) }},
		{"RemoveVolume", func() { _ = f.RemoveVolume(ctx, "x") }},
		{"ImageGet", func() { _ = f.ImageGet(ctx, "x") }},
		{"ImageList", func() { _, _ = f.ImageList(ctx) }},
		{"ImagePrune", func() { _, _ = f.ImagePrune(ctx) }},
		{"PruneStorage", func() { _, _ = f.PruneStorage(ctx, msbSdk.StoragePruneOptions{}) }},
		{"ImageRemove", func() { _ = f.ImageRemove(ctx, "x", false) }},
		{"ImageLoad", func() { _ = f.ImageLoad(ctx, "x", strings.NewReader("")) }},
		{"ImageInspect", func() { _, _ = f.ImageInspect(ctx, "x") }},
		{"ImageInspectDetail", func() { _, _ = f.ImageInspectDetail(ctx, "x") }},
		{"StorageUsage", func() { _, _ = f.StorageUsage(ctx) }},
		{"ShellStream", func() { _, _ = f.ShellStream(ctx, "cmd") }},
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

// TestInstallFailFastGet pins that installing the fail-fast client replaces
// whatever msb.Get returned, so msb.Get panics until a test installs a mock,
// and works once one is.
func TestInstallFailFastGet(t *testing.T) {
	t.Run("replaces a factory", func(t *testing.T) {
		WithMsbMock(t, &MockMsbClient{})
		InstallFailFastGet()
		defer func() {
			if recover() == nil {
				t.Fatal("msb.Get did not panic after InstallFailFastGet")
			}
		}()
		_ = Get().EnsureInstalled(context.Background())
	})
	t.Run("with a mock", func(t *testing.T) {
		WithMsbMock(t, &MockMsbClient{})
		if err := Get().EnsureInstalled(context.Background()); err != nil {
			t.Fatalf("EnsureInstalled through the mock: %v", err)
		}
	})
}

func TestWithCountingMsbMockCountsResolutionsAndRestores(t *testing.T) {
	mock := &MockMsbClient{}

	t.Run("install", func(t *testing.T) {
		resolutions := WithCountingMsbMock(t, mock)
		if *resolutions != 0 {
			t.Fatalf("resolutions before any Get = %d, want 0", *resolutions)
		}
		for range 2 {
			if Get() != mock {
				t.Fatal("Get should return the mock after WithCountingMsbMock")
			}
		}
		if *resolutions != 2 {
			t.Errorf("resolutions = %d, want 2", *resolutions)
		}
	})

	if Get() == mock {
		t.Error("WithCountingMsbMock must restore the previous factory on cleanup")
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

	m3 := (&MockMsbClient{}).SetGotSandbox(h)
	if got := m3.SetGotSandbox(nil); got != m3 {
		t.Fatal("SetGotSandbox should return the receiver")
	}
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
	if _, err := m.CreateSandbox(
		ctx,
		"second",
	); err != nil ||
		!slices.Equal(m.CreatedSandboxes, []string{"default", "second"}) {
		t.Fatalf("second CreateSandbox = %v, recorded %v; want every call recorded", err, m.CreatedSandboxes)
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
	m2 := (&MockMsbClient{Volumes: []VolumeHandle{v}}).SetGetVolumeErr(errors.New("fail"))
	if got := m2.SetGetVolumeErr(nil); got != m2 {
		t.Fatal("SetGetVolumeErr should return the receiver")
	}
	if got, err := m2.GetVolume(ctx, "vol"); err != nil || got != v {
		t.Fatalf("SetGetVolumeErr(nil) should restore the collection lookup, got %v, %v", got, err)
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

func TestMockSandboxAttach(t *testing.T) {
	ctx := context.Background()
	m := &MockSandbox{AttachCode: 7, AttachErr: errors.New("a")}
	if code, err := m.Attach(ctx, "sh"); code != 7 || err == nil {
		t.Fatalf("Attach = %d, %v", code, err)
	}

	m2 := &MockSandbox{AttachCode: 3}
	if code, err := m2.AttachWith(
		ctx, "ls", []string{"-l"}, msbSdk.WithAttachUser("root"),
		msbSdk.WithAttachEnv(map[string]string{"HOME": "/root"}),
	); code != 3 || err != nil {
		t.Fatalf("AttachWith = %d, %v", code, err)
	}
	if m2.AttachCmd != "ls" || len(m2.AttachArgs) != 1 || m2.AttachArgs[0] != "-l" || m2.AttachUser != "root" ||
		m2.AttachEnv["HOME"] != "/root" {
		t.Fatalf("AttachWith captured = %q %v user=%q env=%v", m2.AttachCmd, m2.AttachArgs, m2.AttachUser, m2.AttachEnv)
	}

	m3 := &MockSandbox{}
	if code, err := m3.AttachWith(ctx, "pwd", nil); code != 0 || err != nil {
		t.Fatalf("AttachWith no opts = %d, %v", code, err)
	}

	attachErr := errors.New("attach failed")
	m4 := &MockSandbox{AttachErr: attachErr}
	if _, err := m4.AttachWith(ctx, "pwd", nil, msbSdk.WithAttachCwd("/workspace")); !errors.Is(err, attachErr) {
		t.Fatalf("AttachWith error = %v, want %v", err, attachErr)
	}
	if m4.AttachCwd != "/workspace" {
		t.Errorf("AttachWith cwd = %q, want /workspace", m4.AttachCwd)
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
	// The read error wins even for a file that exists.
	fs.Contents = map[string][]byte{"x": []byte("present")}
	if _, err := fs.ReadString(ctx, "x"); !errors.Is(err, fs.ReadErr) {
		t.Fatalf("ReadString error = %v, want ReadErr", err)
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
	if _, err := fs.Read(ctx, "x"); !errors.Is(err, fs.ReadErr) {
		t.Fatalf("Read error = %v, want ReadErr", err)
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
	if def.Kind() != msbSdk.VolumeKindDir || def.IsDefault() {
		t.Fatalf("zero handle kind %v, default %v; want VolumeKindDir, false", def.Kind(), def.IsDefault())
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

func TestMockSandboxExecDetachedRecordsCall(t *testing.T) {
	calls := &[]MockDetachedCall{}
	sb := &MockSandbox{ExecDetachedCalls: calls}

	got, err := sb.ExecDetached(context.Background(), "dockerd", []string{"-H", "unix:///var/run/docker.sock"})
	if err != nil {
		t.Fatalf("ExecDetached() error = %v", err)
	}
	if got == nil {
		t.Fatal("ExecDetached() returned nil job")
	}
	if len(*calls) != 1 {
		t.Fatalf("ExecDetachedCalls len = %d, want 1", len(*calls))
	}
	if (*calls)[0].Cmd != "dockerd" || len((*calls)[0].Args) != 2 || (*calls)[0].Args[0] != "-H" {
		t.Fatalf("recorded call = %+v, want dockerd -H unix:///var/run/docker.sock", (*calls)[0])
	}
}

func TestMockSandboxExecDetachedReturnsConfiguredJobAndError(t *testing.T) {
	job := &MockJob{ID_: "job-1"}
	sb := &MockSandbox{Job_: job}
	got, err := sb.ExecDetached(context.Background(), "x", nil)
	if err != nil || got != job {
		t.Fatalf("ExecDetached() = (%v, %v), want (job-1, nil)", got, err)
	}

	wantErr := errors.New("exec failed")
	sb = &MockSandbox{ExecDetachedFn: func(
		context.Context, string, []string, ...msbSdk.JobExecOption,
	) (Job, error) {
		return nil, wantErr
	}}
	if _, err := sb.ExecDetached(context.Background(), "x", nil); !errors.Is(err, wantErr) {
		t.Fatalf("ExecDetached() error = %v, want %v", err, wantErr)
	}
}

func TestMockSandboxGetJobAndListJobs(t *testing.T) {
	job := &MockJob{ID_: "job-1"}
	sb := &MockSandbox{Job_: job, Jobs: []msbSdk.JobInfo{{ID: "job-1"}}}

	got, err := sb.GetJob(context.Background(), "job-1")
	if err != nil || got != job {
		t.Fatalf("GetJob() = (%v, %v), want (job-1, nil)", got, err)
	}
	infos, err := sb.ListJobs(context.Background())
	if err != nil || len(infos) != 1 || infos[0].ID != "job-1" {
		t.Fatalf("ListJobs() = (%v, %v), want one job-1", infos, err)
	}
}

func TestMockSandboxGetJobAndListJobsUseCallbacksAndDefaults(t *testing.T) {
	callbackJob := &MockJob{ID_: "fn-job"}
	wantListErr := errors.New("list fn boom")
	sb := &MockSandbox{
		GetJobFn: func(_ context.Context, id string) (Job, error) { return callbackJob, nil },
		ListJobsFn: func(context.Context, ...msbSdk.JobListOption) ([]msbSdk.JobInfo, error) {
			return nil, wantListErr
		},
	}
	got, err := sb.GetJob(context.Background(), "fn-job")
	if err != nil || got != callbackJob {
		t.Fatalf("GetJob() via callback = (%v, %v), want (fn-job, nil)", got, err)
	}
	if _, err := sb.ListJobs(context.Background()); !errors.Is(err, wantListErr) {
		t.Fatalf("ListJobs() via callback error = %v, want %v", err, wantListErr)
	}

	// Neither a callback nor a configured job: GetJob synthesizes one by id.
	defaultSB := &MockSandbox{}
	fallback, err := defaultSB.GetJob(context.Background(), "synthesized")
	if err != nil || fallback == nil || fallback.ID() != "synthesized" {
		t.Fatalf("GetJob() default = (%v, %v), want a job with id synthesized", fallback, err)
	}
}

func TestMockJobMethods(t *testing.T) {
	job := &MockJob{
		ID_:   "job-1",
		Info:  &msbSdk.JobInfo{ID: "job-1"},
		Logs_: []msbSdk.JobLogEntry{{Data: []byte("hello")}},
	}
	ctx := context.Background()

	if job.ID() != "job-1" {
		t.Errorf("ID() = %q, want job-1", job.ID())
	}
	info, err := job.Inspect(ctx)
	if err != nil || info == nil || info.ID != "job-1" {
		t.Fatalf("Inspect() = (%v, %v), want job-1", info, err)
	}
	logs, err := job.Logs(ctx, msbSdk.JobLogOptions{})
	if err != nil || len(logs) != 1 || string(logs[0].Data) != "hello" {
		t.Fatalf("Logs() = (%v, %v), want one hello entry", logs, err)
	}
	if err := job.Kill(ctx); err != nil || !job.DidKill {
		t.Fatalf("Kill() = %v, DidKill=%v; want nil, true", err, job.DidKill)
	}
	if err := job.Close(); err != nil || !job.DidClose {
		t.Fatalf("Close() = %v, DidClose=%v; want nil, true", err, job.DidClose)
	}
}

func TestMockJobInspectAndLogsCallbacks(t *testing.T) {
	ctx := context.Background()
	inspected := &msbSdk.JobInfo{ID: "callback"}
	logged := []msbSdk.JobLogEntry{{Data: []byte("from-callback")}}
	job := &MockJob{
		InspectFn: func(context.Context) (*msbSdk.JobInfo, error) { return inspected, nil },
		LogsFn: func(context.Context, msbSdk.JobLogOptions) ([]msbSdk.JobLogEntry, error) {
			return logged, nil
		},
	}

	info, err := job.Inspect(ctx)
	if err != nil || info != inspected {
		t.Fatalf("Inspect() via callback = (%v, %v), want (%v, nil)", info, err, inspected)
	}
	logs, err := job.Logs(ctx, msbSdk.JobLogOptions{})
	if err != nil || len(logs) != 1 || string(logs[0].Data) != "from-callback" {
		t.Fatalf("Logs() via callback = (%v, %v), want one from-callback entry", logs, err)
	}
}

// TestMockJobErrorFields verifies each MockJob method returns its configured
// error, so a caller can simulate a failing Inspect/Kill/Logs/Close.
func TestMockJobErrorFields(t *testing.T) {
	ctx := context.Background()
	inspectErr := errors.New("inspect boom")
	logsErr := errors.New("logs boom")
	killErr := errors.New("kill boom")
	closeErr := errors.New("close boom")
	job := &MockJob{InfoErr: inspectErr, LogsErr: logsErr, KillErr: killErr, CloseErr: closeErr}

	if _, err := job.Inspect(ctx); !errors.Is(err, inspectErr) {
		t.Fatalf("Inspect() error = %v, want %v", err, inspectErr)
	}
	if _, err := job.Logs(ctx, msbSdk.JobLogOptions{}); !errors.Is(err, logsErr) {
		t.Fatalf("Logs() error = %v, want %v", err, logsErr)
	}
	if err := job.Kill(ctx); !errors.Is(err, killErr) {
		t.Fatalf("Kill() error = %v, want %v", err, killErr)
	}
	if err := job.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("Close() error = %v, want %v", err, closeErr)
	}
}

func TestMockSandboxDestroy(t *testing.T) {
	sb := &MockSandbox{}
	if err := sb.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy() error = %v, want nil", err)
	}
	if !sb.DidDestroy() {
		t.Error("expected DidDestroy() true after a successful destroy")
	}

	failing := &MockSandbox{DestroyErr: errors.New("boom")}
	if err := failing.Destroy(context.Background()); err == nil {
		t.Fatal("expected DestroyErr from Destroy()")
	}
	if failing.DidDestroy() {
		t.Error("expected DidDestroy() false after a failed destroy")
	}
}

func TestMockSandboxHandleCreatedAtBackendKind(t *testing.T) {
	now := time.Date(2026, 8, 17, 10, 30, 0, 0, time.UTC)
	h := &MockSandboxHandle{CreatedAt_: now, BackendKind_: msbSdk.BackendLocal}
	if got := h.CreatedAt(); !got.Equal(now) {
		t.Errorf("CreatedAt() = %v, want %v", got, now)
	}
	if got := h.BackendKind(); got != msbSdk.BackendLocal {
		t.Errorf("BackendKind() = %v, want %v", got, msbSdk.BackendLocal)
	}
}

func TestMockSandboxHandleImageFallsBackToConfig(t *testing.T) {
	viaConfig := &MockSandboxHandle{Cfg: &msbSdk.SandboxConfig{Image: "agents-sandbox/runner:latest"}}
	if got := viaConfig.Image(); got != "agents-sandbox/runner:latest" {
		t.Errorf("Image() via Cfg = %q, want image from config", got)
	}
	explicit := &MockSandboxHandle{Image_: "img:direct"}
	if got := explicit.Image(); got != "img:direct" {
		t.Errorf("Image() explicit = %q, want img:direct", got)
	}
}

// TestMockSandboxDetach pins both detach modes: DetachErr alone, and DetachFn
// observing the context the detach runs under.
func TestMockSandboxDetach(t *testing.T) {
	detachErr := errors.New("detach failed")
	if err := (&MockSandbox{DetachErr: detachErr}).Detach(context.Background()); !errors.Is(err, detachErr) {
		t.Errorf("Detach() = %v, want %v", err, detachErr)
	}

	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "caller")
	var seenCallerValue any
	sb := &MockSandbox{DetachErr: detachErr, DetachFn: func(ctx context.Context) error {
		seenCallerValue = ctx.Value(contextKey{})
		return nil
	}}
	if err := sb.Detach(ctx); err != nil {
		t.Errorf("Detach() with DetachFn = %v, want DetachFn's nil", err)
	}
	if seenCallerValue != "caller" {
		t.Error("DetachFn did not receive the caller's context")
	}
}

// TestSetGetSandboxErrNil covers the else branch of SetGetSandboxErr clearing
// the override.
func TestSetGetSandboxErrNil(t *testing.T) {
	m := (&MockMsbClient{}).SetGetSandboxErr(errors.New("fail"))
	if got := m.SetGetSandboxErr(nil); got != m {
		t.Fatal("SetGetSandboxErr(nil) should return the receiver")
	}
	if m.GetSandboxFn != nil {
		t.Fatal("SetGetSandboxErr(nil) should clear GetSandboxFn")
	}
}

// TestMockSandboxHandleImageDefault covers the default branch of
// MockSandboxHandle.Image when no Image_ or Cfg is set.
func TestMockSandboxHandleImageDefault(t *testing.T) {
	h := &MockSandboxHandle{}
	if got := h.Image(); got != "" {
		t.Errorf("Image() = %q, want empty", got)
	}
}

// TestMockSandboxShellStreamFn covers the ShellStreamFn branch of
// MockSandbox.ShellStream.
func TestMockSandboxShellStreamFn(t *testing.T) {
	stub := &streamHandleStub{}
	m := &MockSandbox{
		ShellStreamFn: func(command string) (StreamHandle, error) {
			if command != "cmd" {
				t.Errorf("ShellStreamFn command = %q, want %q", command, "cmd")
			}
			return stub, nil
		},
	}
	got, err := m.ShellStream(context.Background(), "cmd")
	if err != nil {
		t.Fatalf("ShellStream() error = %v", err)
	}
	if got != stub {
		t.Fatal("ShellStream() did not return the ShellStreamFn handle")
	}
}

// TestEmptyStreamHandleClose covers emptyStreamHandle.Close.
func TestEmptyStreamHandleClose(t *testing.T) {
	var h StreamHandle = &emptyStreamHandle{}
	if err := h.Close(); err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}
}

// TestMockImageInspectDetail covers the mock's callback and default branches.
func TestMockImageInspectDetail(t *testing.T) {
	m := &MockMsbClient{ImageInspectDetailFn: func(_ context.Context, _ string) (*ImageDetail, error) {
		return &ImageDetail{Reference: "agents-sandbox/runner-x", ManifestDigest: "sha256:x"}, nil
	}}
	detail, err := m.ImageInspectDetail(context.Background(), "ref")
	if err != nil || detail.Reference != "agents-sandbox/runner-x" {
		t.Fatalf("ImageInspectDetail via fn = %+v, %v", detail, err)
	}

	got, err := (&MockMsbClient{}).ImageInspectDetail(context.Background(), "ref")
	if err != nil || got.Reference != "ref" {
		t.Fatalf("ImageInspectDetail default = %+v, %v", got, err)
	}
}

// TestMockSandboxHandleID covers the mock handle's stable-identity accessor.
func TestMockSandboxHandleID(t *testing.T) {
	h := &MockSandboxHandle{ID_: "sbx-123"}
	if got := h.ID(); got != "sbx-123" {
		t.Errorf("ID() = %q, want %q", got, "sbx-123")
	}
}

func TestMockVolumeHandleImplementsInterface(_ *testing.T) {
	var _ VolumeHandle = (*MockVolumeHandle)(nil)
	v := &MockVolumeHandle{}
	_ = v.IsDefault()
	_ = v.QuotaMiB()
	_ = v.UsedBytes()
	_ = v.CapacityBytes()
	_ = v.DiskFormat()
	_ = v.DiskFstype()
	_ = v.Labels()
}

func TestMockListSandboxesRecordsLabels(t *testing.T) {
	m := &MockMsbClient{}
	m.ListSandboxesFn = func(_ context.Context, labels map[string]string) ([]SandboxHandle, error) {
		if labels["k"] != "v" {
			t.Errorf("expected label k=v, got %v", labels)
		}
		return nil, nil
	}
	_, err := m.ListSandboxes(context.Background(), map[string]string{"k": "v"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMockImageInspectUsesFn(t *testing.T) {
	called := false
	m := &MockMsbClient{ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
		called = true
		return &msbSdk.ImageConfig{}, nil
	}}
	cfg, err := m.ImageInspect(context.Background(), "ref")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called || cfg == nil {
		t.Error("expected ImageInspectFn to be called and return a config")
	}
}

func TestMockStorageUsageReturnsReport(t *testing.T) {
	want := &msbSdk.StorageUsageReport{}
	m := &MockMsbClient{StorageUsageFn: func(context.Context) (*msbSdk.StorageUsageReport, error) {
		return want, nil
	}}
	got, err := m.StorageUsage(context.Background())
	if err != nil {
		t.Fatalf("StorageUsage() error = %v", err)
	}
	if got != want {
		t.Fatal("StorageUsage() did not return the callback report")
	}
}

func TestMockStorageUsageDefaultsAndError(t *testing.T) {
	m := &MockMsbClient{}
	got, err := m.StorageUsage(context.Background())
	if err != nil {
		t.Fatalf("StorageUsage() default error = %v", err)
	}
	if got == nil {
		t.Fatal("StorageUsage() default report = nil, want empty report")
	}

	m = (&MockMsbClient{}).SetStorageUsageErr(errors.New("boom"))
	if _, err := m.StorageUsage(context.Background()); err == nil {
		t.Fatal("StorageUsage() with SetStorageUsageErr = nil error, want boom")
	}
}

func TestMockImageHandleNewAccessors(t *testing.T) {
	bytes := int64(123456)
	h := MockImageHandle{
		Reference_:      "agents-sandbox/runner:latest",
		ManifestDigest_: "sha256:abc",
		SizeBytes_:      &bytes,
		CreatedAt_:      time.Date(2026, 8, 17, 10, 42, 36, 0, time.UTC),
	}
	if got := h.SizeBytes(); got == nil || *got != 123456 {
		t.Fatalf("SizeBytes() = %v, want 123456", got)
	}
	if want := "2026-08-17 10:42:36"; h.CreatedAt().Format("2006-01-02 15:04:05") != want {
		t.Fatalf("CreatedAt() = %v, want %s", h.CreatedAt(), want)
	}
	if h.Reference() != "agents-sandbox/runner:latest" || h.ManifestDigest() != "sha256:abc" {
		t.Fatalf("existing accessors broken: %s %s", h.Reference(), h.ManifestDigest())
	}
}

func TestMockSandboxHandleDestroyFn(t *testing.T) {
	called := false
	wantErr := errors.New("destroy boom")
	h := &MockSandboxHandle{DestroyFn: func(context.Context) error {
		called = true
		return wantErr
	}}
	if err := h.Destroy(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Destroy() error = %v, want %v", err, wantErr)
	}
	if !called {
		t.Fatal("Destroy() did not call DestroyFn")
	}
	if h.DidDestroy() {
		t.Error("Destroy() with a DestroyFn must not mark the handle destroyed")
	}
}

func TestMockSandboxHandleConfig(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: "agents-sandbox/runner-proj:opencode-latest"}
	h := &MockSandboxHandle{Cfg: cfg}
	got, err := h.Config()
	if err != nil || got != cfg {
		t.Fatalf("Config() = (%v, %v), want the configured config", got, err)
	}

	broken := &MockSandboxHandle{ConfigErr: errors.New("sandbox gone")}
	if _, err := broken.Config(); !errors.Is(err, broken.ConfigErr) {
		t.Fatalf("Config() error = %v, want the configured error", err)
	}
}

// TestMockMsbClientRecordsEveryCall pins that the recording methods keep every
// call, not only the first: tests assert the full sequence of removals and
// loads.
func TestMockMsbClientRecordsEveryCall(t *testing.T) {
	ctx := context.Background()
	m := &MockMsbClient{}
	for _, name := range []string{"a", "b"} {
		_ = m.RemoveVolume(ctx, name)
		_ = m.ImageRemove(ctx, name, false)
		_ = m.ImageLoad(ctx, name, strings.NewReader(""))
	}
	if !slices.Equal(m.RemovedVolumes, []string{"a", "b"}) ||
		len(m.RemovedImages) != 2 || m.RemovedImages[1].Ref != "b" ||
		!slices.Equal(m.LoadedImages, []string{"a", "b"}) {
		t.Errorf("recorded volumes %v, images %v, loads %v; want both calls each",
			m.RemovedVolumes, m.RemovedImages, m.LoadedImages)
	}
}

// TestMockMsbClientGetVolumeErr pins that GetVolumeErr fails the lookup even
// for a volume the mock holds.
func TestMockMsbClientGetVolumeErr(t *testing.T) {
	lookupErr := errors.New("lookup failed")
	m := &MockMsbClient{GetVolumeErr: lookupErr, Volumes: []VolumeHandle{&MockVolumeHandle{Name_: "vol"}}}
	if _, err := m.GetVolume(context.Background(), "vol"); !errors.Is(err, lookupErr) {
		t.Errorf("GetVolume error = %v, want %v", err, lookupErr)
	}
}
