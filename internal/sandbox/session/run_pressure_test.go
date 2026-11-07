package session

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/pressure"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// seedPressureMarker writes a pressure marker as the key's HomeState.
func seedPressureMarker(t *testing.T, k state.Key, marker state.ResourcePressureState) {
	t.Helper()
	if err := state.RecoverHomeState(k, func(cur *state.HomeState, _ bool) (bool, error) {
		*cur = state.HomeState{ResourcePressure: &marker}
		return true, nil
	}); err != nil {
		t.Fatalf("seed home state: %v", err)
	}
}

// TestOfferMemoryRaiseAfterAttachUsesMarker pins that the post-attach path reads
// the project's pressure marker and passes it to the detector: a recorded
// unanswered OOM with no log line is offered from the marker.
func TestOfferMemoryRaiseAfterAttachUsesMarker(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "runpressure", Agent: "opencode"}
	seedPressureMarker(t, k, state.ResourcePressureState{
		Kind: "memory", Evidence: string(pressure.EvidenceOOMLog), Victim: "opencode",
		OOMSeenAt: time.Now().Add(-time.Hour),
	})
	sb := &msb.MockSandbox{
		MetricsResult: &msbSdk.Metrics{MemoryBytes: 1 << 20, MemoryLimitBytes: 4096 << 20},
	}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}

	offerMemoryRaiseAfterAttach(context.Background(), k, sb, pressureSummary{}, options.RunOptions{}, ui)

	if selects != 1 {
		t.Fatalf("Select called %d times, want 1 for the marker-recorded unanswered OOM", selects)
	}
}

// TestOfferMemoryRaiseAfterAttachUsesMetricsLimit pins the current/boot-max
// arguments: with neither supplied, the detector falls back to the metrics limit
// for the suggestion.
func TestOfferMemoryRaiseAfterAttachUsesMetricsLimit(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "runpressurelimit", Agent: "opencode"}
	seedPressureMarker(t, k, state.ResourcePressureState{})
	sb := &msb.MockSandbox{
		MetricsResult: &msbSdk.Metrics{MemoryBytes: 7800 << 20, MemoryLimitBytes: 8192 << 20},
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

	offerMemoryRaiseAfterAttach(context.Background(), k, sb, pressureSummary{Tripped: true}, options.RunOptions{}, ui)

	if inputDefault != "10G" {
		t.Errorf("Input default = %q, want 10G from the metrics limit", inputDefault)
	}
}

// TestAbnormalEnd pins the abnormal-end classification: a clean exit and an
// ordinary non-zero exit are normal, while a signal death (-1) or an attach
// error is abnormal (spec: -1 is the guest's signal death; a non-zero exit
// code is a deliberate agent exit, not a crash).
func TestAbnormalEnd(t *testing.T) {
	cases := []struct {
		name      string
		attachErr error
		exitCode  int
		want      bool
	}{
		{"clean exit", nil, 0, false},
		{"non-zero exit code", nil, 3, false},
		{"signal death", nil, -1, true},
		{"attach error", errors.New("boom"), 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := abnormalEnd(tc.attachErr, tc.exitCode); got != tc.want {
				t.Errorf("abnormalEnd(%v, %d) = %v, want %v", tc.attachErr, tc.exitCode, got, tc.want)
			}
		})
	}
}

// TestOfferMemoryRaiseAfterAttachDeclinedSaysNothingAboutTheNextRun pins that
// a declined offer persists nothing, so no "next run" notice follows.
func TestOfferMemoryRaiseAfterAttachDeclinedSaysNothingAboutTheNextRun(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "declinedoffer", Agent: "opencode"}
	seedSessionHomeState(t, k)
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "keep", nil },
	}
	sb := &msb.MockSandbox{
		LogsResult:    []msbSdk.LogEntry{sessionOOMLogLine(time.Now())},
		MetricsResult: &msbSdk.Metrics{MemoryBytes: 1024 << 20, MemoryLimitBytes: 4096 << 20},
	}

	offerMemoryRaiseAfterAttach(context.Background(), k, sb, pressureSummary{}, options.RunOptions{}, ui)

	if len(ui.InfoCalls) != 0 {
		t.Errorf("info messages %q after a declined offer, want none", ui.InfoCalls)
	}
}

