package resourceproposal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/pressure"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// The msb side satisfies MemoryReads, so a caller can hand its sandbox or
// sandbox handle to DetectAndOfferMemoryRaise. Machine-checked here rather than
// asserted only in prose; it lives in the test package so the production
// resourceproposal package keeps depending on the abstraction, not on msb.
var (
	_ MemoryReads = msb.SandboxObserver(nil)
	_ MemoryReads = msb.Sandbox(nil)
	_ MemoryReads = msb.SandboxHandle(nil)
)

// detectKey is the project/agent key the shared-core tests detect for.
func detectKey() state.Key {
	return state.Key{Slug: "detectslug", Agent: "opencode"}
}

// oomLogLine builds a system-log entry carrying a parseable OOM kill naming the
// harness, timestamped at.
func oomLogLine(at time.Time) msb.LogEntry {
	return msb.LogEntry{
		Source:    msb.LogSourceSystem,
		Timestamp: at,
		Data:      []byte("Out of memory: Killed process 42 (opencode) total-vm:1kB"),
	}
}

// detectTrippedMetrics is a snapshot at ~95% memory usage, above the 90% trip
// level.
func detectTrippedMetrics() *msb.Metrics {
	return &msb.Metrics{MemoryBytes: 7800 << 20, MemoryLimitBytes: 8192 << 20}
}

// detectRecoveredMetrics is a snapshot at ~61% memory usage, below the 80%
// re-arm level.
func detectRecoveredMetrics() *msb.Metrics {
	return &msb.Metrics{MemoryBytes: 5000 << 20, MemoryLimitBytes: 8192 << 20}
}

// seedDetectState writes hs as the key's HomeState so the marker writers (which
// no-op without a state file) take effect.
func seedDetectState(t *testing.T, k state.Key, hs state.HomeState) {
	t.Helper()
	if err := state.RecoverHomeState(k, func(cur *state.HomeState, _ bool) (bool, error) {
		*cur = hs
		return true, nil
	}); err != nil {
		t.Fatalf("seed home state: %v", err)
	}
}

// readDetectMarker re-reads the key's recorded pressure marker.
func readDetectMarker(t *testing.T, k state.Key) *state.ResourcePressureState {
	t.Helper()
	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	return hs.ResourcePressure
}

// detectRun is the shared-core call under test with explicit args; it returns
// the persisted memory setting.
func detectRun(
	t *testing.T,
	r MemoryReads,
	marker *state.ResourcePressureState,
	currentMiB, bootMaxMiB uint32,
	witnessedHeadroomTrip bool,
	ui termio.UI,
) string {
	t.Helper()
	return DetectAndOfferMemoryRaise(
		context.Background(), detectKey(), r, marker,
		MemoryLimits{CurrentMiB: currentMiB, BootMaxMiB: bootMaxMiB, HostAvailableMiB: 0, HostAvailableKnown: false},
		witnessedHeadroomTrip, options.RunOptions{}, configpaths.Get(), ui,
	)
}

// TestDetectAndOfferMemoryRaiseOffersAndPersists pins the primary path: an OOM
// kill with no marker prompts once and an accepted raise is persisted.
func TestDetectAndOfferMemoryRaiseOffersAndPersists(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	testutil.UnsetEnv(t, "AGENTS_SANDBOX_MEMORY")
	testutil.UnsetEnv(t, "OPENCODE_SANDBOX_MEMORY")
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})
	kill := time.Now()
	r := &msb.MockSandbox{LogsResult: []msb.LogEntry{oomLogLine(kill)}}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "raise", nil },
		InputFn:             func(string, string) (string, error) { return "8G", nil },
	}

	if raised := detectRun(t, r, nil, 4096, 8192, false, ui); raised != "8192M" {
		t.Errorf("raised = %q, want the persisted 8192M", raised)
	}

	if selects != 1 {
		t.Fatalf("Select called %d times, want 1", selects)
	}
	marker := readDetectMarker(t, k)
	if marker == nil || !marker.OOMSeenAt.Equal(kill) || marker.OfferedAt.IsZero() {
		t.Fatalf("marker = %+v, want the kill recorded and answered", marker)
	}
	if got := resolveMemory(t, k.Slug); got != "8192M" {
		t.Errorf("persisted memory = %q, want 8192M", got)
	}
}

// TestDetectAndOfferMemoryRaiseConfirmsTheWrittenConfig pins that an accepted
// raise names the value and the config directory it was written to.
func TestDetectAndOfferMemoryRaiseConfirmsTheWrittenConfig(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	testutil.UnsetEnv(t, "AGENTS_SANDBOX_MEMORY")
	testutil.UnsetEnv(t, "OPENCODE_SANDBOX_MEMORY")
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})
	r := &msb.MockSandbox{LogsResult: []msb.LogEntry{oomLogLine(time.Now())}}
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "raise", nil },
		InputFn:             func(string, string) (string, error) { return "8G", nil },
	}

	detectRun(t, r, nil, 4096, 8192, false, ui)

	wantDir := configpaths.Get().UserSlugConfigDir(k.Slug)
	if !slices.ContainsFunc(ui.InfoCalls, func(message string) bool {
		return strings.Contains(message, "8G") && strings.Contains(message, wantDir)
	}) {
		t.Errorf("info messages %q do not confirm 8G written to %s", ui.InfoCalls, wantDir)
	}
}

