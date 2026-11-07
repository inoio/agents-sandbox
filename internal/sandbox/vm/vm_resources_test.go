package vm

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestReconcileResourceConfig pins how a reused VM's resources follow the run's
// request: a changed CPU count or memory size is applied live (no restart), a
// request above the boot maximum is clamped to it with a warning (exactly at the
// maximum is no clamp), 0 CPUs and no memory mean "leave as is", and an
// unchanged request modifies nothing. An unreadable config is a no-op; a failed
// or conflicting modification is reported.
func TestReconcileResourceConfig(t *testing.T) {
	modifyFailed := errors.New("modify boom")
	cases := []struct {
		name       string
		handle     *msb.MockSandboxHandle // boot config: 2 CPUs, 2048 MiB
		opts       options.RunOptions
		wantModify *msbSdk.ModifyOptions
		wantWarn   []string
		wantErr    func(error) bool
	}{
		{
			name:       "cpus and memory change",
			handle:     reconcileHandle(8, 8192),
			opts:       options.RunOptions{CPUs: 4, Memory: "4G"},
			wantModify: &msbSdk.ModifyOptions{CPUs: 4, MemoryMiB: 4096},
		},
		{
			name:       "one cpu is a change",
			handle:     reconcileHandle(0, 0),
			opts:       options.RunOptions{CPUs: 1},
			wantModify: &msbSdk.ModifyOptions{CPUs: 1},
		},
		{
			name:       "memory with an unknown boot maximum",
			handle:     reconcileHandle(0, 0),
			opts:       options.RunOptions{Memory: "4G"},
			wantModify: &msbSdk.ModifyOptions{MemoryMiB: 4096},
		},
		{
			name:       "memory-only change",
			handle:     reconcileHandle(8, 8192),
			opts:       options.RunOptions{Memory: "4G"},
			wantModify: &msbSdk.ModifyOptions{MemoryMiB: 4096},
		},
		{
			name:       "both clamped to the boot maximum",
			handle:     reconcileHandle(4, 4096),
			opts:       options.RunOptions{CPUs: 16, Memory: "16G"},
			wantModify: &msbSdk.ModifyOptions{CPUs: 4, MemoryMiB: 4096},
			wantWarn:   []string{"cpus limited to 4 (boot maximum); requested 16", "memory limited to 4096 MiB"},
		},
		{
			name:       "a boot maximum of one cpu clamps",
			handle:     reconcileHandle(1, 0),
			opts:       options.RunOptions{CPUs: 4},
			wantModify: &msbSdk.ModifyOptions{CPUs: 1},
			wantWarn:   []string{"cpus limited to 1"},
		},
		{
			name:       "a boot maximum of one MiB clamps",
			handle:     reconcileHandle(0, 1),
			opts:       options.RunOptions{Memory: "4G"},
			wantModify: &msbSdk.ModifyOptions{MemoryMiB: 1},
			wantWarn:   []string{"memory limited to 1 MiB"},
		},
		{
			name:       "a request at the boot maximum does not warn",
			handle:     reconcileHandle(8, 4096),
			opts:       options.RunOptions{CPUs: 8, Memory: "4G"},
			wantModify: &msbSdk.ModifyOptions{CPUs: 8, MemoryMiB: 4096},
		},
		{
			name: "clamped cpus with unchanged memory",
			handle: &msb.MockSandboxHandle{
				Cfg:  &msbSdk.SandboxConfig{CPUs: 2, MaxCPUs: 8, MemoryMiB: 4096, MaxMemoryMiB: 8192},
				Plan: &msbSdk.SandboxModificationPlan{Applied: true},
			},
			opts:       options.RunOptions{CPUs: 16, Memory: "4G"},
			wantModify: &msbSdk.ModifyOptions{CPUs: 8},
			wantWarn:   []string{"cpus limited to 8"},
		},
		{
			name:       "a larger root disk grows live",
			handle:     reconcileHandleWithRootDisk(),
			opts:       options.RunOptions{DiskSize: "8G"},
			wantModify: &msbSdk.ModifyOptions{RootDiskSizeMiB: 8192},
		},
		{
			name:   "a root disk at the requested size",
			handle: reconcileHandleWithRootDisk(),
			opts:   options.RunOptions{DiskSize: "4G"},
		},
		{
			name:   "a smaller root disk is not staged",
			handle: reconcileHandleWithRootDisk(),
			opts:   options.RunOptions{DiskSize: "2G"},
		},
		{
			name:   "an unrecorded root disk is not staged",
			handle: reconcileHandle(8, 8192),
			opts:   options.RunOptions{DiskSize: "8G"},
		},
		{
			name:   "no disk size keeps the root disk",
			handle: reconcileHandleWithRootDisk(),
		},
		{
			name:   "unchanged request",
			handle: reconcileHandle(8, 8192),
			opts:   options.RunOptions{CPUs: 2, Memory: "2048"},
		},
		{
			name:   "nothing requested",
			handle: reconcileHandle(8, 8192),
		},
		{
			name: "unreadable config",
			handle: func() *msb.MockSandboxHandle {
				h := reconcileHandle(8, 8192)
				h.ConfigErr = errors.New("config boom")
				return h
			}(),
			opts: options.RunOptions{CPUs: 8, Memory: "4G"},
		},
		{
			name: "modify fails",
			handle: func() *msb.MockSandboxHandle {
				h := reconcileHandle(8, 8192)
				h.ModifyErr = modifyFailed
				return h
			}(),
			opts:       options.RunOptions{CPUs: 4},
			wantModify: &msbSdk.ModifyOptions{CPUs: 4},
			wantErr:    func(err error) bool { return errors.Is(err, modifyFailed) },
		},
		{
			name: "modification conflicts",
			handle: func() *msb.MockSandboxHandle {
				h := reconcileHandle(8, 8192)
				h.Plan = &msbSdk.SandboxModificationPlan{
					Conflicts: []msbSdk.ModificationConflict{{Field: "cpus", Message: "conflict"}},
				}
				return h
			}(),
			opts:       options.RunOptions{CPUs: 4},
			wantModify: &msbSdk.ModifyOptions{CPUs: 4},
			wantErr: func(err error) bool {
				return strings.Contains(err.Error(), "resource change not applied: cpus: conflict")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ui := termio.NewTestMock(t)

			err := reconcileResourceConfig(context.Background(), tc.handle, tc.opts, &ui)

			if tc.wantErr != nil {
				if err == nil || !tc.wantErr(err) {
					t.Fatalf("error = %v, want the modification failure", err)
				}
			} else if err != nil {
				t.Fatalf("reconcileResourceConfig: %v", err)
			}
			switch {
			case tc.wantModify == nil && len(tc.handle.ModifiedOptions) != 0:
				t.Errorf("modified %+v, want no modification", tc.handle.ModifiedOptions)
			case tc.wantModify != nil:
				want := *tc.wantModify
				want.Policy = msbSdk.ModificationPolicyNoRestart
				if len(tc.handle.ModifiedOptions) != 1 || !reflect.DeepEqual(tc.handle.ModifiedOptions[0], want) {
					t.Errorf("modified %+v, want exactly %+v", tc.handle.ModifiedOptions, want)
				}
			}
			if len(ui.WarnCalls) != len(tc.wantWarn) {
				t.Fatalf("warnings = %v, want %v", ui.WarnCalls, tc.wantWarn)
			}
			for i, want := range tc.wantWarn {
				if !strings.Contains(ui.WarnCalls[i], want) {
					t.Errorf("warning %d = %q, want %q", i, ui.WarnCalls[i], want)
				}
			}
		})
	}
}

