package prepare

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/pressure"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sysinfo"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// TestHostAvailableMemorySeamDefaultIsSysinfo pins the copied seam contract: the
// captured default is the production sysinfo read. This is the sole default-suite
// place the real host figure is read.
func TestHostAvailableMemorySeamDefaultIsSysinfo(t *testing.T) {
	//testenv:allow asserts the captured seam default is the production host read
	gotMiB, gotKnown := capturedHostAvailableMemoryMiB()
	//testenv:allow asserts the captured seam default is the production host read
	wantMiB, wantKnown := sysinfo.AvailableMemoryMiB()
	if gotMiB != wantMiB || gotKnown != wantKnown {
		t.Errorf("captured seam = (%d, %v), want the sysinfo read (%d, %v)", gotMiB, gotKnown, wantMiB, wantKnown)
	}
}

// TestHostAvailableMemorySeamReachesTheRaiseWarning pins that the memory-raise
// offer derives its host-available figure from the seam: with the host unable to
// hand out the raise, the warning is emitted.
func TestHostAvailableMemorySeamReachesTheRaiseWarning(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	withoutMemoryEnv(t)
	restore := hostAvailableMemoryMiB
	hostAvailableMemoryMiB = func() (uint32, bool) { return 0, true }
	t.Cleanup(func() { hostAvailableMemoryMiB = restore })

	k := testVMKey()
	oldKill := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := seedHomeState(k, state.HomeState{ResourcePressure: &state.ResourcePressureState{
		Kind: "memory", Evidence: string(pressure.EvidenceOOMLog), Victim: "old",
		OOMSeenAt: oldKill, OfferedAt: oldKill.Add(time.Minute),
	}}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
	newKill := oldKill.Add(time.Hour)
	cfg := &msb.SandboxConfig{MemoryMiB: 4096, MaxMemoryMiB: 8192}
	handle := &msbtest.MockSandboxHandle{Cfg: cfg, LogsResult: []msb.LogEntry{memoryRaiseLogEntry(newKill)}}
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "raise", nil },
		InputFn:             func(string, string) (string, error) { return "8G", nil },
	}

	detectAndOfferMemoryRaise(
		context.Background(), liveProjectVM{handle: handle, config: cfg}, k,
		options.RunOptions{}, pressureHomeState(t, k), configpaths.Get(), ui,
	)

	if !anyWarningContains(ui.WarnCalls, "host has only") {
		t.Errorf("warn calls = %v, want the host-lacks-the-raise warning", ui.WarnCalls)
	}
}

// anyWarningContains reports whether any of the warnings contains substring.
func anyWarningContains(warnings []string, substring string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, substring) {
			return true
		}
	}
	return false
}