// TestDetectAndOfferMemoryRaiseShadowedExplainsInsteadOfPrompting pins that a
// memory value the --memory flag or the environment supplies is reported
// before any prompt: a raise saved to config could not take effect, so the user
// is told where the value comes from, nothing is written, and the hit counts
// as answered.
func TestDetectAndOfferMemoryRaiseShadowedExplainsInsteadOfPrompting(t *testing.T) {
	tests := []struct {
		name       string
		env        string
		flag       bool
		wantSource string
	}{
		{name: "environment", env: "16G", wantSource: "AGENTS_SANDBOX_MEMORY"},
		{name: "flag", flag: true, wantSource: "--memory"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			configpathstest.WithMockConfigPaths(t)
			testutil.UnsetEnv(t, "OPENCODE_SANDBOX_MEMORY")
			t.Setenv("AGENTS_SANDBOX_MEMORY", tc.env)
			k := detectKey()
			seedDetectState(t, k, state.HomeState{})
			r := &msb.MockSandbox{LogsResult: []msb.LogEntry{oomLogLine(time.Now())}}
			prompted := false
			ui := &termio.Mock{
				IsInteractiveResult: true,
				SelectFn: func(string, []termio.Choice, string) (string, error) {
					prompted = true
					return "raise", nil
				},
			}

			if raised := DetectAndOfferMemoryRaise(
				context.Background(),
				k,
				r,
				nil,
				MemoryLimits{CurrentMiB: 4096, BootMaxMiB: 8192, HostAvailableMiB: 0, HostAvailableKnown: false},
				false,
				options.RunOptions{ExplicitFlags: explicitFlags(tc.flag, memoryKey)},
				configpaths.Get(),
				ui,
			); raised != "" {
				t.Errorf("raised = %q, want none", raised)
			}

			if prompted {
				t.Error("prompted for a raise that config cannot apply")
			}
			if !slices.ContainsFunc(ui.WarnCalls, func(message string) bool {
				return strings.Contains(message, tc.wantSource)
			}) {
				t.Errorf("warnings %q do not name %s", ui.WarnCalls, tc.wantSource)
			}
			if marker := readDetectMarker(t, k); marker == nil || marker.OfferedAt.IsZero() {
				t.Errorf("marker = %+v, want the hit counted as answered", marker)
			}
			if _, err := os.Stat(
				filepath.Join(configpaths.Get().UserSlugConfigDir(k.Slug), "config.yaml"),
			); !os.IsNotExist(
				err,
			) {
				t.Errorf("config written despite the shadow, stat err = %v", err)
			}
		})
	}
}

// TestDetectAndOfferMemoryRaiseAtTheBootMaximumExplains pins that a VM already
// at its boot maximum gets an explanation instead of a prompt no answer could
// satisfy, and the hit counts as answered.
func TestDetectAndOfferMemoryRaiseAtTheBootMaximumExplains(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	testutil.UnsetEnv(t, "AGENTS_SANDBOX_MEMORY")
	testutil.UnsetEnv(t, "OPENCODE_SANDBOX_MEMORY")
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})
	r := &msb.MockSandbox{LogsResult: []msb.LogEntry{oomLogLine(time.Now())}}
	prompted := false
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(string, []termio.Choice, string) (string, error) {
			prompted = true
			return "raise", nil
		},
	}

	if raised := detectRun(t, r, nil, 8192, 8192, false, ui); raised != "" {
		t.Errorf("raised = %q, want none", raised)
	}

	if prompted {
		t.Error("prompted for a raise above the boot maximum")
	}
	if !warnContains(ui, "host maximum 8G") {
		t.Errorf("warnings %q do not explain the host maximum", ui.WarnCalls)
	}
	if marker := readDetectMarker(t, k); marker == nil || marker.OfferedAt.IsZero() {
		t.Errorf("marker = %+v, want the hit counted as answered", marker)
	}
}

// TestDetectAndOfferMemoryRaiseAnsweredKillStaysAnsweredAfterStop pins the
// fingerprint across the VM's idle stop (observed on a KVM host): the
// post-attach offer reads the running VM, where the kernel line carries the
// time it was written; the next launch reads the stopped VM, where the runtime
// re-stamped it with the stop time. The answered kill must not be offered
// again.
func TestDetectAndOfferMemoryRaiseAnsweredKillStaysAnsweredAfterStop(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	testutil.UnsetEnv(t, "AGENTS_SANDBOX_MEMORY")
	testutil.UnsetEnv(t, "OPENCODE_SANDBOX_MEMORY")
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})
	bootStart := time.Now().Add(-time.Minute)
	bootLine := msb.LogEntry{
		Source:    msb.LogSourceSystem,
		Timestamp: bootStart,
		Data:      []byte("INFO microsandbox_runtime::runner::vm: sandbox starting sandbox=project-vm"),
	}
	kernelKill := func(loggedAt time.Time) msb.LogEntry {
		return msb.LogEntry{
			Source:    msb.LogSourceSystem,
			Timestamp: loggedAt,
			Data:      []byte("[    1.732160] Out of memory: Killed process 464 (opencode) total-vm:1kB"),
		}
	}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}

	running := &msb.MockSandbox{LogsResult: []msb.LogEntry{bootLine, kernelKill(bootStart.Add(2 * time.Second))}}
	detectRun(t, running, nil, 4096, 8192, false, ui)
	stopped := &msb.MockSandbox{LogsResult: []msb.LogEntry{bootLine, kernelKill(bootStart.Add(5 * time.Second))}}
	detectRun(t, stopped, readDetectMarker(t, k), 4096, 8192, false, ui)

	if selects != 1 {
		t.Fatalf("Select called %d times, want 1: the stopped VM's re-stamped line is the same kill", selects)
	}
}

// TestDetectAndOfferMemoryRaiseReadsBackToTheBootStart pins the second log
// read that finds the kill's boot start even when it has left the tail: it is
// bounded by the newest OOM line and has no tail limit.
func TestDetectAndOfferMemoryRaiseReadsBackToTheBootStart(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})
	kill := time.Now()
	r := &msb.MockSandbox{LogsResult: []msb.LogEntry{oomLogLine(kill)}}
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "keep", nil },
	}

	detectRun(t, r, nil, 4096, 8192, false, ui)

	if len(r.LogsOptions) != 2 {
		t.Fatalf("Logs read %d times, want the tail read and the boot-start read", len(r.LogsOptions))
	}
	backRead := r.LogsOptions[1]
	if !backRead.Until.Equal(kill) || backRead.Tail != 0 ||
		!slices.Equal(backRead.Sources, []msb.LogSource{msb.LogSourceSystem}) {
		t.Errorf("boot-start read = %+v, want the system log until %v without a tail limit", backRead, kill)
	}
}

