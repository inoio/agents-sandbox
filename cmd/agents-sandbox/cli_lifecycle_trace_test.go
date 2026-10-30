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

// TestRunShellPreviewRecordsStartupStages pins the cmd-owned stages and the
// trace seam: a --dry-run preview of run and shell reaches preflight,
// upgrade-check and prune in order (the first two do no work on a preview, the
// prune only reports) and then renders the preview without entering the
// vm-owned stages.
func TestRunShellPreviewRecordsStartupStages(t *testing.T) {
	for _, command := range []string{cmdRun, cmdShell} {
		t.Run(command, func(t *testing.T) {
			initTestProjectDir(t)
			trace := captureLifecycleTrace(t)
			root, _ := setupRunMocks(t, &sandboxmsb.MockMsbClient{}, &sandboxmsb.MockSandbox{}, command, "--dry-run")
			if err := root.Execute(); err != nil {
				t.Fatalf("%s --dry-run: %v", command, err)
			}
			want := []lifecycle.StageID{lifecycle.Preflight, lifecycle.UpgradeCheck, lifecycle.Prune}
			if got := trace.Stages(); !slices.Equal(got, want) {
				t.Errorf("trace = %v, want %v", got, want)
			}
		})
	}
}
