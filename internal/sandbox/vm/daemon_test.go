package vm

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestDaemonHealthSeamResolution pins the seam contract: a nil
// options.Seams.DaemonHealth uses the production default (daemonHealthFunc),
// while a set field is used as-is.
func TestDaemonHealthSeamResolution(t *testing.T) {
	orig := daemonHealthFunc
	t.Cleanup(func() { daemonHealthFunc = orig })
	daemonHealthFunc = func(context.Context, msb.Sandbox, string) (string, int, error) {
		return "default", 1, nil
	}
	if out, code, _ := daemonHealth(
		options.Seams{},
	)(
		context.Background(),
		nil,
		"probe",
	); out != "default" ||
		code != 1 {
		t.Errorf("nil seam probe = (%q, %d), want the production default (default, 1)", out, code)
	}

	seam := func(context.Context, msb.Sandbox, string) (string, int, error) { return "seam", 2, nil }
	if out, code, _ := daemonHealth(
		options.Seams{DaemonHealth: seam},
	)(
		context.Background(),
		nil,
		"probe",
	); out != "seam" ||
		code != 2 {
		t.Errorf("set seam probe = (%q, %d), want (seam, 2)", out, code)
	}
}

// TestEnsureDaemonContextCancelled covers the branch where the poll loop is
// aborted because the context is cancelled while waiting for the daemon to
// become healthy.
func TestEnsureDaemonContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	provider := opencodeProvider(t)
	healthChecks := 0
	seams := options.Seams{DaemonHealth: func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == provider.DaemonHealthCmd() {
			healthChecks++
			if healthChecks == 3 {
				cancel()
			}
			return "", 1, nil
		}
		return "", 0, nil
	}}

	origPoll := daemonPollInterval
	t.Cleanup(func() { daemonPollInterval = origPoll })
	daemonPollInterval = time.Millisecond

	err := ensureDaemon(ctx, opencodeResolved(t), false, false, &msb.MockSandbox{}, &termio.Mock{}, seams)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestEnsureDaemonNoDaemonProvider(t *testing.T) {
	ui := &termio.Mock{}
	calls := &[]msb.MockDetachedCall{}
	sb := &msb.MockSandbox{ExecDetachedCalls: calls}
	if err := ensureDaemon(
		context.Background(), agent.Resolve(&fakeAgent{}), false, false, sb, ui, options.Seams{},
	); err != nil {
		t.Fatalf("expected nil for an agent without a DaemonProvider, got %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("expected no detach calls for an agent without a DaemonProvider, got %v", *calls)
	}
}

func TestEnsureDaemonStartsWhenUnhealthy(t *testing.T) {
	testUI := termio.NewTestMock(t)
	fastDaemonPoll(t)
	seams := healthQueue(unhealthy(), unhealthy(), healthy())

	calls := &[]msb.MockDetachedCall{}
	sb := &msb.MockSandbox{ExecDetachedCalls: calls}

	if err := ensureDaemon(context.Background(), opencodeResolved(t), false, false, sb, &testUI, seams); err != nil {
		t.Fatalf("EnsureDaemon failed: %v", err)
	}
	assertDetachedHelperLaunch(t, *calls, "agent-daemon", "opencode")
	// The daemon runs as the sandbox default user, so its detached launch
	// carries no exec-user option; a helper that added one would launch it as
	// the wrong user.
	if len((*calls)[0].Opts) != 0 {
		t.Errorf("daemon launch exec options = %d, want 0 (default user)", len((*calls)[0].Opts))
	}
	if !slices.Contains(testUI.VerboseCalls, "opencode daemon is healthy") {
		t.Errorf("expected a verbose healthy note, got %v", testUI.VerboseCalls)
	}
	if !slices.Contains(testUI.VerboseCalls, "starting opencode serve daemon") {
		t.Errorf("expected the starting-daemon verbose note, got %v", testUI.VerboseCalls)
	}
}

// TestEnsureDaemonStartsServeOnlyOnExternalInterface verifies that serve-only
// mode launches the opencode daemon bound to 0.0.0.0. Microsandbox's published
// port forwarder dials the guest's external interface address, so a loopback-only
// (127.0.0.1) binding would make host clients get an empty reply.
func TestEnsureDaemonStartsServeOnlyOnExternalInterface(t *testing.T) {
	testUI := termio.NewTestMock(t)
	fastDaemonPoll(t)
	seams := healthQueue(unhealthy(), healthy())

	calls := &[]msb.MockDetachedCall{}
	sb := &msb.MockSandbox{ExecDetachedCalls: calls}

	if err := ensureDaemon(context.Background(), opencodeResolved(t), true, false, sb, &testUI, seams); err != nil {
		t.Fatalf("EnsureDaemon failed: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("detached calls = %+v, want one", *calls)
	}
	if !strings.Contains(strings.Join((*calls)[0].Args, " "), "--hostname 0.0.0.0") {
		t.Fatalf("serve-only daemon was not started with external-interface binding: %+v", *calls)
	}
	assertDetachedHelperLaunch(t, *calls, "agent-daemon", "opencode")
}

// TestEnsureDaemonProbesBeforeWaiting verifies that after launching the daemon
// the first health probe runs immediately rather than after a full poll
// interval; a long interval must not delay discovering an already-healthy
// daemon.
func TestEnsureDaemonProbesBeforeWaiting(t *testing.T) {
	testUI := termio.NewTestMock(t)
	seams := healthQueue(unhealthy(), healthy())

	origTimeout, origInterval := daemonReadyTimeout, daemonPollInterval
	t.Cleanup(func() {
		daemonReadyTimeout = origTimeout
		daemonPollInterval = origInterval
	})
	daemonReadyTimeout = 2 * time.Second
	daemonPollInterval = 2 * time.Second

	sb := &msb.MockSandbox{}
	start := time.Now()
	if err := ensureDaemon(context.Background(), opencodeResolved(t), false, false, sb, &testUI, seams); err != nil {
		t.Fatalf("ensureDaemon: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= 500*time.Millisecond {
		t.Fatalf("ensureDaemon waited %v before its first health probe, want immediate", elapsed)
	}
}

// TestEnsureDaemonSkipsHealthyDaemon verifies the pre-check: a daemon that is
// already healthy is left running (no restart) and reported as such, rather
// than paying for a kill-and-relaunch.
func TestEnsureDaemonSkipsHealthyDaemon(t *testing.T) {
	testUI := termio.NewTestMock(t)
	fastDaemonPoll(t)
	seams := healthQueue(healthy())

	calls := &[]msb.MockDetachedCall{}
	sb := &msb.MockSandbox{ExecDetachedCalls: calls}

	if err := ensureDaemon(context.Background(), opencodeResolved(t), false, false, sb, &testUI, seams); err != nil {
		t.Fatalf("ensureDaemon: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("a healthy daemon must not be restarted, got %+v", *calls)
	}
	if !slices.Contains(testUI.VerboseCalls, "opencode daemon already healthy") {
		t.Fatalf("verbose calls = %q, want an already-healthy note", testUI.VerboseCalls)
	}
}

// TestEnsureDaemonLogsUnhealthyProbeDetail verifies an unhealthy pre-check
// reports the probe's detail, so a restart is diagnosable rather than silent.
func TestEnsureDaemonLogsUnhealthyProbeDetail(t *testing.T) {
	testUI := termio.NewTestMock(t)
	fastDaemonPoll(t)
	seams := healthQueue(unhealthy(), healthy())

	calls := &[]msb.MockDetachedCall{}
	sb := &msb.MockSandbox{ExecDetachedCalls: calls}

	if err := ensureDaemon(context.Background(), opencodeResolved(t), false, false, sb, &testUI, seams); err != nil {
		t.Fatalf("ensureDaemon: %v", err)
	}
	want := "opencode daemon not healthy: health probe exit 1: (no output)"
	if !slices.Contains(testUI.VerboseCalls, want) {
		t.Fatalf("verbose calls = %q, want %q", testUI.VerboseCalls, want)
	}
}

func TestEnsureDaemonForceRestartsHealthy(t *testing.T) {
	testUI := termio.NewTestMock(t)
	fastDaemonPoll(t)
	// force skips the pre-check; the first health probe is the post-launch one.
	seams := healthQueue(healthy())

	calls := &[]msb.MockDetachedCall{}
	sb := &msb.MockSandbox{ExecDetachedCalls: calls}

	if err := ensureDaemon(context.Background(), opencodeResolved(t), false, true, sb, &testUI, seams); err != nil {
		t.Fatalf("EnsureDaemon failed: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("force restart must launch a fresh daemon, got %+v", *calls)
	}
}

func TestEnsureDaemonKillsStaleJobByChangedArgs(t *testing.T) {
	testUI := termio.NewTestMock(t)
	fastDaemonPoll(t)
	seams := healthQueue(unhealthy(), healthy())

	stale := &msb.MockJob{ID_: "stale-daemon"}
	// The stale daemon was launched by a release that bound the previous
	// serve hostname, so matching the current command missed it (#343).
	sb := &msb.MockSandbox{
		Jobs: []msbSdk.JobInfo{
			{ID: "stale-daemon", Command: []string{"opencode", "serve", "--hostname", "0.0.0.0", "--port", "4096"}},
		},
		GetJobFn: func(context.Context, string) (msb.Job, error) { return stale, nil },
	}

	if err := ensureDaemon(context.Background(), opencodeResolved(t), false, false, sb, &testUI, seams); err != nil {
		t.Fatalf("EnsureDaemon failed: %v", err)
	}
	if !stale.DidKill {
		t.Error("stale daemon job was not killed before restart")
	}
}

func TestEnsureDaemonReportsCloseFailure(t *testing.T) {
	testUI := termio.NewTestMock(t)
	fastDaemonPoll(t)
	seams := healthQueue(unhealthy(), healthy())

	sb := &msb.MockSandbox{Job_: &msb.MockJob{ID_: "daemon", CloseErr: errors.New("close boom")}}
	if err := ensureDaemon(context.Background(), opencodeResolved(t), false, false, sb, &testUI, seams); err != nil {
		t.Fatalf("ensureDaemon failed: %v", err)
	}
	if !strings.Contains(strings.Join(testUI.VerboseCalls, "\n"), "closing opencode job handle") {
		t.Errorf("expected a verbose close failure, got %v", testUI.VerboseCalls)
	}
}

func TestEnsureDaemonCrashSurfacesExitAndLogs(t *testing.T) {
	testUI := termio.NewTestMock(t)
	fastDaemonPoll(t)
	seams := healthQueue(unhealthy())

	code := 2
	sb := &msb.MockSandbox{
		Job_: &msb.MockJob{
			Info:  &msbSdk.JobInfo{ExitCode: &code},
			Logs_: []msbSdk.JobLogEntry{{Data: []byte("serve exploded")}},
		},
	}

	err := ensureDaemon(context.Background(), opencodeResolved(t), false, false, sb, &testUI, seams)
	if err == nil || !strings.Contains(err.Error(), "exit 2") || !strings.Contains(err.Error(), "serve exploded") {
		t.Fatalf("ensureDaemon() = %v, want exit-code and log tail", err)
	}
}

func TestEnsureDaemonExecError(t *testing.T) {
	testUI := termio.NewTestMock(t)
	fastDaemonPoll(t)
	seams := healthQueue(unhealthy())

	wantErr := errors.New("connection lost")
	sb := &msb.MockSandbox{
		ExecDetachedFn: func(
			context.Context, string, []string, ...msbSdk.JobExecOption,
		) (msb.Job, error) {
			return nil, wantErr
		},
	}

	err := ensureDaemon(context.Background(), opencodeResolved(t), false, false, sb, &testUI, seams)
	if err == nil || !strings.Contains(err.Error(), "start opencode serve") {
		t.Fatalf("ensureDaemon() = %v, want start failure", err)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("ensureDaemon() = %v, want it to wrap %v", err, wantErr)
	}
}

func TestEnsureDaemonFailsAfterTimeout(t *testing.T) {
	testUI := termio.NewTestMock(t)

	origTimeout, origInterval := daemonReadyTimeout, daemonPollInterval
	t.Cleanup(func() {
		daemonReadyTimeout = origTimeout
		daemonPollInterval = origInterval
	})
	daemonReadyTimeout = 10 * time.Millisecond
	daemonPollInterval = time.Millisecond

	// A health func that is always unhealthy forces the timeout path.
	seams := options.Seams{DaemonHealth: func(context.Context, msb.Sandbox, string) (string, int, error) {
		return "", 1, nil
	}}

	err := ensureDaemon(context.Background(), opencodeResolved(t), false, false, &msb.MockSandbox{}, &testUI, seams)
	if err == nil {
		t.Fatal("expected error after timeout")
	}
	if !strings.Contains(err.Error(), "did not become ready") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}

// TestEnsureDaemonPollingReportsProbeDetail verifies the verbose poll line
// carries the health probe's outcome (exit code / body), so a daemon that never
// becomes healthy is diagnosable without guessing.
func TestEnsureDaemonPollingReportsProbeDetail(t *testing.T) {
	testUI := termio.NewTestMock(t)
	provider := opencodeProvider(t)
	seams := options.Seams{DaemonHealth: func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == provider.DaemonHealthCmd() {
			return "", 22, nil // non-zero probe exit, e.g. connection refused (7)
		}
		return "", 0, nil
	}}

	origTimeout, origInterval := daemonReadyTimeout, daemonPollInterval
	t.Cleanup(func() { daemonReadyTimeout, daemonPollInterval = origTimeout, origInterval })
	daemonReadyTimeout = 5 * time.Millisecond
	daemonPollInterval = time.Millisecond

	err := ensureDaemon(context.Background(), opencodeResolved(t), false, false, &msb.MockSandbox{}, &testUI, seams)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "health probe exit 22") {
		t.Errorf("timeout error missing probe detail: %v", err)
	}
	found := false
	for _, call := range testUI.VerboseCalls {
		if strings.Contains(call, "Polling for daemon health: health probe exit 22") {
			found = true
		}
	}
	if !found {
		t.Errorf("verbose polling missing probe context: %v", testUI.VerboseCalls)
	}
}

// TestEnsureDaemonTimeoutIncludesJobLogs verifies that a health timeout tails
// the daemon job's retained logs so the reason the daemon stayed down (a bind
// failure, a crash) is surfaced; a job is no longer a log file.
func TestEnsureDaemonTimeoutIncludesJobLogs(t *testing.T) {
	testUI := termio.NewTestMock(t)
	provider := opencodeProvider(t)
	seams := options.Seams{DaemonHealth: func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == provider.DaemonHealthCmd() {
			return "", 7, nil // connection refused: nothing listening
		}
		return "", 0, nil
	}}

	origTimeout, origInterval := daemonReadyTimeout, daemonPollInterval
	t.Cleanup(func() { daemonReadyTimeout, daemonPollInterval = origTimeout, origInterval })
	daemonReadyTimeout = 5 * time.Millisecond
	daemonPollInterval = time.Millisecond

	sb := &msb.MockSandbox{
		Job_: &msb.MockJob{
			Logs_: []msbSdk.JobLogEntry{{Data: []byte("error: cannot bind to 127.0.0.1:4096")}},
		},
	}

	err := ensureDaemon(context.Background(), opencodeResolved(t), false, false, sb, &testUI, seams)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "error: cannot bind to 127.0.0.1:4096") {
		t.Errorf("timeout error missing job log tail: %v", err)
	}
}

// TestProbeDaemonHealthReportsUnhealthyBody covers a parseable health response
// that reports the daemon as unhealthy: the raw body (non-empty here) is kept as
// the detail rather than the empty-body placeholder.
func TestProbeDaemonHealthReportsUnhealthyBody(t *testing.T) {
	health := func(_ context.Context, _ msb.Sandbox, _ string) (string, int, error) {
		return `{"healthy":false}`, 0, nil
	}

	probe := probeDaemonHealth(context.Background(), health, nil, fakeDaemonProvider{
		healthCmd: "health",
		parse:     func(string) (bool, error) { return false, nil },
	})
	if probe.healthy {
		t.Fatal("probe reported healthy for a server-reported-unhealthy response")
	}
	if want := `server reported unhealthy: {"healthy":false}`; probe.detail != want {
		t.Errorf("detail = %q, want %q", probe.detail, want)
	}
}

// TestProbeDaemonHealthReportsProbeError covers the health-command failure: the
// probe is unhealthy and its detail names the failure, rather than falling
// through to the exit-code classification.
func TestProbeDaemonHealthReportsProbeError(t *testing.T) {
	health := func(_ context.Context, _ msb.Sandbox, _ string) (string, int, error) {
		return "", -1, errors.New("dial refused")
	}

	probe := probeDaemonHealth(context.Background(), health, nil, fakeDaemonProvider{healthCmd: "health"})
	if probe.healthy {
		t.Fatal("probe reported healthy for a failed health command")
	}
	if want := "health probe failed: dial refused"; probe.detail != want {
		t.Errorf("detail = %q, want %q", probe.detail, want)
	}
}

// TestProbeDaemonHealthReportsParseError covers an unparsable health response:
// the detail names the parse failure, rather than falling through to the
// server-reported-unhealthy branch.
func TestProbeDaemonHealthReportsParseError(t *testing.T) {
	health := func(_ context.Context, _ msb.Sandbox, _ string) (string, int, error) {
		return "garbage", 0, nil
	}

	probe := probeDaemonHealth(context.Background(), health, nil, fakeDaemonProvider{
		healthCmd: "health",
		parse:     func(string) (bool, error) { return false, errors.New("bad json") },
	})
	if probe.healthy {
		t.Fatal("probe reported healthy for an unparsable response")
	}
	if want := "health response unparsable: bad json: garbage"; probe.detail != want {
		t.Errorf("detail = %q, want %q", probe.detail, want)
	}
}

// TestDefaultDaemonHealthFuncSuccess covers the default daemonHealthFunc success
// path: it forwards stdout and the exit code from sb.Shell.
func TestDefaultDaemonHealthFuncSuccess(t *testing.T) {
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			"cmd": msb.NewTestResult(true, 7, "hello", "", nil),
		},
	}
	// daemonHealthFunc is the production default closure; invoking it directly
	// exercises the production success path.
	stdout, code, err := daemonHealthFunc(context.Background(), sb, "cmd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout != "hello" {
		t.Errorf("stdout = %q, want hello", stdout)
	}
	if code != 7 {
		t.Errorf("exit code = %d, want 7", code)
	}
}