// TestDetectAndOfferMemoryRaiseBootStartReadErrorIsBestEffort pins that a
// failing boot-start read keeps the tail read's evidence.
func TestDetectAndOfferMemoryRaiseBootStartReadErrorIsBestEffort(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})
	r := &failingBackReadSandbox{LogsResult: []msb.LogEntry{oomLogLine(time.Now())}}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}

	detectRun(t, r, nil, 4096, 8192, false, ui)

	if selects != 1 {
		t.Errorf("Select called %d times, want the kill offered from the tail read", selects)
	}
	if !verboseContains(ui, "reading the VM system log back to the boot start failed") {
		t.Errorf("verbose calls %v do not report the failed read", ui.VerboseCalls)
	}
}

// TestDetectAndOfferMemoryRaiseFindsTheBootStartBeyondTheTail pins the
// back-read: the kill's boot start has left the tail, so only the read back to
// it gives the stable fingerprint that keeps an answered kill answered.
func TestDetectAndOfferMemoryRaiseFindsTheBootStartBeyondTheTail(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	bootStart := time.Now().Add(-time.Hour)
	kill := bootStart.Add(1_732_160 * time.Microsecond)
	seedDetectState(t, k, state.HomeState{ResourcePressure: &state.ResourcePressureState{
		Kind: "memory", Evidence: string(pressure.EvidenceOOMLog), Victim: "opencode",
		OOMSeenAt: kill, OfferedAt: time.Now(),
	}})
	r := &splitLogSandbox{
		tail: []msb.LogEntry{{
			Source:    msb.LogSourceSystem,
			Timestamp: bootStart.Add(time.Minute),
			Data:      []byte("[    1.732160] Out of memory: Killed process 464 (opencode) total-vm:1kB"),
		}},
		earlier: []msb.LogEntry{{
			Source:    msb.LogSourceSystem,
			Timestamp: bootStart,
			Data:      []byte("INFO microsandbox_runtime::runner::vm: sandbox starting sandbox=project-vm"),
		}},
	}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}

	detectRun(t, r, readDetectMarker(t, k), 4096, 8192, false, ui)

	if selects != 0 {
		t.Fatalf("Select called %d times, want 0: the kill is the answered one", selects)
	}
}

// TestDetectAndOfferMemoryRaiseReadsBackOnlyForAnOOMLine pins that a tail
// without an OOM line costs no second log read.
func TestDetectAndOfferMemoryRaiseReadsBackOnlyForAnOOMLine(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})
	r := &msb.MockSandbox{}

	detectRun(t, r, nil, 4096, 8192, false, &termio.Mock{})

	if len(r.LogsOptions) != 1 {
		t.Errorf("Logs read %d times, want only the tail read", len(r.LogsOptions))
	}
}

// splitLogSandbox serves the tail read and the read back to the boot start from
// separate entries.
type splitLogSandbox struct {
	msb.MockSandbox

	tail    []msb.LogEntry
	earlier []msb.LogEntry
}

func (s *splitLogSandbox) Logs(_ context.Context, query msb.LogQuery) ([]msb.LogEntry, error) {
	if query.Until.IsZero() {
		return s.tail, nil
	}
	return s.earlier, nil
}

// failingBackReadSandbox fails every log read bounded by Until.
type failingBackReadSandbox struct {
	msb.MockSandbox
}

func (s *failingBackReadSandbox) Logs(ctx context.Context, query msb.LogQuery) ([]msb.LogEntry, error) {
	if !query.Until.IsZero() {
		return nil, errors.New("log read failed")
	}
	return s.MockSandbox.Logs(ctx, query)
}

// TestDetectAndOfferMemoryRaiseWarnsWhenTheHostLacksTheMemory pins the
// host-bound warning: an accepted raise whose increase exceeds the host's
// available memory is still persisted, with a warning naming the available
// memory; an unknown available figure warns nothing, while a known zero (an
// exhausted host) warns.
func TestDetectAndOfferMemoryRaiseWarnsWhenTheHostLacksTheMemory(t *testing.T) {
	tests := []struct {
		name               string
		hostAvailableMiB   uint32
		hostAvailableKnown bool
		wantWarning        bool
	}{
		{
			name:               "increase above the available memory",
			hostAvailableMiB:   2048,
			hostAvailableKnown: true,
			wantWarning:        true,
		},
		{name: "increase equal to the available memory", hostAvailableMiB: 4096, hostAvailableKnown: true},
		{name: "host memory exhausted", hostAvailableMiB: 0, hostAvailableKnown: true, wantWarning: true},
		{name: "available memory unknown", hostAvailableMiB: 0, hostAvailableKnown: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			configpathstest.WithMockConfigPaths(t)
			testutil.UnsetEnv(t, "AGENTS_SANDBOX_MEMORY")
			testutil.UnsetEnv(t, "OPENCODE_SANDBOX_MEMORY")
			k := detectKey()
			seedDetectState(t, k, state.HomeState{})
			r := &msb.MockSandbox{LogsResult: []msb.LogEntry{oomLogLine(time.Now())}}
			ui := &termio.Mock{
				IsInteractiveResult: true,
				SelectFn:            func(string, []termio.Choice, string) (string, error) { return "raise", nil },
				InputFn:             func(string, string) (string, error) { return "8G", nil },
			}

			raised := DetectAndOfferMemoryRaise(
				context.Background(), k, r, nil,
				MemoryLimits{
					CurrentMiB:         4096,
					BootMaxMiB:         16384,
					HostAvailableMiB:   tc.hostAvailableMiB,
					HostAvailableKnown: tc.hostAvailableKnown,
				},
				false, options.RunOptions{}, configpaths.Get(), ui,
			)

			if raised != "8192M" {
				t.Errorf("raised = %q, want the raise persisted regardless of the warning", raised)
			}
			if warned := warnContains(ui, "the host has"); warned != tc.wantWarning {
				t.Errorf("warnings %q, want a host-memory warning = %v", ui.WarnCalls, tc.wantWarning)
			}
		})
	}
}

