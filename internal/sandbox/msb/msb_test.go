package msb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

func TestRuntimePreparationHelpers(t *testing.T) {
	previousGet := Get
	Get = realGetFactory
	t.Cleanup(func() { Get = previousGet })
	if !IsRealClient() {
		t.Fatal("default client should be real")
	}
	WithMsbMock(t, &MockMsbClient{})
	if IsRealClient() {
		t.Fatal("a mock client must not report as real")
	}
	Get = realGetFactory
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

func TestGetVMStatus(t *testing.T) {
	tests := []struct {
		name    string
		status  SandboxStatus
		want    VMStatusKind
		wantErr bool
	}{
		{"running", SandboxStatusRunning, VMStatusActive, false},
		{"draining", SandboxStatusDraining, VMStatusActive, false},
		{"paused", SandboxStatusPaused, VMStatusActive, false},
		{"starting", SandboxStatusStarting, VMStatusStarting, false},
		{"created", SandboxStatusCreated, VMStatusCreated, false},
		{"stopped", SandboxStatusStopped, VMStatusStopped, false},
		{"crashed", SandboxStatusCrashed, VMStatusStopped, false},
		{"unknown", SandboxStatus(""), VMStatusUnknown, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetVMStatus(tt.status)
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetVMStatus(%q) error = %v, wantErr %v", tt.status, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("GetVMStatus(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestIsSandboxActive(t *testing.T) {
	tests := []struct {
		name   string
		status SandboxStatus
		want   bool
	}{
		{"running", SandboxStatusRunning, true},
		{"draining", SandboxStatusDraining, true},
		{"paused", SandboxStatusPaused, true},
		{"starting", SandboxStatusStarting, true},
		{"created", SandboxStatusCreated, false},
		{"stopped", SandboxStatusStopped, false},
		{"crashed", SandboxStatusCrashed, false},
		{"unknown", SandboxStatus(""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSandboxActive(tt.status); got != tt.want {
				t.Errorf("IsSandboxActive(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestIsSandboxInactive(t *testing.T) {
	tests := []struct {
		name   string
		status SandboxStatus
		want   bool
	}{
		{"created", SandboxStatusCreated, true},
		{"stopped", SandboxStatusStopped, true},
		{"crashed", SandboxStatusCrashed, true},
		{"starting", SandboxStatusStarting, false},
		{"running", SandboxStatusRunning, false},
		{"draining", SandboxStatusDraining, false},
		{"paused", SandboxStatusPaused, false},
		{"unknown", SandboxStatus(""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSandboxInactive(tt.status); got != tt.want {
				t.Errorf("IsSandboxInactive(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestIsNotFound(t *testing.T) {
	notFound := &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "no such sandbox"}
	other := &msbSdk.Error{Kind: msbSdk.ErrSandboxAlreadyExists, Message: "exists"}
	plain := errors.New("some other failure")

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"not found kind", notFound, true},
		{"wrapped not found", fmt.Errorf("outer: %w", notFound), true},
		{"other kind", other, false},
		{"plain error", plain, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsNotFound(tt.err); got != tt.want {
				t.Errorf("IsNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsVolumeNotFound(t *testing.T) {
	volumeNotFound := &msbSdk.Error{Kind: msbSdk.ErrVolumeNotFound, Message: "no such volume"}
	sandboxNotFound := &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "no such sandbox"}
	plain := errors.New("some other failure")

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"volume not found kind", volumeNotFound, true},
		{"wrapped volume not found", fmt.Errorf("outer: %w", volumeNotFound), true},
		{"sandbox not found kind", sandboxNotFound, false},
		{"plain error", plain, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsVolumeNotFound(tt.err); got != tt.want {
				t.Errorf("IsVolumeNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsVolumeAlreadyExists(t *testing.T) {
	volumeExists := &msbSdk.Error{Kind: msbSdk.ErrVolumeAlreadyExists, Message: "volume exists"}
	sandboxExists := &msbSdk.Error{Kind: msbSdk.ErrSandboxAlreadyExists, Message: "sandbox exists"}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"volume already exists kind", volumeExists, true},
		{"wrapped volume already exists", fmt.Errorf("outer: %w", volumeExists), true},
		{"sandbox already exists kind", sandboxExists, false},
		{"plain error", errors.New("some other failure"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsVolumeAlreadyExists(tt.err); got != tt.want {
				t.Errorf("IsVolumeAlreadyExists(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsReplaced(t *testing.T) {
	replaced := &msbSdk.Error{Kind: msbSdk.ErrSandboxReplaced, Message: "handle was replaced"}
	other := &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "no such sandbox"}
	plain := errors.New("some other failure")

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"replaced kind", replaced, true},
		{"wrapped replaced", fmt.Errorf("outer: %w", replaced), true},
		{"other kind", other, false},
		{"plain error", plain, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsReplaced(tt.err); got != tt.want {
				t.Errorf("IsReplaced(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsSandboxAlreadyExists(t *testing.T) {
	exists := &msbSdk.Error{Kind: msbSdk.ErrSandboxAlreadyExists, Message: "sandbox already exists"}
	volumeExists := &msbSdk.Error{Kind: msbSdk.ErrVolumeAlreadyExists, Message: "volume already exists"}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"already-exists kind", exists, true},
		{"wrapped already-exists", fmt.Errorf("outer: %w", exists), true},
		{"volume already-exists kind", volumeExists, false},
		{"plain error", errors.New("some other failure"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSandboxAlreadyExists(tt.err); got != tt.want {
				t.Errorf("IsSandboxAlreadyExists(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestRealSandboxHandleConnectOrStart pins the adapter's boot report: the
// sandbox ConnectOrStart returns booted exactly when this call owns the VM
// process, a failed connect-or-start is returned as-is, and a sandbox whose
// ownership cannot be read is closed and reported instead of guessed.
func TestRealSandboxHandleConnectOrStart(t *testing.T) {
	originalConnectOrStart := realHandleConnectOrStart
	originalOwnsLifecycle := realSandboxOwnsLifecycle
	originalClose := realSandboxClose
	t.Cleanup(func() {
		realHandleConnectOrStart = originalConnectOrStart
		realSandboxOwnsLifecycle = originalOwnsLifecycle
		realSandboxClose = originalClose
	})

	live := &msbSdk.Sandbox{}
	cause := errors.New("lifecycle failed")
	cases := []struct {
		name            string
		connectErr      error
		owns            bool
		ownsErr         error
		wantBooted      bool
		wantErr         error
		wantClosed      bool
		wantOwnsChecked bool
	}{
		{name: "connected to a running VM", wantOwnsChecked: true},
		{name: "started the VM", owns: true, wantBooted: true, wantOwnsChecked: true},
		{name: "connect-or-start fails", connectErr: cause, wantErr: cause},
		{name: "ownership unreadable", ownsErr: cause, wantErr: cause, wantClosed: true, wantOwnsChecked: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ownsChecked, closed := false, false
			realHandleConnectOrStart = func(
				*msbSdk.SandboxHandle,
				context.Context,
				...msbSdk.ConnectOrStartOption,
			) (*msbSdk.Sandbox, error) {
				if tc.connectErr != nil {
					return nil, tc.connectErr
				}
				return live, nil
			}
			realSandboxOwnsLifecycle = func(sb *msbSdk.Sandbox) (bool, error) {
				ownsChecked = sb == live
				return tc.owns, tc.ownsErr
			}
			realSandboxClose = func(sb *msbSdk.Sandbox) error {
				closed = sb == live
				return nil
			}

			sb, booted, err := realSandboxHandle{handle: &msbSdk.SandboxHandle{}}.ConnectOrStart(context.Background())

			wantSandbox := Sandbox(nil)
			if tc.wantErr == nil {
				wantSandbox = realSandbox{sandbox: live}
			}
			if !errors.Is(err, tc.wantErr) || sb != wantSandbox || booted != tc.wantBooted {
				t.Fatalf("ConnectOrStart = %v, %v, %v; want %v, %v, %v",
					sb, booted, err, wantSandbox, tc.wantBooted, tc.wantErr)
			}
			if ownsChecked != tc.wantOwnsChecked {
				t.Errorf("ownership read = %v, want %v", ownsChecked, tc.wantOwnsChecked)
			}
			if closed != tc.wantClosed {
				t.Errorf("closed = %v, want %v", closed, tc.wantClosed)
			}
		})
	}
}

func TestIsUnsupportedOperation(t *testing.T) {
	err := &msbSdk.Error{Kind: msbSdk.ErrUnsupportedOperation, Message: "nope"}
	if !IsUnsupportedOperation(err) {
		t.Error("IsUnsupportedOperation(direct) = false, want true")
	}
	if !IsUnsupportedOperation(fmt.Errorf("wrap: %w", err)) {
		t.Error("IsUnsupportedOperation(wrapped) = false, want true")
	}
	if IsUnsupportedOperation(&msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound}) {
		t.Error("IsUnsupportedOperation(other kind) = true, want false")
	}
	if IsUnsupportedOperation(nil) {
		t.Error("IsUnsupportedOperation(nil) = true, want false")
	}
}

func TestRealSandboxHandleOperationsReturnErrors(t *testing.T) {
	h := &realSandboxHandle{handle: &msbSdk.SandboxHandle{}}
	ctx := context.Background()

	if sb, err := h.Connect(ctx); err == nil || sb != nil {
		t.Errorf("Connect() = %v, %v; want error on empty handle", sb, err)
	}
	if err := h.Stop(ctx, LifecycleOptions{}); err == nil {
		t.Error("Stop() = nil, want error on empty handle")
	}
	if err := h.Kill(ctx, LifecycleOptions{}); err == nil {
		t.Error("Kill() = nil, want error on empty handle")
	}
	if err := h.Remove(ctx); err == nil {
		t.Error("Remove() = nil, want error on empty handle")
	}
	if err := h.Destroy(ctx, LifecycleOptions{}); err == nil {
		t.Error("Destroy() = nil, want error on empty handle")
	}
	if result, err := h.Compact(ctx, CompactionRequest{}); !IsUnsupportedOperation(err) || result != nil {
		t.Errorf("Compact() = %v, %v; want unsupported-operation error on non-local handle", result, err)
	}
	if plan, err := h.Modify(ctx, msbSdk.ModifyOptions{}); err == nil || plan != nil {
		t.Errorf("Modify() = %v, %v; want error on empty handle", plan, err)
	}
}

func TestMapExecEvent(t *testing.T) {
	tests := []struct {
		name string
		ev   msbSdk.ExecEvent
		want StreamEvent
		ok   bool
		err  error
	}{
		{
			name: "stdout",
			ev:   msbSdk.ExecEvent{Kind: msbSdk.ExecEventStdout, Data: []byte("x")},
			want: StreamEvent{Kind: StreamEventStdout, Data: []byte("x")},
			ok:   true,
		},
		{
			name: "stderr",
			ev:   msbSdk.ExecEvent{Kind: msbSdk.ExecEventStderr, Data: []byte("e")},
			want: StreamEvent{Kind: StreamEventStderr, Data: []byte("e")},
			ok:   true,
		},
		{
			name: "exited",
			ev:   msbSdk.ExecEvent{Kind: msbSdk.ExecEventExited, ExitCode: 3},
			want: StreamEvent{Kind: StreamEventExited, ExitCode: 3},
			ok:   true,
		},
		{
			name: "failed",
			ev:   msbSdk.ExecEvent{Kind: msbSdk.ExecEventFailed},
			want: StreamEvent{Kind: StreamEventFailed},
			ok:   true,
		},
		{
			name: "done is io.EOF",
			ev:   msbSdk.ExecEvent{Kind: msbSdk.ExecEventDone},
			err:  io.EOF,
		},
		{
			name: "started is skipped",
			ev:   msbSdk.ExecEvent{Kind: msbSdk.ExecEventStarted},
			ok:   false,
		},
		{
			name: "stdin error is skipped",
			ev:   msbSdk.ExecEvent{Kind: msbSdk.ExecEventStdinError},
			ok:   false,
		},
		{
			name: "unknown kind is skipped",
			ev:   msbSdk.ExecEvent{Kind: msbSdk.ExecEventKind(9999)},
			ok:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := mapExecEvent(tt.ev)
			if !errors.Is(err, tt.err) {
				t.Fatalf("mapExecEvent() err = %v, want %v", err, tt.err)
			}
			if ok != tt.ok {
				t.Fatalf("mapExecEvent() ok = %v, want %v", ok, tt.ok)
			}
			if got.Kind != tt.want.Kind || string(got.Data) != string(tt.want.Data) ||
				got.ExitCode != tt.want.ExitCode {
				t.Errorf("mapExecEvent() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestRealStreamHandleRecvAndClose covers the streaming handle's Recv loop
// (skip payload-less events, return the first payload, surface errors and the
// clean-end io.EOF) and its Close delegation, all without a live VM.
func TestRealStreamHandleRecvAndClose(t *testing.T) {
	ctx := context.Background()

	t.Run("skips payload-less events then returns the payload", func(t *testing.T) {
		events := []msbSdk.ExecEvent{
			{Kind: msbSdk.ExecEventStarted},
			{Kind: msbSdk.ExecEventStdout, Data: []byte("hi")},
		}
		consumed := 0
		swapSeam(t, &realStreamRecv, func(_ *msbSdk.ExecHandle, _ context.Context) (*msbSdk.ExecEvent, error) {
			ev := events[consumed]
			consumed++
			return &ev, nil
		})
		got, err := (&realStreamHandle{}).Recv(ctx)
		if err != nil || got.Kind != StreamEventStdout || string(got.Data) != "hi" || consumed != 2 {
			t.Fatalf("Recv() = (%+v, %v), consumed=%d; want stdout \"hi\" after skipping Started", got, err, consumed)
		}
	})

	t.Run("done maps to io.EOF", func(t *testing.T) {
		swapSeam(t, &realStreamRecv, func(_ *msbSdk.ExecHandle, _ context.Context) (*msbSdk.ExecEvent, error) {
			return &msbSdk.ExecEvent{Kind: msbSdk.ExecEventDone}, nil
		})
		if _, err := (&realStreamHandle{}).Recv(ctx); !errors.Is(err, io.EOF) {
			t.Fatalf("Recv() error = %v, want io.EOF", err)
		}
	})

	t.Run("recv error propagates", func(t *testing.T) {
		wantErr := errors.New("recv boom")
		swapSeam(t, &realStreamRecv, func(_ *msbSdk.ExecHandle, _ context.Context) (*msbSdk.ExecEvent, error) {
			return nil, wantErr
		})
		if _, err := (&realStreamHandle{}).Recv(ctx); !errors.Is(err, wantErr) {
			t.Fatalf("Recv() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("Close delegates", func(t *testing.T) {
		called := false
		swapSeam(t, &realStreamClose, func(_ *msbSdk.ExecHandle) error {
			called = true
			return nil
		})
		if err := (&realStreamHandle{}).Close(); err != nil || !called {
			t.Fatalf("Close() = %v, called=%v; want nil, true", err, called)
		}
	})
}

func TestRealSandboxFS(t *testing.T) {
	sb := &realSandbox{sandbox: &msbSdk.Sandbox{}}
	fs := sb.FS()
	if fs == nil {
		t.Fatal("FS() returned nil")
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

func TestRealSandboxDetachedDelegates(t *testing.T) {
	ctx := context.Background()

	t.Run("ExecDetached", func(t *testing.T) {
		original := realSandboxExecDetached
		t.Cleanup(func() { realSandboxExecDetached = original })
		called := false
		realSandboxExecDetached = func(
			_ *msbSdk.Sandbox, _ context.Context, cmd string, args []string, _ ...msbSdk.JobExecOption,
		) (*msbSdk.Job, error) {
			called = true
			if cmd != "dockerd" || len(args) != 1 {
				t.Errorf("delegated (%q, %v), want dockerd [x]", cmd, args)
			}
			return &msbSdk.Job{}, nil
		}
		got, err := (realSandbox{}).ExecDetached(ctx, "dockerd", []string{"x"}, JobSpec{})
		if err != nil || !called {
			t.Fatalf("ExecDetached() = %v, called=%v; want nil, true", err, called)
		}
		if got == nil {
			t.Fatal("ExecDetached() returned a nil job")
		}
	})

	t.Run("ExecDetached error", func(t *testing.T) {
		original := realSandboxExecDetached
		t.Cleanup(func() { realSandboxExecDetached = original })
		wantErr := errors.New("boom")
		realSandboxExecDetached = func(
			_ *msbSdk.Sandbox, _ context.Context, _ string, _ []string, _ ...msbSdk.JobExecOption,
		) (*msbSdk.Job, error) {
			return nil, wantErr
		}
		if _, err := (realSandbox{}).ExecDetached(ctx, "x", nil, JobSpec{}); !errors.Is(err, wantErr) {
			t.Fatalf("ExecDetached() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("GetJob", func(t *testing.T) {
		original := realSandboxGetJob
		t.Cleanup(func() { realSandboxGetJob = original })
		called := false
		realSandboxGetJob = func(_ *msbSdk.Sandbox, _ context.Context, id string) (*msbSdk.Job, error) {
			called = true
			if id != "job-1" {
				t.Errorf("delegated id = %q, want job-1", id)
			}
			return &msbSdk.Job{}, nil
		}
		got, err := (realSandbox{}).GetJob(ctx, "job-1")
		if err != nil || !called {
			t.Fatalf("GetJob() = %v, called=%v; want nil, true", err, called)
		}
		if got == nil {
			t.Fatal("GetJob() returned a nil job")
		}
	})

	t.Run("GetJob error", func(t *testing.T) {
		original := realSandboxGetJob
		t.Cleanup(func() { realSandboxGetJob = original })
		wantErr := errors.New("get boom")
		realSandboxGetJob = func(_ *msbSdk.Sandbox, _ context.Context, _ string) (*msbSdk.Job, error) {
			return nil, wantErr
		}
		if _, err := (realSandbox{}).GetJob(ctx, "job-1"); !errors.Is(err, wantErr) {
			t.Fatalf("GetJob() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("ListJobs paginates", func(t *testing.T) {
		original := realSandboxListJobs
		t.Cleanup(func() { realSandboxListJobs = original })
		var optsSeen []int
		realSandboxListJobs = func(_ *msbSdk.Sandbox, _ context.Context, opts ...msbSdk.JobListOption) (*msbSdk.JobPage, error) {
			optsSeen = append(optsSeen, len(opts))
			switch len(optsSeen) {
			case 1:
				next := "c1"
				return &msbSdk.JobPage{Items: []msbSdk.JobInfo{{ID: "a"}}, NextCursor: &next}, nil
			default:
				return &msbSdk.JobPage{Items: []msbSdk.JobInfo{{ID: "b"}}}, nil
			}
		}
		got, err := (realSandbox{}).ListJobs(ctx, JobQuery{})
		if err != nil || len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
			t.Fatalf("ListJobs() = (%v, err=%v), want [a b] over 2 pages", got, err)
		}
		// Page 1 carries no cursor option; page 2 must carry exactly the cursor.
		if len(optsSeen) != 2 || optsSeen[0] != 0 || optsSeen[1] != 1 {
			t.Fatalf("per-page option counts = %v, want [0 1] so page 2 sends the cursor", optsSeen)
		}
	})

	t.Run("ListJobs stops on an empty cursor", func(t *testing.T) {
		original := realSandboxListJobs
		t.Cleanup(func() { realSandboxListJobs = original })
		empty := ""
		calls := 0
		realSandboxListJobs = func(_ *msbSdk.Sandbox, _ context.Context, _ ...msbSdk.JobListOption) (*msbSdk.JobPage, error) {
			calls++
			return &msbSdk.JobPage{Items: []msbSdk.JobInfo{{ID: "a"}}, NextCursor: &empty}, nil
		}
		got, err := (realSandbox{}).ListJobs(ctx, JobQuery{})
		if err != nil || len(got) != 1 || got[0].ID != "a" {
			t.Fatalf("ListJobs() = (%v, err=%v), want [a]", got, err)
		}
		if calls != 1 {
			t.Fatalf("ListJobs made %d calls; an empty cursor must terminate after the first page", calls)
		}
	})

	t.Run("ListJobs error", func(t *testing.T) {
		original := realSandboxListJobs
		t.Cleanup(func() { realSandboxListJobs = original })
		wantErr := errors.New("list failed")
		realSandboxListJobs = func(
			_ *msbSdk.Sandbox, _ context.Context, _ ...msbSdk.JobListOption,
		) (*msbSdk.JobPage, error) {
			return nil, wantErr
		}
		if _, err := (realSandbox{}).ListJobs(ctx, JobQuery{}); !errors.Is(err, wantErr) {
			t.Fatalf("ListJobs() error = %v, want %v", err, wantErr)
		}
	})
}

func TestRealJobDelegates(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("job failed")

	t.Run("Inspect", func(t *testing.T) {
		original := realJobInspect
		t.Cleanup(func() { realJobInspect = original })
		want := &msbSdk.JobInfo{ID: "job-1"}
		realJobInspect = func(_ *msbSdk.Job, _ context.Context) (*msbSdk.JobInfo, error) {
			return want, nil
		}
		got, err := (realJob{}).Inspect(ctx)
		if err != nil || got == nil || got.ID != want.ID {
			t.Fatalf("Inspect() = (%v, %v), want (%v, nil)", got, err, want)
		}
	})

	t.Run("Inspect error", func(t *testing.T) {
		swapSeam(t, &realJobInspect, func(_ *msbSdk.Job, _ context.Context) (*msbSdk.JobInfo, error) {
			return nil, wantErr
		})
		if _, err := (realJob{}).Inspect(ctx); !errors.Is(err, wantErr) {
			t.Fatalf("Inspect() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("Kill", func(t *testing.T) {
		original := realJobKill
		t.Cleanup(func() { realJobKill = original })
		called := false
		realJobKill = func(_ *msbSdk.Job, _ context.Context) error { called = true; return nil }
		if err := (realJob{}).Kill(ctx); err != nil || !called {
			t.Fatalf("Kill() = %v, called=%v; want nil, true", err, called)
		}
	})

	t.Run("Logs", func(t *testing.T) {
		original := realJobLogs
		t.Cleanup(func() { realJobLogs = original })
		called := false
		realJobLogs = func(
			_ *msbSdk.Job, _ context.Context, _ msbSdk.JobLogOptions,
		) ([]msbSdk.JobLogEntry, error) {
			called = true
			return []msbSdk.JobLogEntry{{Data: []byte("x")}}, nil
		}
		got, err := (realJob{}).Logs(ctx, JobLogQuery{})
		if err != nil || !called || len(got) != 1 {
			t.Fatalf("Logs() = (%v, %v), called=%v; want one entry", got, err, called)
		}
	})

	t.Run("Logs error", func(t *testing.T) {
		swapSeam(t, &realJobLogs, func(
			_ *msbSdk.Job, _ context.Context, _ msbSdk.JobLogOptions,
		) ([]msbSdk.JobLogEntry, error) {
			return nil, wantErr
		})
		if _, err := (realJob{}).Logs(ctx, JobLogQuery{}); !errors.Is(err, wantErr) {
			t.Fatalf("Logs() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("Close", func(t *testing.T) {
		original := realJobClose
		t.Cleanup(func() { realJobClose = original })
		called := false
		realJobClose = func(_ *msbSdk.Job) error { called = true; return nil }
		if err := (realJob{}).Close(); err != nil || !called {
			t.Fatalf("Close() = %v, called=%v; want nil, true", err, called)
		}
	})
}

// TestGetFactory exercises the package-level Client factory, which the test
// hook normally overrides.
func TestGetFactory(t *testing.T) {
	c := realGetFactory()
	if _, ok := c.(*realMsbClient); !ok {
		t.Fatalf("Get() = %T, want *realMsbClient", c)
	}
}

// TestRealMsbClientCreateVolumeError covers the error branch of
// realMsbClient.CreateVolume (invalid empty name fails before touching a VM).
func TestRealMsbClientCreateVolumeError(t *testing.T) {
	c := &realMsbClient{}
	if _, err := c.CreateVolume(context.Background(), ""); err == nil {
		t.Error("CreateVolume() with empty name = nil error, want config error")
	}
}

// TestRealMsbClientImagePruneCanceled covers the adapter without pruning the
// shared microsandbox image cache used by the test environment.
func TestRealMsbClientImagePruneCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&realMsbClient{}).ImagePrune(ctx); err == nil {
		t.Fatal("ImagePrune() with a canceled context = nil error, want cancellation error")
	}
}

// TestRealMsbClientStorageUsageCanceled covers the StorageUsage adapter without
// a runtime: a canceled context fails before the native call.
func TestRealMsbClientStorageUsageCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&realMsbClient{}).StorageUsage(ctx); err == nil {
		t.Fatal("StorageUsage() with a canceled context = nil error, want cancellation error")
	}
}

// TestRealMsbClientPruneStorageSuccess pins that the PruneStorage adapter
// projects the request into the SDK prune options and translates the reclaimed
// totals onto the launcher report.
func TestRealMsbClientPruneStorageSuccess(t *testing.T) {
	ctx := context.Background()
	var gotOptions msbSdk.StoragePruneOptions
	physical := uint64(4096)
	swapSeam(
		t,
		&storagePrune,
		func(_ context.Context, options msbSdk.StoragePruneOptions) (*msbSdk.StoragePruneReport, error) {
			gotOptions = options
			return &msbSdk.StoragePruneReport{
				FilesRemoved:           3,
				LogicalBytesRemoved:    12288,
				PhysicalBytesReclaimed: &physical,
			}, nil
		},
	)

	report, err := (&realMsbClient{}).PruneStorage(ctx, PruneRequest{DryRun: true, OlderThanSeconds: 99})
	if err != nil {
		t.Fatalf("PruneStorage() error = %v, want nil", err)
	}
	if !gotOptions.DryRun || gotOptions.OlderThanSeconds != 99 {
		t.Errorf("PruneStorage() options = %+v, want DryRun true OlderThanSeconds 99", gotOptions)
	}
	if report == nil {
		t.Fatal("PruneStorage() = nil, want a report")
	}
	if report.FilesRemoved != 3 || report.LogicalBytesRemoved != 12288 {
		t.Errorf("PruneStorage() = %+v, want files 3 logical 12288", report)
	}
	if report.PhysicalBytesReclaimed == nil || *report.PhysicalBytesReclaimed != physical {
		t.Errorf("PruneStorage() physical = %v, want %d", report.PhysicalBytesReclaimed, physical)
	}
}

// TestRealMsbClientImageInspectInjected covers the success path of the
// ImageInspect adapter through the inspect seam.
func TestRealMsbClientImageInspectInjected(t *testing.T) {
	previous := realImageInspect
	t.Cleanup(func() { realImageInspect = previous })
	realImageInspect = func(_ context.Context, _ string) (*msbSdk.ImageDetail, error) {
		return &msbSdk.ImageDetail{Config: &msbSdk.ImageConfig{Digest: "sha256:abc"}}, nil
	}
	cfg, err := (&realMsbClient{}).ImageInspect(context.Background(), "ref")
	if err != nil || cfg == nil || cfg.Digest != "sha256:abc" {
		t.Fatalf("ImageInspect = %v, %v", cfg, err)
	}
}

// TestRealMsbClientImageInspectDetailInjected covers the success path of the
// ImageInspectDetail adapter through the inspect seam, mapping the SDK detail
// onto the local ImageDetail (including a nil config).
func TestRealMsbClientImageInspectDetailInjected(t *testing.T) {
	previous := realImageInspect
	t.Cleanup(func() { realImageInspect = previous })
	realImageInspect = func(_ context.Context, _ string) (*msbSdk.ImageDetail, error) {
		return &msbSdk.ImageDetail{
			Config: &msbSdk.ImageConfig{Digest: "sha256:abc"},
			Layers: []msbSdk.ImageLayer{{Position: 0, DiffID: "sha256:layer0"}},
		}, nil
	}
	detail, err := (&realMsbClient{}).ImageInspectDetail(context.Background(), "ref")
	if err != nil {
		t.Fatalf("ImageInspectDetail() error = %v", err)
	}
	if detail.Config.Digest != "sha256:abc" || len(detail.Layers) != 1 ||
		detail.Layers[0].DiffID != "sha256:layer0" {
		t.Fatalf("ImageInspectDetail() = %+v, want digest sha256:abc and layer sha256:layer0", detail)
	}
}

// TestRealMsbClientImageInspectDetailNilConfig covers the nil-config branch of
// the adapter, which leaves the zero-value config in place.
func TestRealMsbClientImageInspectDetailNilConfig(t *testing.T) {
	previous := realImageInspect
	t.Cleanup(func() { realImageInspect = previous })
	realImageInspect = func(_ context.Context, _ string) (*msbSdk.ImageDetail, error) {
		return &msbSdk.ImageDetail{}, nil
	}
	detail, err := (&realMsbClient{}).ImageInspectDetail(context.Background(), "ref")
	if err != nil || detail == nil {
		t.Fatalf("ImageInspectDetail() = %v, %v", detail, err)
	}
	if detail.Config.Digest != "" {
		t.Errorf("ImageInspectDetail() config = %+v, want zero value", detail.Config)
	}
}

// TestRealMsbClientImageInspectDetailHandleAndError covers the ImageHandle
// branch (metadata copied off the handle) and the inspect-error branch of the
// ImageInspectDetail adapter.
func TestRealMsbClientImageInspectDetailHandleAndError(t *testing.T) {
	previous := realImageInspect
	t.Cleanup(func() { realImageInspect = previous })

	realImageInspect = func(_ context.Context, _ string) (*msbSdk.ImageDetail, error) {
		return &msbSdk.ImageDetail{
			ImageHandle: &msbSdk.ImageHandle{},
			Config:      &msbSdk.ImageConfig{Digest: "sha256:abc"},
		}, nil
	}
	detail, err := (&realMsbClient{}).ImageInspectDetail(context.Background(), "ref")
	if err != nil || detail == nil {
		t.Fatalf("ImageInspectDetail() = %v, %v", detail, err)
	}
	if detail.Reference != "" || detail.ManifestDigest != "" || detail.Architecture != "" ||
		detail.OS != "" || detail.SizeBytes != nil || !detail.CreatedAt.IsZero() {
		t.Errorf("ImageInspectDetail() from empty handle = %+v, want zero metadata", detail)
	}
	if detail.Config.Digest != "sha256:abc" {
		t.Errorf("ImageInspectDetail() config = %+v, want digest sha256:abc", detail.Config)
	}

	realImageInspect = func(_ context.Context, _ string) (*msbSdk.ImageDetail, error) {
		return nil, errors.New("inspect boom")
	}
	if _, err := (&realMsbClient{}).ImageInspectDetail(context.Background(), "ref"); err == nil {
		t.Fatal("ImageInspectDetail() with a failing inspect = nil error, want error")
	}
}

func TestRealSandboxHandleConfigAndModify(t *testing.T) {
	var h SandboxHandle = &realSandboxHandle{handle: &msbSdk.SandboxHandle{}}
	cfg, err := h.Config()
	if err == nil || cfg != nil {
		t.Errorf("expected Config error on handle without config, got cfg=%v err=%v", cfg, err)
	}
}

func TestRealSandboxHandleAccessors(t *testing.T) {
	h := &realSandboxHandle{handle: &msbSdk.SandboxHandle{}}
	if got := h.Name(); got != "" {
		t.Errorf("Name() = %q, want empty", got)
	}
	if got := h.ID(); got != "" {
		t.Errorf("ID() = %q, want empty", got)
	}
	if got := h.Status(); got != SandboxStatus("") {
		t.Errorf("Status() = %q, want empty", got)
	}
	if !h.UpdatedAt().Equal(time.Time{}) {
		t.Errorf("UpdatedAt() = %v, want zero time", h.UpdatedAt())
	}
	if !h.CreatedAt().Equal(time.Time{}) {
		t.Errorf("CreatedAt() = %v, want zero time", h.CreatedAt())
	}
	if got := h.BackendKind(); got != BackendKind("") {
		t.Errorf("BackendKind() = %q, want empty", got)
	}
	if got := h.Image(); got != "" {
		t.Errorf("Image() = %q, want empty (config parse fails)", got)
	}
}

// TestRealSandboxHandleStatusAndBackendKindDelegation pins that the identity
// accessors translate the SDK enums onto the launcher types through their
// seams, using values that are not the launcher zero value.
func TestRealSandboxHandleStatusAndBackendKindDelegation(t *testing.T) {
	h := realSandboxHandle{handle: &msbSdk.SandboxHandle{}}

	swapSeam(t, &realSandboxHandleStatus, func(*msbSdk.SandboxHandle) msbSdk.SandboxStatus {
		return msbSdk.SandboxStatusRunning
	})
	swapSeam(t, &realSandboxHandleBackendKind, func(*msbSdk.SandboxHandle) msbSdk.BackendKind {
		return msbSdk.BackendLocal
	})
	if got := h.Status(); got != SandboxStatusRunning {
		t.Errorf("Status() = %q, want %q", got, SandboxStatusRunning)
	}
	if got := h.BackendKind(); got != BackendLocal {
		t.Errorf("BackendKind() = %q, want %q", got, BackendLocal)
	}
}

// TestRealSandboxHandleConfigImageConnect covers the handle's Config/Image and
// Connect adapters through their SDK seams, including the config-error and
// connect-error branches that a bare handle cannot reach.
func TestRealSandboxHandleConfigImageConnect(t *testing.T) {
	ctx := context.Background()
	h := realSandboxHandle{handle: &msbSdk.SandboxHandle{}}

	t.Run("Config and Image read the SDK config", func(t *testing.T) {
		swapSeam(t, &realSandboxHandleConfig, func(*msbSdk.SandboxHandle) (*msbSdk.SandboxConfig, error) {
			return &msbSdk.SandboxConfig{Image: "alpine:3"}, nil
		})
		if got := h.Image(); got != "alpine:3" {
			t.Fatalf("Image() = %q, want alpine:3", got)
		}
		cfg, err := h.Config()
		if err != nil || cfg == nil || cfg.Image != "alpine:3" {
			t.Fatalf("Config() = (%+v, %v), want image alpine:3", cfg, err)
		}
	})

	t.Run("Image reads empty on a config error", func(t *testing.T) {
		swapSeam(t, &realSandboxHandleConfig, func(*msbSdk.SandboxHandle) (*msbSdk.SandboxConfig, error) {
			return nil, errors.New("no config")
		})
		if got := h.Image(); got != "" {
			t.Fatalf("Image() = %q, want empty on a config error", got)
		}
	})

	t.Run("Connect wraps the SDK sandbox", func(t *testing.T) {
		live := &msbSdk.Sandbox{}
		swapSeam(t, &realSandboxHandleConnect, func(*msbSdk.SandboxHandle, context.Context) (*msbSdk.Sandbox, error) {
			return live, nil
		})
		sb, err := h.Connect(ctx)
		if err != nil || sb == nil {
			t.Fatalf("Connect() = (%v, %v), want a sandbox, nil", sb, err)
		}
		wrapped, ok := sb.(*realSandbox)
		if !ok || wrapped.sandbox != live {
			t.Fatalf("Connect() = %#v, want realSandbox wrapping the live handle", sb)
		}
	})

	t.Run("Connect propagates the SDK error", func(t *testing.T) {
		wantErr := errors.New("connect boom")
		swapSeam(t, &realSandboxHandleConnect, func(*msbSdk.SandboxHandle, context.Context) (*msbSdk.Sandbox, error) {
			return nil, wantErr
		})
		if _, err := h.Connect(ctx); !errors.Is(err, wantErr) {
			t.Fatalf("Connect() error = %v, want %v", err, wantErr)
		}
	})
}

// TestRealSandboxHandleConfigMounts covers the handle's ConfigMounts adapter
// through the config-JSON seam, so the delegation to ParseConfigMounts is
// exercised without a live VM.
func TestRealSandboxHandleConfigMounts(t *testing.T) {
	swapSeam(t, &realSandboxHandleConfigJSON, func(*msbSdk.SandboxHandle) string {
		return persistedConfigWithMounts
	})
	h := realSandboxHandle{handle: &msbSdk.SandboxHandle{}}
	mounts, err := h.ConfigMounts()
	if err != nil {
		t.Fatalf("ConfigMounts() error = %v", err)
	}
	if len(mounts) != 5 {
		t.Fatalf("ConfigMounts() returned %d mounts, want 5: %+v", len(mounts), mounts)
	}
	if workspace := mounts["/workspace"]; workspace.Bind != "/host/proj" {
		t.Errorf("ConfigMounts()[/workspace] = %+v, want bind /host/proj", workspace)
	}
}

// TestRealVolumeHandleDelegatesAndTranslatesKind pins that the volume wrapper
// exposes the handle's metadata and translates the kind enum onto the launcher
// type. The SDK handle's fields are unexported, so a zero handle exercises the
// delegation with its zero values.
func TestRealVolumeHandleDelegatesAndTranslatesKind(t *testing.T) {
	w := realVolumeHandle{handle: &msbSdk.VolumeHandle{}}
	if got := w.Name(); got != "" {
		t.Errorf("Name() = %q, want empty", got)
	}
	if got := w.Path(); got != "" {
		t.Errorf("Path() = %q, want empty", got)
	}
	if got := w.Kind(); got != VolumeKind("") {
		t.Errorf("Kind() = %q, want empty", got)
	}
	if !w.CreatedAt().Equal(time.Time{}) {
		t.Errorf("CreatedAt() = %v, want zero time", w.CreatedAt())
	}
	if got := w.QuotaMiB(); got != nil {
		t.Errorf("QuotaMiB() = %v, want nil", got)
	}
	if got := w.UsedBytes(); got != 0 {
		t.Errorf("UsedBytes() = %d, want 0", got)
	}
	if got := w.CapacityBytes(); got != nil {
		t.Errorf("CapacityBytes() = %v, want nil", got)
	}
	if got := w.DiskFormat(); got != nil {
		t.Errorf("DiskFormat() = %v, want nil", got)
	}
	if got := w.DiskFstype(); got != nil {
		t.Errorf("DiskFstype() = %v, want nil", got)
	}
	if got := w.Labels(); got != nil {
		t.Errorf("Labels() = %v, want nil", got)
	}
}

// TestRealVolumeHandleDelegationValues injects non-zero values through every
// volume handle seam and pins that the accessors forward them, translating the
// kind enum onto the launcher type.
func TestRealVolumeHandleDelegationValues(t *testing.T) {
	w := realVolumeHandle{handle: &msbSdk.VolumeHandle{}}
	createdAt := time.Unix(1_700_000_000, 0)
	quota := uint32(512)
	capacity := uint64(1 << 30)
	format := "raw"
	fstype := "ext4"
	labels := map[string]string{"project": "demo"}

	swapSeam(t, &realVolumeName, func(*msbSdk.VolumeHandle) string { return "data" })
	swapSeam(t, &realVolumePath, func(*msbSdk.VolumeHandle) string { return "/vol/data" })
	swapSeam(t, &realVolumeKind, func(*msbSdk.VolumeHandle) msbSdk.VolumeKind { return msbSdk.VolumeKindDisk })
	swapSeam(t, &realVolumeCreatedAt, func(*msbSdk.VolumeHandle) time.Time { return createdAt })
	swapSeam(t, &realVolumeQuotaMiB, func(*msbSdk.VolumeHandle) *uint32 { return &quota })
	swapSeam(t, &realVolumeUsedBytes, func(*msbSdk.VolumeHandle) uint64 { return 4096 })
	swapSeam(t, &realVolumeCapacityBytes, func(*msbSdk.VolumeHandle) *uint64 { return &capacity })
	swapSeam(t, &realVolumeDiskFormat, func(*msbSdk.VolumeHandle) *string { return &format })
	swapSeam(t, &realVolumeDiskFstype, func(*msbSdk.VolumeHandle) *string { return &fstype })
	swapSeam(t, &realVolumeLabels, func(*msbSdk.VolumeHandle) map[string]string { return labels })

	if got := w.Name(); got != "data" {
		t.Errorf("Name() = %q, want data", got)
	}
	if got := w.Path(); got != "/vol/data" {
		t.Errorf("Path() = %q, want /vol/data", got)
	}
	if got := w.Kind(); got != VolumeKindDisk {
		t.Errorf("Kind() = %q, want %q", got, VolumeKindDisk)
	}
	if got := w.CreatedAt(); !got.Equal(createdAt) {
		t.Errorf("CreatedAt() = %v, want %v", got, createdAt)
	}
	if got := w.QuotaMiB(); got == nil || *got != quota {
		t.Errorf("QuotaMiB() = %v, want %d", got, quota)
	}
	if got := w.UsedBytes(); got != 4096 {
		t.Errorf("UsedBytes() = %d, want 4096", got)
	}
	if got := w.CapacityBytes(); got == nil || *got != capacity {
		t.Errorf("CapacityBytes() = %v, want %d", got, capacity)
	}
	if got := w.DiskFormat(); got == nil || *got != format {
		t.Errorf("DiskFormat() = %v, want %q", got, format)
	}
	if got := w.DiskFstype(); got == nil || *got != fstype {
		t.Errorf("DiskFstype() = %v, want %q", got, fstype)
	}
	if got := w.Labels(); len(got) != 1 || got["project"] != "demo" {
		t.Errorf("Labels() = %v, want project=demo", got)
	}
}

// TestRealMsbClientAdapterErrors pins that every adapter propagates the SDK
// error through its seam.
func TestRealMsbClientAdapterErrors(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("sdk failed")

	swapSeam(t, &getSandbox, func(context.Context, string) (*msbSdk.SandboxHandle, error) {
		return nil, wantErr
	})
	swapSeam(t, &createSandbox, func(context.Context, string, ...msbSdk.SandboxOption) (*msbSdk.Sandbox, error) {
		return nil, wantErr
	})
	swapSeam(t, &listSandboxesWith, func(context.Context, ...msbSdk.SandboxListOption) (*msbSdk.SandboxPage, error) {
		return nil, wantErr
	})
	swapSeam(t, &getVolume, func(context.Context, string) (*msbSdk.VolumeHandle, error) {
		return nil, wantErr
	})
	swapSeam(t, &createVolume, func(context.Context, string, ...msbSdk.VolumeOption) (*msbSdk.Volume, error) {
		return nil, wantErr
	})
	swapSeam(t, &listVolumes, func(context.Context) ([]*msbSdk.VolumeHandle, error) {
		return nil, wantErr
	})
	swapSeam(t, &removeVolume, func(context.Context, string) error { return wantErr })
	swapSeam(t, &imageGet, func(context.Context, string) (*msbSdk.ImageHandle, error) {
		return nil, wantErr
	})
	swapSeam(t, &imageList, func(context.Context) ([]*msbSdk.ImageHandle, error) {
		return nil, wantErr
	})
	swapSeam(t, &imageRemove, func(context.Context, string, bool) error { return wantErr })
	swapSeam(t, &imageLoad, func(context.Context, string, ...string) ([]*msbSdk.ImageHandle, error) {
		return nil, wantErr
	})
	swapSeam(t, &realImageInspect, func(context.Context, string) (*msbSdk.ImageDetail, error) {
		return nil, wantErr
	})
	swapSeam(t, &imagePrune, func(context.Context) (*msbSdk.ImagePruneReport, error) {
		return nil, wantErr
	})
	swapSeam(t, &storageUsage, func(context.Context) (*msbSdk.StorageUsageReport, error) {
		return nil, wantErr
	})
	swapSeam(t, &storagePrune, func(context.Context, msbSdk.StoragePruneOptions) (*msbSdk.StoragePruneReport, error) {
		return nil, wantErr
	})

	c := &realMsbClient{}
	cases := []struct {
		name string
		call func() error
	}{
		{"GetSandbox", func() error { _, err := c.GetSandbox(ctx, "x"); return err }},
		{"CreateSandbox", func() error { _, err := c.CreateSandbox(ctx, "x"); return err }},
		{"ListSandboxes", func() error { _, err := c.ListSandboxes(ctx, nil); return err }},
		{"GetVolume", func() error { _, err := c.GetVolume(ctx, "x"); return err }},
		{"CreateVolume", func() error { _, err := c.CreateVolume(ctx, "x"); return err }},
		{"ListVolumes", func() error { _, err := c.ListVolumes(ctx); return err }},
		{"RemoveVolume", func() error { return c.RemoveVolume(ctx, "x") }},
		{"ImageGet", func() error { return c.ImageGet(ctx, "x") }},
		{"ImageList", func() error { _, err := c.ImageList(ctx); return err }},
		{"ImageRemove", func() error { return c.ImageRemove(ctx, "x", false) }},
		{"ImageLoad", func() error { return c.ImageLoad(ctx, "x", strings.NewReader("")) }},
		{"ImageInspect", func() error { _, err := c.ImageInspect(ctx, "x"); return err }},
		{"ImagePrune", func() error { _, err := c.ImagePrune(ctx); return err }},
		{"StorageUsage", func() error { _, err := c.StorageUsage(ctx); return err }},
		{"PruneStorage", func() error { _, err := c.PruneStorage(ctx, PruneRequest{}); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, wantErr) {
				t.Fatalf("%s error = %v, want the seam error", tc.name, err)
			}
		})
	}
}

// TestRealMsbClientLists pins the slice mapping of the three list adapters.
func TestRealMsbClientLists(t *testing.T) {
	ctx := context.Background()
	swapSeam(t, &listSandboxesWith, func(context.Context, ...msbSdk.SandboxListOption) (*msbSdk.SandboxPage, error) {
		return &msbSdk.SandboxPage{Sandboxes: []*msbSdk.SandboxHandle{{}, {}}}, nil
	})
	swapSeam(t, &listVolumes, func(context.Context) ([]*msbSdk.VolumeHandle, error) {
		return []*msbSdk.VolumeHandle{{}, {}}, nil
	})
	swapSeam(t, &imageList, func(context.Context) ([]*msbSdk.ImageHandle, error) {
		return []*msbSdk.ImageHandle{{}, {}}, nil
	})

	// Two of each, so a loop that stops after the first is caught.
	c := &realMsbClient{}
	if handles, err := c.ListSandboxes(ctx, nil); err != nil || len(handles) != 2 || handles[1] == nil {
		t.Errorf("ListSandboxes() = %v, %v; want 2 handles", handles, err)
	}
	if handles, err := c.ListVolumes(ctx); err != nil || len(handles) != 2 || handles[1] == nil {
		t.Errorf("ListVolumes() = %v, %v; want 2 handles", handles, err)
	}
	if handles, err := c.ImageList(ctx); err != nil || len(handles) != 2 || handles[1] == nil {
		t.Errorf("ImageList() = %v, %v; want 2 handles", handles, err)
	}
}

// TestRealMsbClientListSandboxesPagination drives the paging loop and the label
// and cursor option branches. The first page carries the label option; the
// second, reached through NextCursor, adds the cursor option.
func TestRealMsbClientListSandboxesPagination(t *testing.T) {
	ctx := context.Background()
	next := "page-2"
	pages := []*msbSdk.SandboxPage{
		{Sandboxes: []*msbSdk.SandboxHandle{{}}, NextCursor: &next},
		{Sandboxes: []*msbSdk.SandboxHandle{{}}, NextCursor: nil},
	}
	var optionCounts []int
	swapSeam(
		t,
		&listSandboxesWith,
		func(_ context.Context, opts ...msbSdk.SandboxListOption) (*msbSdk.SandboxPage, error) {
			optionCounts = append(optionCounts, len(opts))
			return pages[len(optionCounts)-1], nil
		},
	)

	handles, err := (&realMsbClient{}).ListSandboxes(ctx, map[string]string{"project": "demo"})
	if err != nil || len(handles) != 2 {
		t.Fatalf("ListSandboxes() = %d handles, %v; want 2 over two pages", len(handles), err)
	}
	if len(optionCounts) != 2 || optionCounts[0] != 1 || optionCounts[1] != 2 {
		t.Errorf("ListSandboxesWith option counts = %v, want [1 2]", optionCounts)
	}
}

// TestRealMsbClientListSandboxesOmitsLabelOption pins that an empty label
// filter adds no WithListLabels option, so a nil/empty map does not filter.
func TestRealMsbClientListSandboxesOmitsLabelOption(t *testing.T) {
	var optionCounts []int
	swapSeam(
		t,
		&listSandboxesWith,
		func(_ context.Context, opts ...msbSdk.SandboxListOption) (*msbSdk.SandboxPage, error) {
			optionCounts = append(optionCounts, len(opts))
			return &msbSdk.SandboxPage{}, nil
		},
	)

	if _, err := (&realMsbClient{}).ListSandboxes(context.Background(), map[string]string{}); err != nil {
		t.Fatalf("ListSandboxes() error = %v", err)
	}
	if len(optionCounts) != 1 || optionCounts[0] != 0 {
		t.Errorf("ListSandboxesWith option counts = %v, want [0] for empty labels", optionCounts)
	}
}

// TestRealMsbClientEnsureInstalled covers EnsureInstalled's error path through
// the installer seam. The real SDK installer is deliberately not exercised: it
// downloads a runtime over the network, which makes a unit test slow and flaky,
// and the success path (including the private home it must name) is covered by
// TestRealClientEnsureInstalledPassesPrivateHome.
func TestRealMsbClientEnsureInstalled(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	previous := skipRuntimeInstall
	skipRuntimeInstall = false
	t.Cleanup(func() { skipRuntimeInstall = previous })

	installErr := errors.New("installer unavailable")
	orig := ensureRuntime
	ensureRuntime = func(
		context.Context, msbSdk.RuntimeConfig, msbSdk.InstallOptions,
	) (msbSdk.ResolvedRuntime, error) {
		return msbSdk.ResolvedRuntime{}, installErr
	}
	t.Cleanup(func() { ensureRuntime = orig })

	if err := (&realMsbClient{}).EnsureInstalled(context.Background()); !errors.Is(err, installErr) {
		t.Errorf("EnsureInstalled() error = %v, want %v", err, installErr)
	}
}

func TestRealMsbClientCreateRemoveVolume(t *testing.T) {
	ctx := context.Background()
	var createdName, removedName string
	swapSeam(t, &createVolume, func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (*msbSdk.Volume, error) {
		createdName = name
		return &msbSdk.Volume{}, nil
	})
	swapSeam(t, &getVolume, func(context.Context, string) (*msbSdk.VolumeHandle, error) {
		return &msbSdk.VolumeHandle{}, nil
	})
	swapSeam(t, &removeVolume, func(_ context.Context, name string) error {
		removedName = name
		return nil
	})

	c := &realMsbClient{}
	if h, err := c.CreateVolume(ctx, "vol"); err != nil || h == nil {
		t.Fatalf("CreateVolume() = %v, %v; want a handle", h, err)
	}
	if err := c.RemoveVolume(ctx, "vol"); err != nil {
		t.Errorf("RemoveVolume() error = %v, want nil", err)
	}
	if createdName != "vol" || removedName != "vol" {
		t.Errorf("create=%q remove=%q, want both %q", createdName, removedName, "vol")
	}
}

// TestCreateVolumeResolvesTheCreatedVolumeThroughGetVolume pins that CreateVolume
// returns the resolved lookup handle, not a wrapper or the raw create result.
func TestCreateVolumeResolvesTheCreatedVolumeThroughGetVolume(t *testing.T) {
	ctx := context.Background()
	swapSeam(t, &createVolume, func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (*msbSdk.Volume, error) {
		return &msbSdk.Volume{}, nil
	})
	var resolvedName string
	resolved := &msbSdk.VolumeHandle{}
	swapSeam(t, &getVolume, func(_ context.Context, name string) (*msbSdk.VolumeHandle, error) {
		resolvedName = name
		return resolved, nil
	})

	handle, err := (&realMsbClient{}).CreateVolume(ctx, "vol")
	if err != nil || handle == nil {
		t.Fatalf("CreateVolume() = %v, %v; want a handle", handle, err)
	}
	if resolvedName != "vol" {
		t.Fatal("CreateVolume did not resolve the created volume through getVolume")
	}
	if got, ok := handle.(realVolumeHandle); !ok || got.handle != resolved {
		t.Errorf("CreateVolume() handle = %T, want a wrapper around the resolved *msbSdk.VolumeHandle", handle)
	}
}

// TestCreateVolumeRemovesTheOrphanWhenGetVolumeFails pins that a volume created
// but not resolved is not orphaned: the lookup error still surfaces, the volume
// is removed by name, and the removal runs detached from the caller's
// cancellation so a cancelled ctx cannot skip it.
func TestCreateVolumeRemovesTheOrphanWhenGetVolumeFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	swapSeam(t, &createVolume, func(_ context.Context, _ string, _ ...msbSdk.VolumeOption) (*msbSdk.Volume, error) {
		return &msbSdk.Volume{}, nil
	})
	wantErr := errors.New("resolve boom")
	swapSeam(t, &getVolume, func(context.Context, string) (*msbSdk.VolumeHandle, error) {
		cancel() // the run is abandoned before the orphan can be cleaned up
		return nil, wantErr
	})
	var removedName string
	var removedCtxErr error
	swapSeam(t, &removeVolume, func(cleanupCtx context.Context, name string) error {
		removedName = name
		removedCtxErr = cleanupCtx.Err()
		return nil
	})

	if _, err := (&realMsbClient{}).CreateVolume(ctx, "vol"); !errors.Is(err, wantErr) {
		t.Fatalf("CreateVolume() error = %v, want %v", err, wantErr)
	}
	if removedName != "vol" {
		t.Errorf("removed volume = %q, want %q", removedName, "vol")
	}
	if removedCtxErr != nil {
		t.Errorf("cleanup context err = %v, want a context detached from the caller", removedCtxErr)
	}
}

// TestCreateVolumeSkipsLookupWhenCreateFails pins that a failed create returns
// immediately: the lookup is never reached, so a create error cannot be masked
// by a lookup that happens to succeed.
func TestCreateVolumeSkipsLookupWhenCreateFails(t *testing.T) {
	createErr := errors.New("create boom")
	swapSeam(t, &createVolume, func(context.Context, string, ...msbSdk.VolumeOption) (*msbSdk.Volume, error) {
		return nil, createErr
	})
	lookedUp := false
	swapSeam(t, &getVolume, func(context.Context, string) (*msbSdk.VolumeHandle, error) {
		lookedUp = true
		return &msbSdk.VolumeHandle{}, nil
	})

	if _, err := (&realMsbClient{}).CreateVolume(context.Background(), "vol"); !errors.Is(err, createErr) {
		t.Fatalf("CreateVolume() error = %v, want %v", err, createErr)
	}
	if lookedUp {
		t.Error("CreateVolume looked up the volume after the create failed")
	}
}

// TestCreateVolumeReportsOrphanRemovalFailure pins that when the lookup and the
// best-effort removal both fail, the error carries both, so the orphan is not
// silently lost.
func TestCreateVolumeReportsOrphanRemovalFailure(t *testing.T) {
	swapSeam(t, &createVolume, func(_ context.Context, _ string, _ ...msbSdk.VolumeOption) (*msbSdk.Volume, error) {
		return &msbSdk.Volume{}, nil
	})
	lookupErr := errors.New("resolve boom")
	removeErr := errors.New("remove boom")
	swapSeam(t, &getVolume, func(context.Context, string) (*msbSdk.VolumeHandle, error) {
		return nil, lookupErr
	})
	swapSeam(t, &removeVolume, func(context.Context, string) error { return removeErr })

	_, err := (&realMsbClient{}).CreateVolume(context.Background(), "vol")
	if !errors.Is(err, lookupErr) || !errors.Is(err, removeErr) {
		t.Errorf("CreateVolume() error = %v, want both the lookup and removal errors", err)
	}
}

// TestRealMsbClientSandboxAdapters covers the GetSandbox and CreateSandbox
// success paths, wrapping the seam's SDK handle in the adapter.
func TestRealMsbClientSandboxAdapters(t *testing.T) {
	ctx := context.Background()
	swapSeam(t, &getSandbox, func(context.Context, string) (*msbSdk.SandboxHandle, error) {
		return &msbSdk.SandboxHandle{}, nil
	})
	swapSeam(t, &createSandbox, func(context.Context, string, ...msbSdk.SandboxOption) (*msbSdk.Sandbox, error) {
		return &msbSdk.Sandbox{}, nil
	})

	c := &realMsbClient{}
	if handle, err := c.GetSandbox(ctx, "s"); err != nil || handle == nil {
		t.Errorf("GetSandbox() = %v, %v; want a handle, nil", handle, err)
	}
	if sandbox, err := c.CreateSandbox(ctx, "s"); err != nil || sandbox == nil {
		t.Errorf("CreateSandbox() = %v, %v; want a sandbox, nil", sandbox, err)
	}
}

// TestRealMsbClientGetVolume covers the GetVolume success path.
func TestRealMsbClientGetVolume(t *testing.T) {
	swapSeam(t, &getVolume, func(context.Context, string) (*msbSdk.VolumeHandle, error) {
		return &msbSdk.VolumeHandle{}, nil
	})
	if handle, err := (&realMsbClient{}).GetVolume(context.Background(), "v"); err != nil || handle == nil {
		t.Errorf("GetVolume() = %v, %v; want a handle, nil", handle, err)
	}
}

// TestRealMsbClientImageAdapters covers the single-object image adapter success
// paths (ImageList is covered by TestRealMsbClientLists).
func TestRealMsbClientImageAdapters(t *testing.T) {
	ctx := context.Background()
	swapSeam(t, &imageGet, func(context.Context, string) (*msbSdk.ImageHandle, error) {
		return &msbSdk.ImageHandle{}, nil
	})
	swapSeam(t, &imageRemove, func(context.Context, string, bool) error { return nil })
	swapSeam(t, &imageLoad, func(context.Context, string, ...string) ([]*msbSdk.ImageHandle, error) {
		return []*msbSdk.ImageHandle{{}}, nil
	})
	swapSeam(t, &imagePrune, func(context.Context) (*msbSdk.ImagePruneReport, error) {
		return &msbSdk.ImagePruneReport{ManifestsRemoved: 2}, nil
	})
	swapSeam(t, &storageUsage, func(context.Context) (*msbSdk.StorageUsageReport, error) {
		return &msbSdk.StorageUsageReport{}, nil
	})

	c := &realMsbClient{}
	if err := c.ImageGet(ctx, "ref"); err != nil {
		t.Errorf("ImageGet() = %v, want nil", err)
	}
	if err := c.ImageRemove(ctx, "ref", true); err != nil {
		t.Errorf("ImageRemove() = %v, want nil", err)
	}
	if err := c.ImageLoad(ctx, "ref", strings.NewReader("")); err != nil {
		t.Errorf("ImageLoad() = %v, want nil", err)
	}
	if report, err := c.ImagePrune(ctx); err != nil || report.ManifestsRemoved != 2 {
		t.Errorf("ImagePrune() = %v, %v; want 2 manifests removed", report, err)
	}
	if report, err := c.StorageUsage(ctx); err != nil || report == nil {
		t.Errorf("StorageUsage() = %v, %v; want a report, nil", report, err)
	}
}

// TestRealMsbClientImageLoadErrors covers the spooling failure before the SDK
// seam is reached; the SDK load failure is covered by
// TestRealMsbClientAdapterErrors. The reader's error must be wrapped with %w so
// callers can unwrap it.
func TestRealMsbClientImageLoadErrors(t *testing.T) {
	wantErr := errors.New("reader boom")
	err := (&realMsbClient{}).ImageLoad(context.Background(), "ref", errReader{err: wantErr})
	if !errors.Is(err, wantErr) {
		t.Errorf("ImageLoad() error = %v, want it to wrap %v", err, wantErr)
	}
}

// fakeSpoolFile is an imageSpoolFile whose Write/Sync/Close can each fail, so
// the ImageLoad spool branches that a real temp file cannot reach are covered.
type fakeSpoolFile struct {
	name     string
	writeErr error
	syncErr  error
	closeErr error
}

func (f *fakeSpoolFile) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return len(p), nil
}

func (f *fakeSpoolFile) Name() string { return f.name }
func (f *fakeSpoolFile) Sync() error  { return f.syncErr }
func (f *fakeSpoolFile) Close() error { return f.closeErr }

// TestRealMsbClientImageLoadSpoolFailures covers ImageLoad's temp-file
// creation, sync, and close failure branches. The close branch is asserted both
// ways: a close error after a clean spool is surfaced, and a close error after a
// prior failure does not mask the earlier error.
func TestRealMsbClientImageLoadSpoolFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("creating the temp file fails", func(t *testing.T) {
		wantErr := errors.New("temp boom")
		swapSeam(t, &createImageSpoolFile, func() (imageSpoolFile, error) { return nil, wantErr })
		if err := (&realMsbClient{}).ImageLoad(ctx, "ref", strings.NewReader("x")); !errors.Is(err, wantErr) {
			t.Fatalf("ImageLoad() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("syncing fails", func(t *testing.T) {
		wantErr := errors.New("sync boom")
		swapSeam(t, &createImageSpoolFile, func() (imageSpoolFile, error) {
			return &fakeSpoolFile{name: "spool.tar", syncErr: wantErr}, nil
		})
		if err := (&realMsbClient{}).ImageLoad(ctx, "ref", strings.NewReader("x")); !errors.Is(err, wantErr) {
			t.Fatalf("ImageLoad() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("close failure after a clean spool is surfaced", func(t *testing.T) {
		closeErr := errors.New("close boom")
		swapSeam(t, &createImageSpoolFile, func() (imageSpoolFile, error) {
			return &fakeSpoolFile{name: "spool.tar", closeErr: closeErr}, nil
		})
		swapSeam(t, &imageLoad, func(context.Context, string, ...string) ([]*msbSdk.ImageHandle, error) {
			return nil, nil
		})
		if err := (&realMsbClient{}).ImageLoad(ctx, "ref", strings.NewReader("x")); !errors.Is(err, closeErr) {
			t.Fatalf("ImageLoad() error = %v, want %v", err, closeErr)
		}
	})

	t.Run("close failure does not mask a prior error", func(t *testing.T) {
		syncErr := errors.New("sync boom")
		swapSeam(t, &createImageSpoolFile, func() (imageSpoolFile, error) {
			return &fakeSpoolFile{name: "spool.tar", syncErr: syncErr, closeErr: errors.New("close boom")}, nil
		})
		err := (&realMsbClient{}).ImageLoad(ctx, "ref", strings.NewReader("x"))
		if !errors.Is(err, syncErr) {
			t.Fatalf("ImageLoad() error = %v, want the earlier %v not masked by close", err, syncErr)
		}
	})
}

func TestRealSandboxDelegatesToSDK(t *testing.T) {
	ctx := context.Background()

	t.Run("Attach", func(t *testing.T) {
		original := realSandboxAttach
		t.Cleanup(func() { realSandboxAttach = original })
		called := false
		realSandboxAttach = func(_ *msbSdk.Sandbox, _ context.Context, _ string, _ ...string) (int, error) {
			called = true
			return 3, nil
		}
		code, err := (realSandbox{}).Attach(ctx, "cmd")
		if err != nil || code != 3 || !called {
			t.Fatalf("Attach() = (%d, %v), called=%v; want (3, nil, true)", code, err, called)
		}
	})

	t.Run("AttachWith", func(t *testing.T) {
		original := realSandboxAttachWith
		t.Cleanup(func() { realSandboxAttachWith = original })
		called := false
		realSandboxAttachWith = func(
			_ *msbSdk.Sandbox, _ context.Context, _ string, _ []string, _ ...msbSdk.AttachOption,
		) (int, error) {
			called = true
			return 4, nil
		}
		code, err := (realSandbox{}).AttachWith(ctx, "cmd", []string{"a"}, AttachRequest{})
		if err != nil || code != 4 || !called {
			t.Fatalf("AttachWith() = (%d, %v), called=%v; want (4, nil, true)", code, err, called)
		}
	})

	t.Run("Detach", func(t *testing.T) {
		original := realSandboxDetach
		t.Cleanup(func() { realSandboxDetach = original })
		called := false
		realSandboxDetach = func(_ *msbSdk.Sandbox, _ context.Context) error {
			called = true
			return nil
		}
		if err := (realSandbox{}).Detach(ctx); err != nil || !called {
			t.Fatalf("Detach() = %v, called=%v; want nil, true", err, called)
		}
	})

	t.Run("Destroy", func(t *testing.T) {
		original := realSandboxDestroy
		t.Cleanup(func() { realSandboxDestroy = original })
		wantErr := errors.New("destroy failed")
		realSandboxDestroy = func(_ *msbSdk.Sandbox, _ context.Context, _ ...msbSdk.DestroyOption) error {
			return wantErr
		}
		if err := (realSandbox{}).Destroy(ctx, LifecycleOptions{}); !errors.Is(err, wantErr) {
			t.Fatalf("Destroy() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("Close", func(t *testing.T) {
		original := realSandboxClose
		t.Cleanup(func() { realSandboxClose = original })
		called := false
		realSandboxClose = func(_ *msbSdk.Sandbox) error {
			called = true
			return nil
		}
		if err := (realSandbox{}).Close(); err != nil || !called {
			t.Fatalf("Close() = %v, called=%v; want nil, true", err, called)
		}
	})
}

// TestRealSandboxShellExecStreamDelegation covers the sandbox's Shell, Exec, and
// ShellStream adapters through their SDK seams, including the ShellStream
// success wrap and error branch.
func TestRealSandboxShellExecStreamDelegation(t *testing.T) {
	ctx := context.Background()

	t.Run("Shell", func(t *testing.T) {
		out := &msbSdk.ExecOutput{}
		swapSeam(t, &realSandboxShell, func(
			_ *msbSdk.Sandbox, _ context.Context, command string, _ ...msbSdk.ExecOption,
		) (*msbSdk.ExecOutput, error) {
			if command != "echo hi" {
				t.Errorf("Shell() command = %q, want echo hi", command)
			}
			return out, nil
		})
		got, err := (realSandbox{}).Shell(ctx, "echo hi", ExecRequest{})
		if err != nil || got != ShellResult(out) {
			t.Fatalf("Shell() = (%v, %v), want the SDK output", got, err)
		}
	})

	t.Run("Exec", func(t *testing.T) {
		out := &msbSdk.ExecOutput{}
		swapSeam(t, &realSandboxExec, func(
			_ *msbSdk.Sandbox, _ context.Context, command string, args []string, _ ...msbSdk.ExecOption,
		) (*msbSdk.ExecOutput, error) {
			if command != "sh" || len(args) != 1 || args[0] != "-c" {
				t.Errorf("Exec() = (%q, %v), want sh [-c]", command, args)
			}
			return out, nil
		})
		got, err := (realSandbox{}).Exec(ctx, "sh", []string{"-c"}, ExecRequest{})
		if err != nil || got != ShellResult(out) {
			t.Fatalf("Exec() = (%v, %v), want the SDK output", got, err)
		}
	})

	t.Run("ShellStream wraps the SDK handle", func(t *testing.T) {
		handle := &msbSdk.ExecHandle{}
		swapSeam(t, &realSandboxShellStream, func(
			_ *msbSdk.Sandbox, _ context.Context, _ string, _ ...msbSdk.ExecOption,
		) (*msbSdk.ExecHandle, error) {
			return handle, nil
		})
		got, err := (realSandbox{}).ShellStream(ctx, "cmd", ExecRequest{})
		if err != nil {
			t.Fatalf("ShellStream() error = %v", err)
		}
		wrapped, ok := got.(*realStreamHandle)
		if !ok || wrapped.handle != handle {
			t.Fatalf("ShellStream() = %#v, want realStreamHandle wrapping the SDK handle", got)
		}
	})

	t.Run("ShellStream propagates the SDK error", func(t *testing.T) {
		wantErr := errors.New("stream boom")
		swapSeam(t, &realSandboxShellStream, func(
			_ *msbSdk.Sandbox, _ context.Context, _ string, _ ...msbSdk.ExecOption,
		) (*msbSdk.ExecHandle, error) {
			return nil, wantErr
		})
		if _, err := (realSandbox{}).ShellStream(ctx, "cmd", ExecRequest{}); !errors.Is(err, wantErr) {
			t.Fatalf("ShellStream() error = %v, want %v", err, wantErr)
		}
	})
}

// swapSeam replaces a package-level SDK seam for the duration of the test and
// restores the real call on cleanup, so unit tests exercise the adapters
// against fakes without loading the native library or reading real runtime
// state. The real adapter-over-SDK behavior lives in the msb-tagged tests.
func swapSeam[T any](t *testing.T, seam *T, replacement T) {
	t.Helper()
	original := *seam
	*seam = replacement
	t.Cleanup(func() { *seam = original })
}

func TestRealSandboxMetricsAndLogsDelegation(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("metrics failed")

	t.Run("Metrics", func(t *testing.T) {
		original := realSandboxMetrics
		t.Cleanup(func() { realSandboxMetrics = original })
		want := &msbSdk.Metrics{MemoryBytes: 42}
		called := false
		realSandboxMetrics = func(_ *msbSdk.Sandbox, _ context.Context) (*msbSdk.Metrics, error) {
			called = true
			return want, nil
		}
		got, err := (realSandbox{}).Metrics(ctx)
		if err != nil || got == nil || got.MemoryBytes != want.MemoryBytes || !called {
			t.Fatalf("Metrics() = (%v, %v), called=%v; want (want, nil, true)", got, err, called)
		}
	})

	t.Run("Metrics error", func(t *testing.T) {
		swapSeam(t, &realSandboxMetrics, func(_ *msbSdk.Sandbox, _ context.Context) (*msbSdk.Metrics, error) {
			return nil, wantErr
		})
		if _, err := (realSandbox{}).Metrics(ctx); !errors.Is(err, wantErr) {
			t.Fatalf("Metrics() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("Logs", func(t *testing.T) {
		original := realSandboxLogs
		t.Cleanup(func() { realSandboxLogs = original })
		want := []msbSdk.LogEntry{{Source: msbSdk.LogSourceSystem, Data: []byte("boom")}}
		var gotOpts msbSdk.LogOptions
		realSandboxLogs = func(
			_ *msbSdk.Sandbox, _ context.Context, opts msbSdk.LogOptions,
		) ([]msbSdk.LogEntry, error) {
			gotOpts = opts
			return want, nil
		}
		got, err := (realSandbox{}).Logs(ctx, LogQuery{Tail: 7})
		if err != nil || len(got) != 1 || got[0].Source != LogSourceSystem || gotOpts.Tail != 7 {
			t.Fatalf("Logs() = (%v, %v), opts=%+v; want one system entry, tail 7", got, err, gotOpts)
		}
	})

	t.Run("Logs error", func(t *testing.T) {
		swapSeam(t, &realSandboxLogs, func(
			_ *msbSdk.Sandbox, _ context.Context, _ msbSdk.LogOptions,
		) ([]msbSdk.LogEntry, error) {
			return nil, wantErr
		})
		if _, err := (realSandbox{}).Logs(ctx, LogQuery{}); !errors.Is(err, wantErr) {
			t.Fatalf("Logs() error = %v, want %v", err, wantErr)
		}
	})
}

func TestRealSandboxHandleMetricsAndLogsDelegation(t *testing.T) {
	ctx := context.Background()
	h := realSandboxHandle{handle: &msbSdk.SandboxHandle{}}
	wantErr := errors.New("handle metrics failed")

	t.Run("Metrics", func(t *testing.T) {
		original := realSandboxHandleMetrics
		t.Cleanup(func() { realSandboxHandleMetrics = original })
		want := &msbSdk.Metrics{MemoryLimitBytes: 99}
		realSandboxHandleMetrics = func(_ *msbSdk.SandboxHandle, _ context.Context) (*msbSdk.Metrics, error) {
			return want, nil
		}
		got, err := h.Metrics(ctx)
		if err != nil || got == nil || got.MemoryLimitBytes != want.MemoryLimitBytes {
			t.Fatalf("Metrics() = (%v, %v); want (want, nil)", got, err)
		}
	})

	t.Run("Metrics error", func(t *testing.T) {
		swapSeam(
			t,
			&realSandboxHandleMetrics,
			func(_ *msbSdk.SandboxHandle, _ context.Context) (*msbSdk.Metrics, error) {
				return nil, wantErr
			},
		)
		if _, err := h.Metrics(ctx); !errors.Is(err, wantErr) {
			t.Fatalf("Metrics() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("Logs", func(t *testing.T) {
		original := realSandboxHandleLogs
		t.Cleanup(func() { realSandboxHandleLogs = original })
		want := []msbSdk.LogEntry{{Source: msbSdk.LogSourceSystem}}
		realSandboxHandleLogs = func(
			_ *msbSdk.SandboxHandle, _ context.Context, _ msbSdk.LogOptions,
		) ([]msbSdk.LogEntry, error) {
			return want, nil
		}
		got, err := h.Logs(ctx, LogQuery{})
		if err != nil || len(got) != 1 {
			t.Fatalf("Logs() = (%v, %v); want one entry, nil", got, err)
		}
	})

	t.Run("Logs error", func(t *testing.T) {
		swapSeam(t, &realSandboxHandleLogs, func(
			_ *msbSdk.SandboxHandle, _ context.Context, _ msbSdk.LogOptions,
		) ([]msbSdk.LogEntry, error) {
			return nil, wantErr
		})
		if _, err := h.Logs(ctx, LogQuery{}); !errors.Is(err, wantErr) {
			t.Fatalf("Logs() error = %v, want %v", err, wantErr)
		}
	})
}

func TestMockSandboxMetricsAndLogs(t *testing.T) {
	ctx := context.Background()

	sb := &MockSandbox{MetricsResult: &Metrics{MemoryBytes: 1}}
	got, err := sb.Metrics(ctx)
	if err != nil || got.MemoryBytes != 1 {
		t.Fatalf("Metrics() = (%v, %v); want MemoryBytes 1", got, err)
	}

	boom := errors.New("boom")
	if _, err := (&MockSandbox{MetricsErr: boom}).Metrics(ctx); !errors.Is(err, boom) {
		t.Fatalf("Metrics() error = %v; want boom", err)
	}

	entries := []LogEntry{{Source: LogSourceSystem}}
	logSB := &MockSandbox{LogsResult: entries}
	gotEntries, err := logSB.Logs(ctx, LogQuery{Tail: 3})
	if err != nil || len(gotEntries) != 1 {
		t.Fatalf("Logs() = (%v, %v); want one entry, nil", gotEntries, err)
	}
	if len(logSB.LogsOptions) != 1 || logSB.LogsOptions[0].Tail != 3 {
		t.Fatalf("LogsOptions = %+v; want one call with Tail 3", logSB.LogsOptions)
	}
	if _, err := (&MockSandbox{LogsErr: boom}).Logs(ctx, LogQuery{}); !errors.Is(err, boom) {
		t.Fatalf("Logs() error = %v; want boom", err)
	}
}

func TestMockSandboxHandleMetricsAndLogs(t *testing.T) {
	ctx := context.Background()

	h := &MockSandboxHandle{MetricsResult: &Metrics{MemoryLimitBytes: 2}}
	got, err := h.Metrics(ctx)
	if err != nil || got.MemoryLimitBytes != 2 {
		t.Fatalf("Metrics() = (%v, %v); want MemoryLimitBytes 2", got, err)
	}

	boom := errors.New("boom")
	if _, err := (&MockSandboxHandle{MetricsErr: boom}).Metrics(ctx); !errors.Is(err, boom) {
		t.Fatalf("Metrics() error = %v; want boom", err)
	}

	logHandle := &MockSandboxHandle{LogsResult: []LogEntry{{Source: LogSourceSystem}}}
	gotEntries, err := logHandle.Logs(ctx, LogQuery{Tail: 9})
	if err != nil || len(gotEntries) != 1 {
		t.Fatalf("Logs() = (%v, %v); want one entry, nil", gotEntries, err)
	}
	if len(logHandle.LogsOptions) != 1 || logHandle.LogsOptions[0].Tail != 9 {
		t.Fatalf("LogsOptions = %+v; want one call with Tail 9", logHandle.LogsOptions)
	}
	if _, err := (&MockSandboxHandle{LogsErr: boom}).Logs(ctx, LogQuery{}); !errors.Is(err, boom) {
		t.Fatalf("Logs() error = %v; want boom", err)
	}
}