func TestSummarizeConflicts(t *testing.T) {
	cs := []msbSdk.ModificationConflict{
		{Field: "cpus", Message: "too high"},
		{Field: "memory", Message: "not enough"},
	}
	if got := summarizeConflicts(cs); got != "cpus: too high; memory: not enough" {
		t.Errorf("summarizeConflicts() = %q, want joined string", got)
	}
	if got := summarizeConflicts(nil); got != "" {
		t.Errorf("summarizeConflicts(nil) = %q, want empty", got)
	}
}

// reconcileHandle builds a handle whose boot config reports 2 CPUs and 2048 MiB,
// the given CPU and memory boot maxima, and a plan that applies cleanly. The
// boot values stay below every requested count, so a change always registers
// unless a test intends none.
// reconcileHandleWithRootDisk is reconcileHandle's VM with the runtime's
// default 4 GiB managed root disk.
func reconcileHandleWithRootDisk() *msb.MockSandboxHandle {
	handle := reconcileHandle(8, 8192)
	rootDisk := msbSdk.RootDisk.Managed(options.DefaultRootDiskMiB)
	handle.Cfg.RootDisk = &rootDisk
	return handle
}

func reconcileHandle(maxCPUs uint8, maxMemoryMiB uint32) *msb.MockSandboxHandle {
	return &msb.MockSandboxHandle{
		Cfg: &msbSdk.SandboxConfig{
			CPUs:         2,
			MaxCPUs:      maxCPUs,
			MemoryMiB:    2048,
			MaxMemoryMiB: maxMemoryMiB,
		},
		Plan: &msbSdk.SandboxModificationPlan{Applied: true},
		// A staged live memory change is waited on until the limit converges; a
		// large snapshot lets every memory case converge immediately without
		// affecting the staged Modify under test.
		MetricsResult: &msbSdk.Metrics{MemoryLimitBytes: 64 << 30},
	}
}