// TestDetectAndOfferMemoryRaiseDeclineAdvancesMarker pins the decline answer:
// it advances the marker without persisting a raise, so a second detection with
// the same kill does not re-offer.
func TestDetectAndOfferMemoryRaiseDeclineAdvancesMarker(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})
	kill := time.Now()
	r := &msb.MockSandbox{LogsResult: []msb.LogEntry{oomLogLine(kill)}}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}

	if raised := detectRun(t, r, nil, 4096, 8192, false, ui); raised != "" {
		t.Errorf("raised = %q, want none", raised)
	}
	detectRun(t, r, readDetectMarker(t, k), 4096, 8192, false, ui)

	if selects != 1 {
		t.Fatalf("Select called %d times, want 1 (a declined kill is not re-offered)", selects)
	}
	marker := readDetectMarker(t, k)
	if marker == nil || marker.OfferedAt.IsZero() {
		t.Fatalf("marker = %+v, want the decline recorded", marker)
	}
	if _, err := os.Stat(
		filepath.Join(configpaths.Get().UserSlugConfigDir(k.Slug), "config.yaml"),
	); !os.IsNotExist(
		err,
	) {
		t.Errorf("a decline wrote a config value; want none (stat err = %v)", err)
	}
}

// TestDetectAndOfferMemoryRaiseNonInteractiveRecordsWithoutAnswer pins a
// non-interactive detection records the hit but leaves it unanswered so a later
// interactive run offers it.
func TestDetectAndOfferMemoryRaiseNonInteractiveRecordsWithoutAnswer(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})
	kill := time.Now()
	r := &msb.MockSandbox{LogsResult: []msb.LogEntry{oomLogLine(kill)}}
	ui := &termio.Mock{}

	if raised := detectRun(t, r, nil, 4096, 8192, false, ui); raised != "" {
		t.Errorf("raised = %q, want none", raised)
	}

	marker := readDetectMarker(t, k)
	if marker == nil || !marker.OOMSeenAt.Equal(kill) || !marker.OfferedAt.IsZero() {
		t.Fatalf("marker = %+v, want the kill recorded but unanswered", marker)
	}
	if !verboseContains(ui, "later interactive run") {
		t.Errorf("verbose calls %v do not report the deferred offer", ui.VerboseCalls)
	}
}

// TestDetectAndOfferMemoryRaiseUnansweredOOMWithoutLogOffers pins that a
// recorded-but-unanswered OOM kill is still offered when its log line has left
// the read tail (or the VM was recreated): the marker alone keeps the promise to
// the non-interactive run (specs/resources/resource-limit-proposal.md).
func TestDetectAndOfferMemoryRaiseUnansweredOOMWithoutLogOffers(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	kill := time.Now().Add(-time.Hour)
	seedDetectState(t, k, state.HomeState{ResourcePressure: &state.ResourcePressureState{
		Kind:      "memory",
		Evidence:  string(pressure.EvidenceOOMLog),
		Victim:    "opencode",
		OOMSeenAt: kill,
		OfferedAt: time.Time{},
	}})
	r := &msb.MockSandbox{} // no log line at all
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}

	detectRun(t, r, readDetectMarker(t, k), 4096, 8192, false, ui)

	if selects != 1 {
		t.Fatalf("Select called %d times, want 1 for the recorded unanswered kill", selects)
	}
	marker := readDetectMarker(t, k)
	if marker == nil || marker.OfferedAt.IsZero() {
		t.Fatalf("marker = %+v, want the offer answered", marker)
	}
	if marker.Kind != "memory" || marker.Evidence != string(pressure.EvidenceOOMLog) {
		t.Errorf("marker = %+v, want an OOM-log hit reconstructed from the marker", marker)
	}
}

// TestDetectAndOfferMemoryRaiseNoKnownLimitRecordsWithoutOffering pins the
// no-basis guard: an OOM line whose metrics read failed leaves currentMiB
// unknown, so the detection records the hit unanswered rather than offering a
// raise whose suggestion (SuggestRaise(0, …)) would pre-fill a decrease.
func TestDetectAndOfferMemoryRaiseNoKnownLimitRecordsWithoutOffering(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})
	kill := time.Now()
	r := &msb.MockSandbox{
		LogsResult: []msb.LogEntry{oomLogLine(kill)},
		MetricsErr: errors.New("metrics boom"),
	}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "raise", nil },
	}

	if raised := detectRun(t, r, nil, 0, 0, false, ui); raised != "" {
		t.Errorf("raised = %q, want none", raised)
	}

	if selects != 0 {
		t.Fatalf("Select called %d times, want 0 without a known limit", selects)
	}
	marker := readDetectMarker(t, k)
	if marker == nil || !marker.OOMSeenAt.Equal(kill) || !marker.OfferedAt.IsZero() {
		t.Fatalf("marker = %+v, want the kill recorded but unanswered", marker)
	}
	if !verboseContains(ui, "no known limit") {
		t.Errorf("verbose calls %v do not report the missing-limit deferral", ui.VerboseCalls)
	}
}

// TestDetectAndOfferMemoryRaiseWitnessedHeadroomTripOffersWhenMetricsRecovered
// pins the post-attach addition: a trip the caller witnessed in-session still
// offers a headroom raise even though the fresh metrics read has recovered.
func TestDetectAndOfferMemoryRaiseWitnessedHeadroomTripOffersWhenMetricsRecovered(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})
	r := &msb.MockSandbox{MetricsResult: detectRecoveredMetrics()}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}

	detectRun(t, r, nil, 4096, 8192, true, ui)

	if selects != 1 {
		t.Fatalf("Select called %d times, want 1 for the witnessed trip", selects)
	}
	marker := readDetectMarker(t, k)
	if marker == nil || marker.Evidence != string(pressure.EvidenceHeadroom) || marker.OfferedAt.IsZero() {
		t.Fatalf("marker = %+v, want a headroom hit recorded and answered", marker)
	}
}

// TestDetectAndOfferMemoryRaiseWitnessedTripStillTrippedUsesMetricsSignal pins
// that when the witnessed trip and the metrics read both report pressure, the
// metrics-based headroom signal (with its used/limit) is used.
func TestDetectAndOfferMemoryRaiseWitnessedTripStillTrippedUsesMetricsSignal(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})
	r := &msb.MockSandbox{MetricsResult: detectTrippedMetrics()}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}

	detectRun(t, r, nil, 4096, 8192, true, ui)

	if selects != 1 {
		t.Fatalf("Select called %d times, want 1", selects)
	}
	marker := readDetectMarker(t, k)
	if marker == nil || marker.Evidence != string(pressure.EvidenceHeadroom) {
		t.Fatalf("marker = %+v, want a headroom hit", marker)
	}
}