// TestOfferMemoryRaiseAfterAttachSaysTheRaiseAppliesNextRun pins that a raise
// accepted after a session tells the user when it applies: the VM keeps its
// limit until the next run.
func TestOfferMemoryRaiseAfterAttachSaysTheRaiseAppliesNextRun(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_MEMORY", "")
	t.Setenv("OPENCODE_SANDBOX_MEMORY", "")
	k := state.Key{Slug: "nextrunoffer", Agent: "opencode"}
	seedSessionHomeState(t, k)
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "raise", nil },
		InputFn:             func(_, defaultValue string) (string, error) { return defaultValue, nil },
	}
	sb := &msb.MockSandbox{
		LogsResult:    []msbSdk.LogEntry{sessionOOMLogLine(time.Now())},
		MetricsResult: &msbSdk.Metrics{MemoryBytes: 1024 << 20, MemoryLimitBytes: 4096 << 20},
	}

	offerMemoryRaiseAfterAttach(context.Background(), k, sb, pressureSummary{}, options.RunOptions{}, ui)

	if !slices.ContainsFunc(ui.InfoCalls, func(message string) bool { return strings.Contains(message, "next run") }) {
		t.Errorf("info messages %q do not say the raise applies on the next run", ui.InfoCalls)
	}
}

// TestOfferMemoryRaiseAfterAttachClampsToTheBootMaximum pins that the
// post-attach offer never suggests more than a VM can boot with: the suggestion
// is clamped to the host maximum a project VM is created with.
func TestOfferMemoryRaiseAfterAttachClampsToTheBootMaximum(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	bootMaxMiB := bootMaxMemoryMiB()
	k := state.Key{Slug: "clampedoffer", Agent: "opencode"}
	seedSessionHomeState(t, k)
	var suggested string
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "raise", nil },
		InputFn: func(_, defaultValue string) (string, error) {
			suggested = defaultValue
			return defaultValue, nil
		},
	}
	nearMax := uint64(bootMaxMiB-512) << 20
	sb := &msb.MockSandbox{MetricsResult: &msbSdk.Metrics{MemoryBytes: nearMax, MemoryLimitBytes: nearMax}}

	offerMemoryRaiseAfterAttach(
		context.Background(), k, sb, pressureSummary{Tripped: true}, options.RunOptions{}, ui,
	)

	if want := fmt.Sprintf("%dG", bootMaxMiB/1024); suggested != want {
		t.Errorf("suggested %q, want the boot maximum %s", suggested, want)
	}
}

// TestOfferMemoryRaiseAfterAttachWitnessedTrip pins the wiring from the watcher
// summary into the shared core: a tripped summary offers a headroom raise even
// with no OOM line and recovered fresh metrics, while an untripped one is silent.
func TestOfferMemoryRaiseAfterAttachWitnessedTrip(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	recovered := &msbSdk.Metrics{MemoryBytes: 5000 << 20, MemoryLimitBytes: 8192 << 20}

	t.Run("tripped summary offers", func(t *testing.T) {
		k := state.Key{Slug: "witnessedtrip", Agent: "opencode"}
		seedSessionHomeState(t, k)
		var selects int
		ui := &termio.Mock{
			IsInteractiveResult: true,
			SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
		}
		sb := &msb.MockSandbox{MetricsResult: recovered}

		offerMemoryRaiseAfterAttach(
			context.Background(), k, sb, pressureSummary{Tripped: true}, options.RunOptions{}, ui,
		)

		if selects != 1 {
			t.Fatalf("Select called %d times, want 1 for a witnessed trip", selects)
		}
	})

	t.Run("untripped summary is silent", func(t *testing.T) {
		k := state.Key{Slug: "untrippedsummary", Agent: "opencode"}
		seedSessionHomeState(t, k)
		var selects int
		ui := &termio.Mock{
			IsInteractiveResult: true,
			SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
		}
		sb := &msb.MockSandbox{MetricsResult: recovered}

		offerMemoryRaiseAfterAttach(
			context.Background(), k, sb, pressureSummary{Tripped: false}, options.RunOptions{}, ui,
		)

		if selects != 0 {
			t.Fatalf("Select called %d times, want 0 without a trip or OOM line", selects)
		}
	})
}