// setMemoryConvergeSeams shrinks the poll interval and deadline for a test and
// restores the production values afterwards.
func setMemoryConvergeSeams(t *testing.T, timeout time.Duration) {
	t.Helper()
	origInterval, origTimeout := memoryConvergeInterval, memoryConvergeTimeout
	memoryConvergeInterval = time.Millisecond
	memoryConvergeTimeout = timeout
	t.Cleanup(func() {
		memoryConvergeInterval = origInterval
		memoryConvergeTimeout = origTimeout
	})
}

// TestWaitForMemoryLimitMemoryConvergesOnSequentialRead pins the convergence
// contract: an old limit reported first, then the requested limit, succeeds
// once the second read lands — a single read is never enough.
func TestWaitForMemoryLimitMemoryConvergesOnSequentialRead(t *testing.T) {
	setMemoryConvergeSeams(t, time.Second)
	calls := 0
	read := func(context.Context) (*msbSdk.Metrics, error) {
		calls++
		if calls == 1 {
			return &msbSdk.Metrics{MemoryLimitBytes: 1 << 20}, nil
		}
		return &msbSdk.Metrics{MemoryLimitBytes: 4 << 20}, nil
	}

	err := waitForMemoryLimit(context.Background(), read, 4)
	if err != nil {
		t.Fatalf("waitForMemoryLimit() error = %v, want nil", err)
	}
	if calls < 2 {
		t.Errorf("read called %d times, want multiple reads (old limit then new)", calls)
	}
}

// TestWaitForMemoryLimitMemoryConvergeTimeout pins the deadline contract: a
// limit that never reaches the requested value errors instead of hanging.
func TestWaitForMemoryLimitMemoryConvergeTimeout(t *testing.T) {
	setMemoryConvergeSeams(t, 5*time.Millisecond)
	read := func(context.Context) (*msbSdk.Metrics, error) {
		return &msbSdk.Metrics{MemoryLimitBytes: 1 << 20}, nil
	}

	err := waitForMemoryLimit(context.Background(), read, 4)
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "did not converge") {
		t.Errorf("error = %v, want a 'did not converge' message", err)
	}
}

// TestWaitForMemoryLimitMemoryConvergeReadErrorTerminal pins that a metrics
// read error aborts the wait immediately instead of polling on.
func TestWaitForMemoryLimitMemoryConvergeReadErrorTerminal(t *testing.T) {
	setMemoryConvergeSeams(t, time.Second)
	sentinel := errors.New("metrics boom")
	calls := 0
	read := func(context.Context) (*msbSdk.Metrics, error) {
		calls++
		return nil, sentinel
	}

	err := waitForMemoryLimit(context.Background(), read, 4)
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want the metrics read error", err)
	}
	if calls != 1 {
		t.Errorf("read called %d times, want exactly 1 (terminal)", calls)
	}
}

// TestReconcileResourceConfigMemoryConverges pins the end-to-end path: a staged
// live memory change waits on Metrics until the limit converges and then
// succeeds.
func TestReconcileResourceConfigMemoryConverges(t *testing.T) {
	handle := reconcileHandle(8, 8192)
	handle.MetricsResult = &msbSdk.Metrics{MemoryLimitBytes: 4096 << 20}
	ui := termio.NewTestMock(t)

	if err := reconcileResourceConfig(context.Background(), handle, options.RunOptions{Memory: "4G"}, &ui); err != nil {
		t.Fatalf("reconcileResourceConfig() error = %v, want nil", err)
	}
}