// TestDetectAndOfferMemoryRaiseFreshHeadroomAloneDoesNotOffer pins that a
// tripped metrics read the session watcher did not witness is no hit: a VM
// that is merely busy (another client at launch) must not prompt.
func TestDetectAndOfferMemoryRaiseFreshHeadroomAloneDoesNotOffer(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})
	r := &msb.MockSandbox{MetricsResult: detectTrippedMetrics()}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}

	if raised := detectRun(t, r, nil, 4096, 8192, false, ui); raised != "" {
		t.Errorf("raised = %q, want none", raised)
	}

	if selects != 0 {
		t.Fatalf("Select called %d times, want 0 for an unwitnessed headroom read", selects)
	}
	if marker := readDetectMarker(t, k); marker != nil {
		t.Errorf("marker = %+v, want nothing recorded", marker)
	}
}

// TestDetectAndOfferMemoryRaiseHeadroomTripOffers pins the metrics-fallback
// offer: a witnessed trip still shown by the fresh snapshot, with no OOM log,
// prompts once and timestamps the headroom fingerprint.
func TestDetectAndOfferMemoryRaiseHeadroomTripOffers(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})
	r := &msb.MockSandbox{MetricsResult: detectTrippedMetrics()}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}

	detectRun(t, r, nil, 4096, 8192, true, ui)

	if selects != 1 {
		t.Fatalf("Select called %d times, want 1 for the headroom trip", selects)
	}
	marker := readDetectMarker(t, k)
	if marker == nil || marker.OOMSeenAt.IsZero() {
		t.Fatalf("marker = %+v, want the headroom fingerprint timestamped", marker)
	}
}

// TestDetectAndOfferMemoryRaiseAnsweredHeadroomNotReOffered pins the anti-nag
// hysteresis: a headroom offer already answered is not re-offered for a new
// witnessed trip while the condition is still tripped (the snapshot never
// recovers below ReArmPercent).
func TestDetectAndOfferMemoryRaiseAnsweredHeadroomNotReOffered(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	seedDetectState(t, k, state.HomeState{ResourcePressure: &state.ResourcePressureState{
		Kind: "memory", Evidence: string(pressure.EvidenceHeadroom), OOMSeenAt: time.Now(), OfferedAt: time.Now(),
	}})
	r := &msb.MockSandbox{MetricsResult: detectTrippedMetrics()}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}

	detectRun(t, r, readDetectMarker(t, k), 4096, 8192, true, ui)

	if selects != 0 {
		t.Fatalf("Select called %d times, want 0 while the answered trip still fires", selects)
	}
}

// TestDetectAndOfferMemoryRaiseReArmsAnsweredHeadroomOnRecovery pins the re-arm
// rewrite: recovered metrics clear a recorded headroom condition, preserving the
// recorded OOM timestamp, without offering.
func TestDetectAndOfferMemoryRaiseReArmsAnsweredHeadroomOnRecovery(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	seen := time.Now().Add(-time.Hour)
	seedDetectState(t, k, state.HomeState{ResourcePressure: &state.ResourcePressureState{
		Kind: "memory", Evidence: string(pressure.EvidenceHeadroom), OOMSeenAt: seen, OfferedAt: seen,
	}})
	r := &msb.MockSandbox{MetricsResult: detectRecoveredMetrics()}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}

	detectRun(t, r, readDetectMarker(t, k), 4096, 8192, false, ui)

	if selects != 0 {
		t.Fatalf("Select called %d times, want 0 on a recovery observation", selects)
	}
	marker := readDetectMarker(t, k)
	if marker == nil || marker.Evidence == string(pressure.EvidenceHeadroom) {
		t.Fatalf("marker = %+v, want the headroom condition cleared on recovery", marker)
	}
	if !marker.OOMSeenAt.Equal(seen) {
		t.Errorf("OOMSeenAt = %v, want the recorded hit %v preserved", marker.OOMSeenAt, seen)
	}
}

// TestDetectAndOfferMemoryRaiseUnansweredHeadroomRecoveredStillOffers pins
// that recovery does not discard a headroom hit whose run never answered it: the
// offer is kept from the marker even though the fresh metrics recovered.
func TestDetectAndOfferMemoryRaiseUnansweredHeadroomRecoveredStillOffers(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	seen := time.Now().Add(-time.Hour)
	seedDetectState(t, k, state.HomeState{ResourcePressure: &state.ResourcePressureState{
		Kind: "memory", Evidence: string(pressure.EvidenceHeadroom), OOMSeenAt: seen,
	}})
	r := &msb.MockSandbox{MetricsResult: detectRecoveredMetrics()}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}

	detectRun(t, r, readDetectMarker(t, k), 4096, 8192, false, ui)

	if selects != 1 {
		t.Fatalf("Select called %d times, want 1 for an unanswered recovered hit", selects)
	}
	marker := readDetectMarker(t, k)
	if marker == nil || marker.OfferedAt.IsZero() {
		t.Fatalf("marker = %+v, want the answered hit retained", marker)
	}
	if marker.Kind != "memory" || marker.Evidence != string(pressure.EvidenceHeadroom) {
		t.Errorf("marker = %+v, want a headroom hit reconstructed from the marker", marker)
	}
}

// TestDetectAndOfferMemoryRaiseHeadroomSeenAtPreserved pins the headroom
// fingerprint: an already-recorded unanswered headroom hit keeps its first-seen
// time rather than starting a new one, whether it is offered from the marker
// alone or for a new trip the session watcher witnessed.
func TestDetectAndOfferMemoryRaiseHeadroomSeenAtPreserved(t *testing.T) {
	for _, witnessed := range []bool{false, true} {
		t.Run(fmt.Sprintf("witnessed=%v", witnessed), func(t *testing.T) {
			configpathstest.WithMockConfigPaths(t)
			k := detectKey()
			seen := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
			seedDetectState(t, k, state.HomeState{ResourcePressure: &state.ResourcePressureState{
				Kind: "memory", Evidence: string(pressure.EvidenceHeadroom), OOMSeenAt: seen,
			}})
			r := &msb.MockSandbox{MetricsResult: detectTrippedMetrics()}
			var selects int
			ui := &termio.Mock{
				IsInteractiveResult: true,
				SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
			}

			detectRun(t, r, readDetectMarker(t, k), 4096, 8192, witnessed, ui)

			if selects != 1 {
				t.Fatalf("Select called %d times, want 1 for the still-unanswered headroom hit", selects)
			}
			marker := readDetectMarker(t, k)
			if marker == nil || !marker.OOMSeenAt.Equal(seen) {
				t.Fatalf("marker = %+v, want the first-seen headroom time %v preserved", marker, seen)
			}
		})
	}
}

