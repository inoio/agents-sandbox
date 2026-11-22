package session

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/hostfigures"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/prepare"
	"github.com/inoio/agents-sandbox/internal/sandbox/pressure"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// The package-var seams captured at init, before main_test.go pins them, so a
// test can assert their production wiring.
var (
	defaultBootMaxMemoryMiB       = bootMaxMemoryMiB
	defaultHostAvailableMemoryMiB = hostAvailableMemoryMiB
)

// TestDefaultSeamsReadTheHostFigures pins that the two package-var seams default
// to the hostfigures reads, not a raw host read: overriding the seam moves the
// default's result. Without this a silent swap back to sysinfo would leave the
// suite hitting the real host with no failing test.
func TestDefaultSeamsReadTheHostFigures(t *testing.T) {
	restoreMemory := hostfigures.OverrideTotalMemoryGiBForTest(func() int { return 16 })
	restoreAvailable := hostfigures.OverrideAvailableMemoryMiBForTest(func() (uint32, bool) { return 12345, true })
	t.Cleanup(func() {
		restoreMemory()
		restoreAvailable()
	})

	if got, want := defaultBootMaxMemoryMiB(), options.ParseMemoryGiB(16); got != want {
		t.Errorf("default bootMaxMemoryMiB = %d, want the seam's %d", got, want)
	}
	if got, ok := defaultHostAvailableMemoryMiB(); got != 12345 || !ok {
		t.Errorf("default hostAvailableMemoryMiB = (%d, %v), want the seam's (12345, true)", got, ok)
	}
}

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
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "runpressure", Agent: "opencode"}
	seedPressureMarker(t, k, state.ResourcePressureState{
		Kind: "memory", Evidence: string(pressure.EvidenceOOMLog), Victim: "opencode",
		OOMSeenAt: time.Now().Add(-time.Hour),
	})
	sb := &msbtest.MockSandbox{
		MetricsResult: &msb.Metrics{MemoryBytes: 1 << 20, MemoryLimitBytes: 4096 << 20},
	}
	var selects int
	ui := &termiotest.Mock{
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
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "runpressurelimit", Agent: "opencode"}
	seedPressureMarker(t, k, state.ResourcePressureState{})
	sb := &msbtest.MockSandbox{
		MetricsResult: &msb.Metrics{MemoryBytes: 7800 << 20, MemoryLimitBytes: 8192 << 20},
	}
	var inputDefault string
	ui := &termiotest.Mock{
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

// TestOfferMemoryRaiseAfterAttachDeclinedSaysNothingAboutTheNextRun pins that
// a declined offer persists nothing, so no "next run" notice follows.
func TestOfferMemoryRaiseAfterAttachDeclinedSaysNothingAboutTheNextRun(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "declinedoffer", Agent: "opencode"}
	seedSessionHomeState(t, k)
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "keep", nil },
	}
	sb := &msbtest.MockSandbox{
		LogsResult:    []msb.LogEntry{sessionOOMLogLine(time.Now())},
		MetricsResult: &msb.Metrics{MemoryBytes: 1024 << 20, MemoryLimitBytes: 4096 << 20},
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
	configpathstest.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_MEMORY", "")
	t.Setenv("OPENCODE_SANDBOX_MEMORY", "")
	k := state.Key{Slug: "nextrunoffer", Agent: "opencode"}
	seedSessionHomeState(t, k)
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "raise", nil },
		InputFn:             func(_, defaultValue string) (string, error) { return defaultValue, nil },
	}
	sb := &msbtest.MockSandbox{
		LogsResult:    []msb.LogEntry{sessionOOMLogLine(time.Now())},
		MetricsResult: &msb.Metrics{MemoryBytes: 1024 << 20, MemoryLimitBytes: 4096 << 20},
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
	configpathstest.WithMockConfigPaths(t)
	bootMaxMiB := bootMaxMemoryMiB()
	k := state.Key{Slug: "clampedoffer", Agent: "opencode"}
	seedSessionHomeState(t, k)
	var suggested string
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "raise", nil },
		InputFn: func(_, defaultValue string) (string, error) {
			suggested = defaultValue
			return defaultValue, nil
		},
	}
	nearMax := uint64(bootMaxMiB-512) << 20
	sb := &msbtest.MockSandbox{MetricsResult: &msb.Metrics{MemoryBytes: nearMax, MemoryLimitBytes: nearMax}}

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
	configpathstest.WithMockConfigPaths(t)
	recovered := &msb.Metrics{MemoryBytes: 5000 << 20, MemoryLimitBytes: 8192 << 20}

	t.Run("tripped summary offers", func(t *testing.T) {
		k := state.Key{Slug: "witnessedtrip", Agent: "opencode"}
		seedSessionHomeState(t, k)
		var selects int
		ui := &termiotest.Mock{
			IsInteractiveResult: true,
			SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
		}
		sb := &msbtest.MockSandbox{MetricsResult: recovered}

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
		ui := &termiotest.Mock{
			IsInteractiveResult: true,
			SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
		}
		sb := &msbtest.MockSandbox{MetricsResult: recovered}

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

// TestOfferMemoryRaiseAfterAttachUsesConfiguredMemoryWhenMetricsFail pins the
// post-attach fallback on a runtime that reports no live metrics: the offer is
// still made, with the run's configured memory as its basis, instead of
// deferring to the next launch.
func TestOfferMemoryRaiseAfterAttachUsesConfiguredMemoryWhenMetricsFail(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_MEMORY", "")
	t.Setenv("OPENCODE_SANDBOX_MEMORY", "")
	k := state.Key{Slug: "fallbacklimit", Agent: "opencode"}
	seedSessionHomeState(t, k)
	var inputDefault string
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "raise", nil },
		InputFn: func(_ string, defaultValue string) (string, error) {
			inputDefault = defaultValue
			return defaultValue, nil
		},
	}
	sb := &msbtest.MockSandbox{
		LogsResult: []msb.LogEntry{sessionOOMLogLine(time.Now())},
		MetricsErr: errors.New("sandbox 199 has no verified live metrics slot"),
	}

	offerMemoryRaiseAfterAttach(
		context.Background(), k, sb, pressureSummary{}, options.RunOptions{Memory: "4G"}, ui,
	)

	if inputDefault != "5G" {
		t.Errorf("Input default = %q, want 5G from the run's configured memory", inputDefault)
	}
}

