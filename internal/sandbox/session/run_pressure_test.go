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
// error is abnormal (spec §1b: -1 is the guest's signal death; a non-zero exit
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

// dfFullTmp is the probe's df output with /tmp short of space.
const dfFullTmp = "Filesystem 1024-blocks Used Available Capacity Mounted on\n" +
	"overlay 4194304 1048576 3145728 25% /\n" +
	"workspace_x 16777216 1048576 15728640 7% /workspace\n" +
	"tmpfs 2097152 1996800 100352 96% /tmp\n"

// diskProbeKey is the MockSandbox exec key of the disk probe.
func diskProbeKey() string {
	command, args := pressure.DiskProbeCommand()
	return command + " " + strings.Join(args, " ")
}

// TestOfferDiskRaiseAfterAttachOffersAFullTmp pins the post-attach disk probe:
// a /tmp short of space is offered with the run's /tmp size and saved, and the
// user learns it applies on the next run.
func TestOfferDiskRaiseAfterAttachOffersAFullTmp(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	for _, key := range []string{"AGENTS_SANDBOX_TMP_SIZE", "OPENCODE_SANDBOX_TMP_SIZE"} {
		t.Setenv(key, "")
	}
	k := state.Key{Slug: "diskoffer", Agent: "opencode"}
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
	sb := &msb.MockSandbox{
		ExecOut: map[string]msb.ShellResult{diskProbeKey(): msb.NewTestResult(true, 0, dfFullTmp, "", nil)},
	}

	offerDiskRaiseAfterAttach(context.Background(), k, sb, options.RunOptions{TmpSize: "2G", Memory: "4G"}, ui)

	if suggested != "3G" {
		t.Errorf("suggested %q, want 3G from the run's 2G /tmp", suggested)
	}
	if !slices.ContainsFunc(ui.InfoCalls, func(message string) bool { return strings.Contains(message, "next run") }) {
		t.Errorf("info messages %q, want the next-run notice", ui.InfoCalls)
	}
}

// TestOfferDiskRaiseAfterAttachProbeFailureIsSilent pins that a failed probe
// offers nothing and is logged only at verbose.
func TestOfferDiskRaiseAfterAttachProbeFailureIsSilent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "diskprobefail", Agent: "opencode"}
	seedSessionHomeState(t, k)
	ui := &termio.Mock{IsInteractiveResult: true}
	sb := &msb.MockSandbox{ExecErr: errors.New("exec refused")}

	offerDiskRaiseAfterAttach(context.Background(), k, sb, options.RunOptions{}, ui)

	if len(ui.InfoCalls) != 0 || len(ui.WarnCalls) != 0 {
		t.Errorf("info %q warn %q, want nothing on a failed probe", ui.InfoCalls, ui.WarnCalls)
	}
	if !slices.ContainsFunc(
		ui.VerboseCalls,
		func(message string) bool { return strings.Contains(message, "disk probe failed") },
	) {
		t.Errorf("verbose calls %q, want the probe failure", ui.VerboseCalls)
	}
}

// deadlineRecordingSandbox records the deadline of the context its Exec runs
// under, so a test can check the probe is bounded.
type deadlineRecordingSandbox struct {
	*msb.MockSandbox

	deadline    time.Time
	hasDeadline bool
}

func (s *deadlineRecordingSandbox) Exec(
	ctx context.Context,
	command string,
	args []string,
	options ...msbSdk.ExecOption,
) (msb.ShellResult, error) {
	s.deadline, s.hasDeadline = ctx.Deadline()
	return s.MockSandbox.Exec(ctx, command, args, options...)
}

// TestOfferDiskRaiseAfterAttachBoundsTheProbe pins that the in-guest probe runs
// under its own timeout, so a wedged guest cannot hang the end of a session.
func TestOfferDiskRaiseAfterAttachBoundsTheProbe(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "diskprobetimeout", Agent: "opencode"}
	seedSessionHomeState(t, k)
	sb := &deadlineRecordingSandbox{MockSandbox: &msb.MockSandbox{ExecErr: errors.New("exec refused")}}
	started := time.Now()

	offerDiskRaiseAfterAttach(context.Background(), k, sb, options.RunOptions{}, &termio.Mock{})

	const wantTimeout = 10 * time.Second
	if !sb.hasDeadline || sb.deadline.Before(started.Add(wantTimeout)) ||
		sb.deadline.After(started.Add(wantTimeout+time.Second)) {
		t.Errorf("probe deadline = %v (set %v), want %v after the call", sb.deadline, sb.hasDeadline, wantTimeout)
	}
}

