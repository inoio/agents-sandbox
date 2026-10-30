package main

import (
	"slices"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/lifecycle"
	sandboxmsb "github.com/inoio/agents-sandbox/internal/sandbox/msb"
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