// TestRunOffersFromTheAppliedMemoryLimit pins that Run hands the post-attach
// offer the memory the run applied rather than the CLI-resolved value: a raise
// accepted in prepare for this same run is the offer's basis, so the offer
// cannot persist a value below the live limit (the stale-value regression).
func TestRunOffersFromTheAppliedMemoryLimit(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_MEMORY", "")
	t.Setenv("OPENCODE_SANDBOX_MEMORY", "")
	k := state.Key{Slug: "appliedmemory", Agent: "opencode"}
	seedSessionHomeState(t, k)
	var inputDefault string
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "raise", nil },
		InputFn: func(_ string, defaultValue string) (string, error) {
			inputDefault = defaultValue
			return defaultValue, nil
		},
	}
	sb := &msbtest.MockSandbox{
		AttachCode: -1,
		Name_:      "test-vm",
		LogsResult: []msb.LogEntry{sessionOOMLogLine(time.Now())},
		MetricsErr: errors.New("sandbox 199 has no verified live metrics slot"),
	}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: sb, target: "/workspace", key: k, limits: prepare.AppliedLimits{Memory: "12G"}}, nil
	}
	defer func() { prepareSandbox = orig }()

	// The attach ends abnormally (signal death), so Run returns an ExitError;
	// the offer's basis is what this test asserts.
	_ = Run(context.Background(), resolvedRunOptions(t, options.RunOptions{
		Agent: "opencode", Memory: "4G", ReapPolicy: options.NewReapPolicy(true, 5),
	}), options.ModeRun, ui)

	if inputDefault != "15G" {
		t.Errorf("Input default = %q, want 15G from the session's applied memory", inputDefault)
	}
}

// TestApplyAppliedLimitsReplacesEveryLimit pins that the run's configured limits
// are replaced by the applied ones, not merged, so a same-run raise reaches both
// the memory offer and the disk offer's bases; the other options are kept.
func TestApplyAppliedLimitsReplacesEveryLimit(t *testing.T) {
	ses := &fakePrepared{limits: prepare.AppliedLimits{
		Memory: "12G", DiskSize: "4G", WorkspaceQuota: "16G", TmpSize: "2G",
	}}
	got := applyAppliedLimits(ses, options.RunOptions{
		Agent: "opencode", Memory: "4G", DiskSize: "2G", WorkspaceQuota: "8G", TmpSize: "1G",
	})

	if got.Memory != "12G" || got.DiskSize != "4G" || got.WorkspaceQuota != "16G" || got.TmpSize != "2G" {
		t.Errorf("applyAppliedLimits = %+v, want the applied limits", got)
	}
	if got.Agent != "opencode" {
		t.Errorf("applyAppliedLimits dropped the other options: %+v", got)
	}
}

