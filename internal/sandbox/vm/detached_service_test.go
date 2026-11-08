package vm

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

func TestEnsureDetachedServiceLeavesHealthyServiceRunning(t *testing.T) {
	ui := termiotest.NewTestMock(t)
	calls := &[]msb.MockDetachedCall{}
	sb := &msb.MockSandbox{ExecDetachedCalls: calls}
	service := testDetachedService()
	service.launchArgs = func(context.Context) ([]string, string, error) {
		t.Fatal("a healthy service must not be re-launched")
		return nil, "", nil
	}

	if err := ensureDetachedService(context.Background(), sb, &ui, service); err != nil {
		t.Fatalf("ensureDetachedService: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("detached calls = %+v, want none", *calls)
	}
	if !slices.Contains(ui.VerboseCalls, "already healthy") {
		t.Errorf("verbose calls = %v, want the already-healthy note", ui.VerboseCalls)
	}
}

func TestEnsureDetachedServiceKillsStaleThenLaunchesAndAwaits(t *testing.T) {
	ui := termiotest.NewTestMock(t)
	calls := &[]msb.MockDetachedCall{}
	var sequence []string
	stale := &msb.MockJob{}
	sb := &msb.MockSandbox{
		ExecDetachedCalls: calls,
		ExecDetachedFn: func(context.Context, string, []string, msb.JobSpec) (msb.Job, error) {
			sequence = append(sequence, "launch")
			return &msb.MockJob{}, nil
		},
		Jobs: []msb.JobInfo{
			{ID: "stale", Command: []string{"env", "AGENTS_SANDBOX_DETACHED_JOB=dockerd", "dockerd"}},
		},
		GetJobFn: func(context.Context, string) (msb.Job, error) {
			return &orderRecordingJob{MockJob: stale, onKill: func() { sequence = append(sequence, "kill") }}, nil
		},
	}
	service := testDetachedService()
	service.probe = scriptedProbe(
		detachedProbeOutcome{healthy: false, detail: "not yet"},
		detachedProbeOutcome{healthy: false, detail: "still starting"},
		detachedProbeOutcome{healthy: true},
	)
	service.preCheckUnhealthyNote = "pre-check: %s"
	service.cleanup = func(context.Context) error {
		sequence = append(sequence, "cleanup")
		return nil
	}
	var unhealthy []string
	service.onUnhealthy = func(detail string) { unhealthy = append(unhealthy, detail) }

	if err := ensureDetachedService(context.Background(), sb, &ui, service); err != nil {
		t.Fatalf("ensureDetachedService: %v", err)
	}
	// The stale kill and the residue cleanup must precede the fresh launch, or
	// the stale process keeps the socket/port the launch needs (#343).
	if want := []string{"kill", "cleanup", "launch"}; !slices.Equal(sequence, want) {
		t.Errorf("restart sequence = %v, want %v", sequence, want)
	}
	if !stale.DidKill {
		t.Error("the stale service job was not killed before the launch")
	}
	assertDetachedHelperLaunch(t, *calls, "dockerd", dockerdBinary)
	if !slices.Contains(ui.VerboseCalls, "pre-check: not yet") {
		t.Errorf("verbose calls = %v, want the pre-check detail", ui.VerboseCalls)
	}
	if !slices.Contains(ui.VerboseCalls, "starting dockerd") {
		t.Errorf("verbose calls = %v, want the starting note", ui.VerboseCalls)
	}
	if !slices.Contains(unhealthy, "still starting") {
		t.Errorf("onUnhealthy = %v, want the readiness poll detail", unhealthy)
	}
	if !slices.Contains(ui.VerboseCalls, "ready") {
		t.Errorf("verbose calls = %v, want the ready note", ui.VerboseCalls)
	}
}

// TestEnsureDetachedServiceSkipPreCheckForcesLaunch pins the daemon's force
// path: with the pre-check skipped, an already-healthy probe must not stop the
// restart.
func TestEnsureDetachedServiceSkipPreCheckForcesLaunch(t *testing.T) {
	ui := termiotest.NewTestMock(t)
	calls := &[]msb.MockDetachedCall{}
	sb := &msb.MockSandbox{ExecDetachedCalls: calls}
	service := testDetachedService()
	service.skipPreCheck = true

	if err := ensureDetachedService(context.Background(), sb, &ui, service); err != nil {
		t.Fatalf("ensureDetachedService: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("detached calls = %+v, want one forced launch", *calls)
	}
}

func TestEnsureDetachedServiceCleanupFailureIsNonFatal(t *testing.T) {
	ui := termiotest.NewTestMock(t)
	sb := &msb.MockSandbox{}
	service := testDetachedService()
	service.probe = scriptedProbe(
		detachedProbeOutcome{healthy: false},
		detachedProbeOutcome{healthy: true},
	)
	service.cleanup = func(context.Context) error { return errors.New("residue boom") }

	if err := ensureDetachedService(context.Background(), sb, &ui, service); err != nil {
		t.Fatalf("a cleanup failure must be non-fatal, got: %v", err)
	}
	if !strings.Contains(strings.Join(ui.VerboseCalls, "\n"), "cleanup failed: residue boom") {
		t.Errorf("verbose calls = %v, want the cleanup failure", ui.VerboseCalls)
	}
}

// TestEnsureDetachedServiceLaunchArgsErrorAborts pins the ordering: a failure to
// prepare the launch aborts before the stale service is killed or a fresh one is
// launched.
func TestEnsureDetachedServiceLaunchArgsErrorAborts(t *testing.T) {
	ui := termiotest.NewTestMock(t)
	wantErr := errors.New("args boom")
	calls := &[]msb.MockDetachedCall{}
	stale := &msb.MockJob{}
	sb := &msb.MockSandbox{
		ExecDetachedCalls: calls,
		Jobs:              []msb.JobInfo{{ID: "stale", Command: []string{"dockerd"}}},
		GetJobFn:          func(context.Context, string) (msb.Job, error) { return stale, nil },
	}
	service := testDetachedService()
	service.probe = scriptedProbe(detachedProbeOutcome{healthy: false})
	service.launchArgs = func(context.Context) ([]string, string, error) { return nil, "", wantErr }

	err := ensureDetachedService(context.Background(), sb, &ui, service)
	if !errors.Is(err, wantErr) {
		t.Fatalf("ensureDetachedService() = %v, want %v", err, wantErr)
	}
	if stale.DidKill {
		t.Error("a launch-preparation failure must abort before the kill")
	}
	if len(*calls) != 0 {
		t.Errorf("detached calls = %+v, want none", *calls)
	}
}

func TestEnsureDetachedServiceWrapsLaunchError(t *testing.T) {
	ui := termiotest.NewTestMock(t)
	wantErr := errors.New("connection lost")
	sb := &msb.MockSandbox{
		ExecDetachedFn: func(
			context.Context, string, []string, msb.JobSpec,
		) (msb.Job, error) {
			return nil, wantErr
		},
	}
	service := testDetachedService()
	service.probe = scriptedProbe(detachedProbeOutcome{healthy: false})

	err := ensureDetachedService(context.Background(), sb, &ui, service)
	if !errors.Is(err, wantErr) {
		t.Fatalf("ensureDetachedService() = %v, want it to wrap %v", err, wantErr)
	}
	if !strings.Contains(err.Error(), "start dockerd") {
		t.Errorf("error = %v, want it to name the service", err)
	}
}

func TestEnsureDetachedServiceReportsCloseFailure(t *testing.T) {
	ui := termiotest.NewTestMock(t)
	sb := &msb.MockSandbox{
		Job_: &msb.MockJob{CloseErr: errors.New("close boom")},
	}
	service := testDetachedService()
	service.probe = scriptedProbe(detachedProbeOutcome{healthy: false})

	if err := ensureDetachedService(context.Background(), sb, &ui, service); err != nil {
		t.Fatalf("ensureDetachedService: %v", err)
	}
	if !strings.Contains(strings.Join(ui.VerboseCalls, "\n"), "closing job handle failed: close boom") {
		t.Errorf("verbose calls = %v, want the close failure", ui.VerboseCalls)
	}
}

// TestEnsureDetachedServiceSkipsEmptyPreCheckDetail pins that a service without
// an unhealthy-note template does not leak the probe's detail (dockerd's
// pre-check is silent about why it is not up).
func TestEnsureDetachedServiceSkipsEmptyPreCheckDetail(t *testing.T) {
	ui := termiotest.NewTestMock(t)
	sb := &msb.MockSandbox{}
	service := testDetachedService()
	service.probe = scriptedProbe(
		detachedProbeOutcome{healthy: false, detail: "secret why"},
		detachedProbeOutcome{healthy: true},
	)

	if err := ensureDetachedService(context.Background(), sb, &ui, service); err != nil {
		t.Fatalf("ensureDetachedService: %v", err)
	}
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, "secret why") {
			t.Errorf("pre-check detail leaked without a note template: %v", ui.VerboseCalls)
		}
	}
}