// TestConfigSlugPrefersResolverSlug pins that a config write targets the
// resolver's slug when the run carried one, and falls back to the state key's slug
// for non-CLI callers.
func TestConfigSlugPrefersResolverSlug(t *testing.T) {
	k := state.Key{Slug: "state-slug", Agent: "opencode"}
	if got := configSlug(options.RunOptions{ConfigSlug: "resolver-slug"}, k); got != "resolver-slug" {
		t.Errorf("configSlug = %q, want resolver-slug", got)
	}
	if got := configSlug(options.RunOptions{}, k); got != "state-slug" {
		t.Errorf("configSlug = %q, want state-slug", got)
	}
}

// TestDetectAndOfferMemoryRaiseNewerOOMOffers pins the oomNeedsOffer newest-kill
// branch: a kill newer than the marker's recorded one is offered.
func TestDetectAndOfferMemoryRaiseNewerOOMOffers(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	oldKill := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedDetectState(t, k, state.HomeState{ResourcePressure: &state.ResourcePressureState{
		Kind: "memory", Evidence: string(pressure.EvidenceOOMLog), Victim: "old",
		OOMSeenAt: oldKill, OfferedAt: oldKill.Add(time.Minute),
	}})
	newKill := oldKill.Add(time.Hour)
	r := &msb.MockSandbox{LogsResult: []msb.LogEntry{oomLogLine(newKill)}}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}

	detectRun(t, r, readDetectMarker(t, k), 4096, 8192, false, ui)

	if selects != 1 {
		t.Fatalf("Select called %d times, want 1 for a newer kill", selects)
	}
	marker := readDetectMarker(t, k)
	if marker == nil || !marker.OOMSeenAt.Equal(newKill) {
		t.Fatalf("marker = %+v, want the newer kill recorded", marker)
	}
}

// TestDetectAndOfferMemoryRaiseSameTimestampUnansweredReOffers pins the
// oomNeedsOffer equality branch: a recorded kill at the same timestamp that was
// never answered is still offered.
func TestDetectAndOfferMemoryRaiseSameTimestampUnansweredReOffers(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	kill := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	seedDetectState(t, k, state.HomeState{ResourcePressure: &state.ResourcePressureState{
		Kind: "memory", Evidence: string(pressure.EvidenceOOMLog), OOMSeenAt: kill,
	}})
	r := &msb.MockSandbox{LogsResult: []msb.LogEntry{oomLogLine(kill)}}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}

	detectRun(t, r, readDetectMarker(t, k), 4096, 8192, false, ui)

	if selects != 1 {
		t.Fatalf("Select called %d times, want 1 for an unanswered same-timestamp kill", selects)
	}
}

// TestDetectAndOfferMemoryRaiseSameTimestampAnsweredNoOffer pins the negative
// of the oomNeedsOffer equality branch: an answered kill at the same timestamp
// is not re-offered.
func TestDetectAndOfferMemoryRaiseSameTimestampAnsweredNoOffer(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	kill := time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC)
	seedDetectState(t, k, state.HomeState{ResourcePressure: &state.ResourcePressureState{
		Kind: "memory", Evidence: string(pressure.EvidenceOOMLog), OOMSeenAt: kill, OfferedAt: kill.Add(time.Minute),
	}})
	r := &msb.MockSandbox{LogsResult: []msb.LogEntry{oomLogLine(kill)}}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}

	detectRun(t, r, readDetectMarker(t, k), 4096, 8192, false, ui)

	if selects != 0 {
		t.Fatalf("Select called %d times, want 0 for an answered kill", selects)
	}
}

// TestDetectAndOfferMemoryRaiseOlderOOMThanMarkerNotOffered pins the
// oomNeedsOffer newest-kill comparison: a log line older than the recorded kill
// is not a new hit and is not offered.
func TestDetectAndOfferMemoryRaiseOlderOOMThanMarkerNotOffered(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	markerSeen := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	olderLog := markerSeen.Add(-time.Hour)
	seedDetectState(t, k, state.HomeState{ResourcePressure: &state.ResourcePressureState{
		Kind: "memory", Evidence: string(pressure.EvidenceOOMLog), OOMSeenAt: markerSeen,
	}})
	r := &msb.MockSandbox{LogsResult: []msb.LogEntry{oomLogLine(olderLog)}}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}

	detectRun(t, r, readDetectMarker(t, k), 4096, 8192, false, ui)

	if selects != 0 {
		t.Fatalf("Select called %d times, want 0 for a log line older than the marker", selects)
	}
}

// TestDetectAndOfferMemoryRaiseAnsweredOOMWithoutLogNotOffered pins that an
// answered OOM kill whose log line has left the tail is not re-offered.
func TestDetectAndOfferMemoryRaiseAnsweredOOMWithoutLogNotOffered(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	seen := time.Now().Add(-time.Hour)
	seedDetectState(t, k, state.HomeState{ResourcePressure: &state.ResourcePressureState{
		Kind: "memory", Evidence: string(pressure.EvidenceOOMLog), OOMSeenAt: seen, OfferedAt: seen,
	}})
	r := &msb.MockSandbox{}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}

	detectRun(t, r, readDetectMarker(t, k), 4096, 8192, false, ui)

	if selects != 0 {
		t.Fatalf("Select called %d times, want 0 for an answered OOM with no log line", selects)
	}
}