// TestReconcileResourceConfigCPUOnlyMemoryConvergeNotWaited pins that a CPU-only
// change triggers no wait: a failing metrics read would surface if one ran.
func TestReconcileResourceConfigCPUOnlyMemoryConvergeNotWaited(t *testing.T) {
	handle := reconcileHandle(8, 8192)
	handle.MetricsErr = errors.New("metrics must not be read")
	ui := termio.NewTestMock(t)

	if err := reconcileResourceConfig(context.Background(), handle, options.RunOptions{CPUs: 4}, &ui); err != nil {
		t.Fatalf("reconcileResourceConfig() error = %v, want nil", err)
	}
	if len(handle.ModifiedOptions) != 1 {
		t.Fatalf("expected 1 Modify, got %d", len(handle.ModifiedOptions))
	}
}

// TestWaitForMemoryLimitMemoryConvergeCanceled pins that a non-deadline context
// cancellation (the caller's own ctx) is returned as-is, not reported as a
// non-convergence.
func TestWaitForMemoryLimitMemoryConvergeCanceled(t *testing.T) {
	setMemoryConvergeSeams(t, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	read := func(context.Context) (*msbSdk.Metrics, error) {
		return &msbSdk.Metrics{MemoryLimitBytes: 1 << 20}, nil
	}

	err := waitForMemoryLimit(ctx, read, 4)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForMemoryLimit() error = %v, want context.Canceled", err)
	}
}

// TestReconcileResourceConfigMemoryConvergeFailure pins that a staged live
// memory change that never converges surfaces the wait error from
// reconcileResourceConfig rather than reporting the raise applied.
func TestReconcileResourceConfigMemoryConvergeFailure(t *testing.T) {
	setMemoryConvergeSeams(t, 5*time.Millisecond)
	handle := reconcileHandle(8, 8192)
	handle.MetricsResult = &msbSdk.Metrics{MemoryLimitBytes: 1 << 20}
	ui := termio.NewTestMock(t)

	err := reconcileResourceConfig(context.Background(), handle, options.RunOptions{Memory: "4G"}, &ui)
	if err == nil {
		t.Fatal("expected a convergence error, got nil")
	}
	if !strings.Contains(err.Error(), "wait for live memory resize") {
		t.Errorf("error = %v, want the wait-for-convergence wrap", err)
	}
	if errors.Unwrap(err) == nil {
		t.Error("the convergence error must preserve its cause (a %w wrap)")
	}
}

// TestMemoryConvergeSeamDefaults pins the convergence poll interval and
// deadline, wall-clock constants the tests otherwise shrink through the seam.
func TestMemoryConvergeSeamDefaults(t *testing.T) {
	if memoryConvergeInterval != 200*time.Millisecond {
		t.Errorf("memoryConvergeInterval = %v, want 200ms", memoryConvergeInterval)
	}
	if memoryConvergeTimeout != 10*time.Second {
		t.Errorf("memoryConvergeTimeout = %v, want 10s", memoryConvergeTimeout)
	}
}

// TestWaitForMemoryLimitUsesMiB pins the MiB-to-bytes conversion: a limit of
// 2 MiB does not satisfy a 4 MiB request.
func TestWaitForMemoryLimitUsesMiB(t *testing.T) {
	setMemoryConvergeSeams(t, 5*time.Millisecond)
	read := func(context.Context) (*msbSdk.Metrics, error) {
		return &msbSdk.Metrics{MemoryLimitBytes: 4 << 19}, nil
	}

	err := waitForMemoryLimit(context.Background(), read, 4)
	if err == nil || !strings.Contains(err.Error(), "did not converge") {
		t.Fatalf("err = %v, want a non-convergence error", err)
	}
}

// TestWaitForMemoryLimitNilMetricsKeepsPolling pins the nil-snapshot guard: a
// nil read result is not converged and must not be dereferenced.
func TestWaitForMemoryLimitNilMetricsKeepsPolling(t *testing.T) {
	setMemoryConvergeSeams(t, 5*time.Millisecond)
	read := func(context.Context) (*msbSdk.Metrics, error) {
		//nolint:nilnil // a nil snapshot with no error is exactly the state this guard handles
		return nil, nil
	}

	err := waitForMemoryLimit(context.Background(), read, 4)
	if err == nil || !strings.Contains(err.Error(), "did not converge") {
		t.Fatalf("err = %v, want a non-convergence error without a nil dereference", err)
	}
}