// TestDefaultDaemonHealthFuncErrorBranch covers the error branch of the default
// daemonHealthFunc closure: a failed sb.Shell yields exit code -1.
func TestDefaultDaemonHealthFuncErrorBranch(t *testing.T) {
	sb := &msb.MockSandbox{ShellErr: errors.New("shell failed")}
	stdout, code, err := daemonHealthFunc(context.Background(), sb, "cmd")
	if err == nil {
		t.Fatal("expected error from the default daemonHealthFunc")
	}
	if code != -1 {
		t.Errorf("exit code = %d, want -1", code)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
}

// TestDaemonReadinessDefaults pins the production readiness window and poll
// cadence. TestMain overwrites the vars for fast unit tests, so the declared
// values are observed through the capture in main_test.go; a zeroed or
// off-by-one-nanosecond deadline would make every run race the daemon's startup.
func TestDaemonReadinessDefaults(t *testing.T) {
	if capturedDaemonReadyTimeout != 60*time.Second {
		t.Errorf("daemon readiness timeout = %s, want 60s", capturedDaemonReadyTimeout)
	}
	if capturedDaemonPollInterval != 2*time.Second {
		t.Errorf("daemon poll interval = %s, want 2s", capturedDaemonPollInterval)
	}
}

// TestDefaultDaemonHealthFuncError covers the default daemonHealthFunc closure
// (the production default): when sb.Shell fails, it returns exit code -1.
func TestDefaultDaemonHealthFuncError(t *testing.T) {
	sb := &msb.MockSandbox{ShellErr: errors.New("shell failed")}
	stdout, code, err := daemonHealthFunc(context.Background(), sb, "some-command")
	if err == nil {
		t.Fatal("expected error from default daemonHealthFunc")
	}
	if code != -1 {
		t.Errorf("expected exit code -1 on error, got %d", code)
	}
	if stdout != "" {
		t.Errorf("expected empty stdout on error, got %q", stdout)
	}
}

// healthQueue returns a daemon-health seam returning queued probe responses in
// order. When the queue is exhausted the probe reports healthy.
func healthQueue(responses ...mockShellResp) options.Seams {
	index := 0
	return options.Seams{DaemonHealth: func(context.Context, msb.Sandbox, string) (string, int, error) {
		if index >= len(responses) {
			return `{"healthy":true}`, 0, nil
		}
		response := responses[index]
		index++
		return response.stdout, response.exitCode, response.err
	}}
}

// fastDaemonPoll shortens the daemon readiness poll for tests.
func fastDaemonPoll(t *testing.T) {
	t.Helper()
	origTimeout, origInterval := daemonReadyTimeout, daemonPollInterval
	t.Cleanup(func() {
		daemonReadyTimeout = origTimeout
		daemonPollInterval = origInterval
	})
	daemonReadyTimeout = 2 * time.Second
	daemonPollInterval = time.Millisecond
}