// TestDetectAndOfferMemoryRaiseAnsweredHeadroomUnknownMetricsNotOffered pins
// that an answered headroom hit is not re-offered when the metrics read cannot
// establish recovery (so no re-arm happened).
func TestDetectAndOfferMemoryRaiseAnsweredHeadroomUnknownMetricsNotOffered(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	seen := time.Now().Add(-time.Hour)
	seedDetectState(t, k, state.HomeState{ResourcePressure: &state.ResourcePressureState{
		Kind: "memory", Evidence: string(pressure.EvidenceHeadroom), OOMSeenAt: seen, OfferedAt: seen,
	}})
	r := &msb.MockSandbox{}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}

	detectRun(t, r, readDetectMarker(t, k), 4096, 8192, false, ui)

	if selects != 0 {
		t.Fatalf("Select called %d times, want 0 for an answered headroom hit without recovery", selects)
	}
}

// TestDetectAndOfferMemoryRaiseHeadroomOverAnsweredOOMMarker pins that a new
// witnessed headroom trip is offered even when the marker is an answered OOM kill, and
// that the headroom fingerprint starts now rather than reusing the OOM
// timestamp.
func TestDetectAndOfferMemoryRaiseHeadroomOverAnsweredOOMMarker(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	oomSeen := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	seedDetectState(t, k, state.HomeState{ResourcePressure: &state.ResourcePressureState{
		Kind: "memory", Evidence: string(pressure.EvidenceOOMLog), OOMSeenAt: oomSeen, OfferedAt: oomSeen,
	}})
	r := &msb.MockSandbox{MetricsResult: detectTrippedMetrics()}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}

	detectRun(t, r, readDetectMarker(t, k), 4096, 8192, true, ui)

	if selects != 1 {
		t.Fatalf("Select called %d times, want 1 for a headroom trip over an answered OOM marker", selects)
	}
	marker := readDetectMarker(t, k)
	if marker == nil || !marker.OOMSeenAt.After(oomSeen) {
		t.Fatalf("marker = %+v, want a fresh headroom fingerprint newer than the old OOM kill %v", marker, oomSeen)
	}
}

// TestDetectAndOfferMemoryRaiseHeadroomZeroFingerprintGetsNow pins the headroom
// fingerprint fallback: a recorded headroom hit with no timestamp starts a fresh
// fingerprint at now rather than recording the zero time.
func TestDetectAndOfferMemoryRaiseHeadroomZeroFingerprintGetsNow(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	seedDetectState(t, k, state.HomeState{ResourcePressure: &state.ResourcePressureState{
		Kind: "memory", Evidence: string(pressure.EvidenceHeadroom),
	}})
	r := &msb.MockSandbox{MetricsResult: detectTrippedMetrics()}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}

	detectRun(t, r, readDetectMarker(t, k), 4096, 8192, true, ui)

	if selects != 1 {
		t.Fatalf("Select called %d times, want 1 for the headroom hit", selects)
	}
	marker := readDetectMarker(t, k)
	if marker == nil || marker.OOMSeenAt.IsZero() {
		t.Fatalf("marker = %+v, want a non-zero headroom fingerprint", marker)
	}
}

// TestDetectAndOfferMemoryRaiseOOMEvidenceNotReArmed pins that the re-arm path
// only clears a headroom marker: an answered OOM kill is left untouched by a
// recovered metrics read.
func TestDetectAndOfferMemoryRaiseOOMEvidenceNotReArmed(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	seen := time.Now().Add(-time.Hour)
	seedDetectState(t, k, state.HomeState{ResourcePressure: &state.ResourcePressureState{
		Kind: "memory", Evidence: string(pressure.EvidenceOOMLog), Victim: "opencode", OOMSeenAt: seen, OfferedAt: seen,
	}})
	r := &msb.MockSandbox{MetricsResult: detectRecoveredMetrics()}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}

	detectRun(t, r, readDetectMarker(t, k), 4096, 8192, false, ui)

	if selects != 0 {
		t.Fatalf("Select called %d times, want 0", selects)
	}
	marker := readDetectMarker(t, k)
	if marker == nil || marker.Evidence != string(pressure.EvidenceOOMLog) {
		t.Fatalf("marker = %+v, want the answered OOM evidence preserved", marker)
	}
}

// TestDetectAndOfferMemoryRaiseReArmThenWitnessedTripOffers pins that re-arming
// an answered headroom hit clears the local marker too, so a trip witnessed in
// the same session still offers.
func TestDetectAndOfferMemoryRaiseReArmThenWitnessedTripOffers(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	seen := time.Now().Add(-time.Hour)
	seedDetectState(t, k, state.HomeState{ResourcePressure: &state.ResourcePressureState{
		Kind: "memory", Evidence: string(pressure.EvidenceHeadroom), OOMSeenAt: seen, OfferedAt: seen,
	}})
	r := &msb.MockSandbox{MetricsResult: detectRecoveredMetrics()}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}

	detectRun(t, r, readDetectMarker(t, k), 4096, 8192, true, ui)

	if selects != 1 {
		t.Fatalf("Select called %d times, want 1 after a re-arm plus a witnessed trip", selects)
	}
}

// TestOOMLogTailBound pins the system-log tail bound.
func TestOOMLogTailBound(t *testing.T) {
	if oomLogTail != 200 {
		t.Errorf("oomLogTail = %d, want 200", oomLogTail)
	}
}

// TestDetectAndOfferMemoryRaiseLogReadErrorIsBestEffort pins that a system-log
// read failure is logged and never offers or fails.
func TestDetectAndOfferMemoryRaiseLogReadErrorIsBestEffort(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})
	r := &msb.MockSandbox{LogsErr: errors.New("logs boom")}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "raise", nil },
	}

	detectRun(t, r, nil, 4096, 8192, false, ui)

	if selects != 0 {
		t.Fatalf("Select called %d times, want 0 after a log read error", selects)
	}
	if !verboseContains(ui, "reading VM system log failed") {
		t.Errorf("verbose calls %v do not report the log read failure", ui.VerboseCalls)
	}
}

