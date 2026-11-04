package vm

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

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
func reconcileHandle(maxCPUs uint8, maxMemoryMiB uint32) *msb.MockSandboxHandle {
	return &msb.MockSandboxHandle{
		Cfg: &msbSdk.SandboxConfig{
			CPUs:         2,
			MaxCPUs:      maxCPUs,
			MemoryMiB:    2048,
			MaxMemoryMiB: maxMemoryMiB,
		},
		Plan: &msbSdk.SandboxModificationPlan{Applied: true},
	}
}