// TestRunAttachTracesWhyNoMemoryOffer pins the diagnostic: a clean exit skips
// the in-session memory offer, and the reason is reported at verbose level so
// an operator can tell a skipped offer from detection that never ran.
func TestRunAttachTracesWhyNoMemoryOffer(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "traceoffer", Agent: "opencode"}
	seedSessionHomeState(t, k)
	ui := &termiotest.Mock{}
	sb := &msbtest.MockSandbox{AttachCode: 0}

	_ = runAttach(context.Background(), k, sb, "/workspace", ui,
		resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(false, 5)}), "-l")

	traced := false
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, "no in-session memory-raise offer") {
			traced = true
		}
	}
	if !traced {
		t.Errorf("verbose calls %v do not trace the skipped offer", ui.VerboseCalls)
	}
}

// TestOfferDiskRaiseAfterAttachOffersAFullTmp pins the post-attach disk probe:
// a /tmp short of space is offered with the run's /tmp size and saved, and the
// user learns it applies on the next run.
func TestOfferDiskRaiseAfterAttachOffersAFullTmp(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	for _, key := range []string{"AGENTS_SANDBOX_TMP_SIZE", "OPENCODE_SANDBOX_TMP_SIZE"} {
		t.Setenv(key, "")
	}
	k := state.Key{Slug: "diskoffer", Agent: "opencode"}
	seedSessionHomeState(t, k)
	var suggested string
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "raise", nil },
		InputFn: func(_, defaultValue string) (string, error) {
			suggested = defaultValue
			return defaultValue, nil
		},
	}
	sb := &msbtest.MockSandbox{
		ExecOut: map[string]msb.ShellResult{diskProbeKey(): msbtest.NewTestResult(true, 0, dfFullTmp, "", nil)},
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
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "diskprobefail", Agent: "opencode"}
	seedSessionHomeState(t, k)
	ui := &termiotest.Mock{IsInteractiveResult: true}
	sb := &msbtest.MockSandbox{ExecErr: errors.New("exec refused")}

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
	*msbtest.MockSandbox

	deadline    time.Time
	hasDeadline bool
}

func (s *deadlineRecordingSandbox) Exec(
	ctx context.Context,
	command string,
	args []string,
	req msb.ExecRequest,
) (msb.ShellResult, error) {
	s.deadline, s.hasDeadline = ctx.Deadline()
	return s.MockSandbox.Exec(ctx, command, args, req)
}

// TestOfferDiskRaiseAfterAttachBoundsTheProbe pins that the in-guest probe runs
// under its own timeout, so a wedged guest cannot hang the end of a session.
func TestOfferDiskRaiseAfterAttachBoundsTheProbe(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "diskprobetimeout", Agent: "opencode"}
	seedSessionHomeState(t, k)
	sb := &deadlineRecordingSandbox{MockSandbox: &msbtest.MockSandbox{ExecErr: errors.New("exec refused")}}
	started := time.Now()

	offerDiskRaiseAfterAttach(context.Background(), k, sb, options.RunOptions{}, &termiotest.Mock{})

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
	probe := func() *msbtest.MockSandbox {
		return &msbtest.MockSandbox{
			ExecOut: map[string]msb.ShellResult{diskProbeKey(): msbtest.NewTestResult(true, 0, dfFullTmp, "", nil)},
		}
	}
	t.Run("answered hit", func(t *testing.T) {
		configpathstest.WithMockConfigPaths(t)
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
		ui := &termiotest.Mock{
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
		configpathstest.WithMockConfigPaths(t)
		k := state.Key{Slug: "diskdeclined", Agent: "opencode"}
		seedSessionHomeState(t, k)
		ui := &termiotest.Mock{
			IsInteractiveResult: true,
			SelectFn:            func(string, []termio.Choice, string) (string, error) { return "keep", nil },
		}
		offerDiskRaiseAfterAttach(context.Background(), k, probe(), options.RunOptions{}, ui)
		if len(ui.InfoCalls) != 0 {
			t.Errorf("info messages %q after a decline, want none", ui.InfoCalls)
		}
	})
	t.Run("missing state", func(t *testing.T) {
		configpathstest.WithMockConfigPaths(t)
		ui := &termiotest.Mock{}
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
	configpathstest.WithMockConfigPaths(t)
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
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		SelectFn: func(string, []termio.Choice, string) (string, error) {
			prompted = true
			return "keep", nil
		},
	}
	sb := &msbtest.MockSandbox{
		ExecOut: map[string]msb.ShellResult{diskProbeKey(): msbtest.NewTestResult(true, 0, dfBandTmp, "", nil)},
	}

	offerDiskRaiseAfterAttach(context.Background(), k, sb, options.RunOptions{TmpSize: "2G", Memory: "4G"}, ui)

	if !prompted {
		t.Error("a recorded unanswered /tmp hit in the band was not offered")
	}
}