// TestDetectAndOfferMemoryRaiseMetricsReadErrorIsBestEffort pins that a metrics
// read failure is logged and a log-free detection does not offer.
func TestDetectAndOfferMemoryRaiseMetricsReadErrorIsBestEffort(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})
	r := &msb.MockSandbox{MetricsErr: errors.New("metrics boom")}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "raise", nil },
	}

	detectRun(t, r, nil, 4096, 8192, false, ui)

	if selects != 0 {
		t.Fatalf("Select called %d times, want 0 after a metrics read error", selects)
	}
	if !verboseContains(ui, "reading VM metrics failed") {
		t.Errorf("verbose calls %v do not report the metrics read failure", ui.VerboseCalls)
	}
}

// TestDetectAndOfferMemoryRaiseCurrentMemoryFallsBackToMetricsLimit pins the
// suggestion basis: a zero currentMiB falls back to the metrics limit.
func TestDetectAndOfferMemoryRaiseCurrentMemoryFallsBackToMetricsLimit(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	testutil.UnsetEnv(t, "AGENTS_SANDBOX_MEMORY")
	testutil.UnsetEnv(t, "OPENCODE_SANDBOX_MEMORY")
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})
	r := &msb.MockSandbox{
		LogsResult:    []msb.LogEntry{oomLogLine(time.Now())},
		MetricsResult: detectTrippedMetrics(),
	}
	var inputDefault string
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "raise", nil },
		InputFn: func(_ string, defaultValue string) (string, error) {
			inputDefault = defaultValue
			return defaultValue, nil
		},
	}

	detectRun(t, r, nil, 0, 0, false, ui)

	if inputDefault != "10G" {
		t.Errorf("Input default = %q, want 10G from the metrics limit", inputDefault)
	}
}

// TestDetectAndOfferMemoryRaisePromptErrorIsBestEffort pins that a prompt
// failure records the hit unanswered and never returns an error.
func TestDetectAndOfferMemoryRaisePromptErrorIsBestEffort(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})
	r := &msb.MockSandbox{LogsResult: []msb.LogEntry{oomLogLine(time.Now())}}
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "", errors.New("prompt boom") },
	}

	if raised := detectRun(t, r, nil, 4096, 8192, false, ui); raised != "" {
		t.Errorf("raised = %q, want none", raised)
	}

	if marker := readDetectMarker(t, k); marker == nil || !marker.OfferedAt.IsZero() {
		t.Fatalf("marker = %+v, want the hit recorded but unanswered after a prompt error", marker)
	}
	if !verboseContains(ui, "memory raise prompt failed") {
		t.Errorf("verbose calls %v do not report the prompt failure", ui.VerboseCalls)
	}
}

// TestDetectAndOfferMemoryRaisePersistErrorWarns pins that a persist refusal
// (here a JSON config the launcher does not edit) warns rather than failing,
// and still advances the marker.
func TestDetectAndOfferMemoryRaisePersistErrorWarns(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := detectKey()
	seedDetectState(t, k, state.HomeState{})
	r := &msb.MockSandbox{LogsResult: []msb.LogEntry{oomLogLine(time.Now())}}
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "raise", nil },
		InputFn:             func(string, string) (string, error) { return "8G", nil },
	}
	testutil.UnsetEnv(t, "AGENTS_SANDBOX_MEMORY")
	testutil.UnsetEnv(t, "OPENCODE_SANDBOX_MEMORY")
	configDir := configpaths.Get().UserSlugConfigDir(k.Slug)
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if raised := detectRun(t, r, nil, 4096, 8192, false, ui); raised != "" {
		t.Errorf("raised = %q, want none", raised)
	}

	if marker := readDetectMarker(t, k); marker == nil || marker.OfferedAt.IsZero() {
		t.Fatalf("marker = %+v, want the answer recorded despite the persist refusal", marker)
	}
	if !warnContains(ui, "could not persist") {
		t.Errorf("warnings %v do not report the persist failure", ui.WarnCalls)
	}
}

// TestDetectAndOfferMemoryRaiseRecordWriteErrorIsBestEffort pins the
// marker-write failure path: it logs at verbose level instead of failing.
func TestDetectAndOfferMemoryRaiseRecordWriteErrorIsBestEffort(t *testing.T) {
	withFailingDetectStateDir(t)
	ui := termio.NewTestMock(t)
	recordResourcePressure(detectKey(), state.ResourcePressureState{}, &ui)
	if !verboseContains(&ui, "recording the VM memory-limit hit failed") {
		t.Errorf("verbose calls %v do not report the marker-write failure", ui.VerboseCalls)
	}
}

// TestMarkResourcePressureOfferedErrorIsBestEffort pins the answer-write
// failure path: it logs at verbose level instead of failing.
func TestMarkResourcePressureOfferedErrorIsBestEffort(t *testing.T) {
	withFailingDetectStateDir(t)
	ui := termio.NewTestMock(t)
	markResourcePressureOffered(detectKey(), time.Now(), &ui)
	if !verboseContains(&ui, "recording the memory raise answer failed") {
		t.Errorf("verbose calls %v do not report the answer-write failure", ui.VerboseCalls)
	}
}

// verboseContains reports whether any verbose call contains want.
func verboseContains(ui *termio.Mock, want string) bool {
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, want) {
			return true
		}
	}
	return false
}

// warnContains reports whether any warning contains want.
func warnContains(ui *termio.Mock, want string) bool {
	for _, call := range ui.WarnCalls {
		if strings.Contains(call, want) {
			return true
		}
	}
	return false
}

// failingDetectStateDirConfigPaths points only UserStateDir at a regular file,
// so reads and writes of the per-project state file fail.
type failingDetectStateDirConfigPaths struct {
	configpaths.ConfigPaths

	stateDir string
}

func (f failingDetectStateDirConfigPaths) UserStateDir() string { return f.stateDir }

// withFailingDetectStateDir installs a ConfigPaths whose UserStateDir is a
// regular file, so any attempt to read or write <stateDir>/... fails.
func withFailingDetectStateDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "userstate")
	if err := os.WriteFile(stateFile, []byte("not-a-dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	orig := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths {
		return failingDetectStateDirConfigPaths{stateDir: stateFile}
	}
	t.Cleanup(func() { configpaths.Get = orig })
}

// explicitFlags is the run's explicit-flag list with key in it when set.
func explicitFlags(set bool, key string) []string {
	if set {
		return []string{key}
	}
	return nil
}