// TestEnsureDetachedServiceSkipsBlankPreCheckDetail pins that an unhealthy
// pre-check that reports no diagnostic detail does not log a blank note line
// (the note template is rendered only when there is a detail to render).
func TestEnsureDetachedServiceSkipsBlankPreCheckDetail(t *testing.T) {
	ui := termiotest.NewTestMock(t)
	sb := &msb.MockSandbox{}
	service := testDetachedService()
	service.preCheckUnhealthyNote = "pre-check: %s"
	service.probe = scriptedProbe(
		detachedProbeOutcome{healthy: false, detail: ""},
		detachedProbeOutcome{healthy: true},
	)

	if err := ensureDetachedService(context.Background(), sb, &ui, service); err != nil {
		t.Fatalf("ensureDetachedService: %v", err)
	}
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, "pre-check:") {
			t.Errorf("blank pre-check note was logged: %v", ui.VerboseCalls)
		}
	}
}

// detachedProbeOutcome is one scripted health-probe result.
type detachedProbeOutcome struct {
	healthy bool
	detail  string
}

// scriptedProbe returns a health probe that reports each outcome in order and is
// healthy once the script is exhausted, so a test can drive the pre-check and
// the readiness polls independently.
func scriptedProbe(outcomes ...detachedProbeOutcome) func(context.Context) (bool, string) {
	index := 0
	return func(context.Context) (bool, string) {
		if index >= len(outcomes) {
			return true, ""
		}
		outcome := outcomes[index]
		index++
		return outcome.healthy, outcome.detail
	}
}

// testDetachedService is the ingredient table the ensureDetachedService tests
// vary one field at a time from: a dockerd-shaped service whose pre-check is
// healthy, launching under the dockerd helper marker.
func testDetachedService() detachedService {
	return detachedService{
		helper:     dockerdHelper(),
		probe:      func(context.Context) (bool, string) { return true, "" },
		label:      "dockerd",
		startLabel: "dockerd",
		launchArgs: func(context.Context) ([]string, string, error) {
			return []string{dockerdSocketFlag, dockerdSocket}, "starting dockerd", nil
		},
		timeout:            time.Second,
		pollInterval:       time.Millisecond,
		alreadyHealthyNote: "already healthy",
		readyNote:          "ready",
		closeNote:          "closing job handle failed: %v",
		cleanupNote:        "cleanup failed: %v",
	}
}
