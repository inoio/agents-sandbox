package main

import (
	"context"
	"slices"
	"testing"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/sandbox/doctor"
	"github.com/inoio/agents-sandbox/internal/sandbox/lifecycle"
	sandboxmsb "github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// captureLifecycleTrace makes the next run or shell record into a trace the
// test keeps, and returns it.
func captureLifecycleTrace(t *testing.T) *lifecycle.Trace {
	t.Helper()
	trace := &lifecycle.Trace{}
	original := newLifecycleTrace
	newLifecycleTrace = func() *lifecycle.Trace { return trace }
	t.Cleanup(func() { newLifecycleTrace = original })
	return trace
}

// TestRunShellRecordTheLifecycleTrace pins the ordered stage trace per command
// and mode against lifecycle.Order(): the mutating path reaches every stage
// through attach, --dry-run-vm stops before attach, and a --dry-run preview
// stops after the startup stages. Reordering, adding or removing a stage
// anywhere in cmd, vm or session fails a case here.
func TestRunShellRecordTheLifecycleTrace(t *testing.T) {
	order := lifecycle.Order()
	startup := order[:slices.Index(order, lifecycle.Prune)+1]
	beforeAttach := order[:slices.Index(order, lifecycle.Attach)]
	for _, command := range []string{cmdRun, cmdShell} {
		for _, tc := range []struct {
			mode string
			flag []string
			want []lifecycle.StageID
		}{
			{mode: "run", want: order},
			{mode: "dry-run-vm", flag: []string{"--dry-run-vm"}, want: beforeAttach},
			{mode: "dry-run", flag: []string{"--dry-run"}, want: startup},
		} {
			t.Run(command+"/"+tc.mode, func(t *testing.T) {
				initTestProjectDir(t)
				trace := captureLifecycleTrace(t)
				args := append([]string{command}, tc.flag...)
				root, _ := setupRunMocks(t, &sandboxmsb.MockMsbClient{}, &sandboxmsb.MockSandbox{}, args...)
				if err := root.Execute(); err != nil {
					t.Fatalf("%v: %v", args, err)
				}
				if got := trace.Stages(); !slices.Equal(got, tc.want) {
					t.Errorf("trace = %v, want %v", got, tc.want)
				}
			})
		}
	}
}

// TestNewLifecycleTraceIsNotNil pins the trace seam's default: the factory must
// hand out a usable trace, not nil, so an invocation with no test override still
// has somewhere to record its stages.
func TestNewLifecycleTraceIsNotNil(t *testing.T) {
	if trace := newLifecycleTrace(); trace == nil {
		t.Fatal("newLifecycleTrace() = nil, want a non-nil trace")
	}
}

// TestRunStartupStagesPreflightFailure pins the (exit, error) pair the run and
// shell callers branch on, and the stage recorded before the failure: a failing
// preflight records preflight-only and returns exit=false with the error, so the
// caller aborts on the error rather than the flag.
func TestRunStartupStagesPreflightFailure(t *testing.T) {
	doctor.MockedCheckAll(t, false)
	trace := &lifecycle.Trace{}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	exit, err := runStartupStages(
		context.Background(), cmd, options.ModeRun, options.RunOptions{Trace: trace}, &termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected the preflight error to propagate")
	}
	if exit {
		t.Error("exit = true, want false on a preflight failure")
	}
	if got, want := trace.Stages(), []lifecycle.StageID{lifecycle.Preflight}; !slices.Equal(got, want) {
		t.Errorf("trace = %v, want %v", got, want)
	}
}