// TestOfferDiskRaiseAfterAttachHonoursTheRecordedState pins that the probe's
// offer reads the project's disk markers: an answered /tmp hit is not offered
// again, a declined offer says nothing about the next run, and a missing state
// file is logged at verbose.
func TestOfferDiskRaiseAfterAttachHonoursTheRecordedState(t *testing.T) {
	probe := func() *msb.MockSandbox {
		return &msb.MockSandbox{
			ExecOut: map[string]msb.ShellResult{diskProbeKey(): msb.NewTestResult(true, 0, dfFullTmp, "", nil)},
		}
	}
	t.Run("answered hit", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		k := state.Key{Slug: "diskanswered", Agent: "opencode"}
		seen := time.Now().Add(-time.Hour)
		if err := state.RecoverHomeState(k, func(hs *state.HomeState, _ bool) (bool, error) {
			hs.HomeVolume = "vol"
			hs.DiskPressure = map[string]state.DiskPressureState{"tmp": {SeenAt: seen, OfferedAt: seen}}
			return true, nil
		}); err != nil {
			t.Fatalf("seed state: %v", err)
		}
		prompted := false
		ui := &termio.Mock{
			IsInteractiveResult: true,
			SelectFn: func(string, []termio.Choice, string) (string, error) {
				prompted = true
				return "keep", nil
			},
		}
		offerDiskRaiseAfterAttach(context.Background(), k, probe(), options.RunOptions{}, ui)
		if prompted {
			t.Error("an answered /tmp hit was offered again")
		}
	})
	t.Run("declined", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		k := state.Key{Slug: "diskdeclined", Agent: "opencode"}
		seedSessionHomeState(t, k)
		ui := &termio.Mock{
			IsInteractiveResult: true,
			SelectFn:            func(string, []termio.Choice, string) (string, error) { return "keep", nil },
		}
		offerDiskRaiseAfterAttach(context.Background(), k, probe(), options.RunOptions{}, ui)
		if len(ui.InfoCalls) != 0 {
			t.Errorf("info messages %q after a decline, want none", ui.InfoCalls)
		}
	})
	t.Run("missing state", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		ui := &termio.Mock{}
		offerDiskRaiseAfterAttach(
			context.Background(),
			state.Key{Slug: "disknostate", Agent: "opencode"},
			probe(),
			options.RunOptions{},
			ui,
		)
		if !slices.ContainsFunc(
			ui.VerboseCalls,
			func(message string) bool { return strings.Contains(message, "reading home state") },
		) {
			t.Errorf("verbose calls %q, want the state read failure", ui.VerboseCalls)
		}
	})
}

// TestOfferDiskRaiseAfterAttachOffersARecordedHitInTheBand pins that an
// unanswered recorded hit is offered even when the fresh probe no longer trips
// (the mount sits between the trip and recovery levels): the recorded marker,
// not the probe, decides, so the probe's markers must reach the core.
func TestOfferDiskRaiseAfterAttachOffersARecordedHitInTheBand(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	for _, key := range []string{"AGENTS_SANDBOX_TMP_SIZE", "OPENCODE_SANDBOX_TMP_SIZE"} {
		t.Setenv(key, "")
	}
	k := state.Key{Slug: "diskband", Agent: "opencode"}
	seen := time.Now().Add(-time.Hour)
	if err := state.RecoverHomeState(k, func(hs *state.HomeState, _ bool) (bool, error) {
		hs.HomeVolume = "vol"
		hs.DiskPressure = map[string]state.DiskPressureState{
			"tmp": {SeenAt: seen, SizeKiB: 2 << 20, AvailKiB: 98 << 10},
		}
		return true, nil
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	// /tmp free space is between its trip level (15 % of 2 GiB) and twice that:
	// neither tripped nor recovered, so only the recorded marker can offer.
	const dfBandTmp = "Filesystem 1024-blocks Used Available Capacity Mounted on\n" +
		"overlay 4194304 1048576 3145728 25% /\n" +
		"workspace_x 16777216 1048576 15728640 7% /workspace\n" +
		"tmpfs 2097152 1697152 400000 81% /tmp\n"
	prompted := false
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(string, []termio.Choice, string) (string, error) {
			prompted = true
			return "keep", nil
		},
	}
	sb := &msb.MockSandbox{
		ExecOut: map[string]msb.ShellResult{diskProbeKey(): msb.NewTestResult(true, 0, dfBandTmp, "", nil)},
	}

	offerDiskRaiseAfterAttach(context.Background(), k, sb, options.RunOptions{TmpSize: "2G", Memory: "4G"}, ui)

	if !prompted {
		t.Error("a recorded unanswered /tmp hit in the band was not offered")
	}
}
