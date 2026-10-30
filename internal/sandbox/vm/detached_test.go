package vm

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestJobExited(t *testing.T) {
	if got, exited := jobExited(nil); exited || got != 0 {
		t.Errorf("jobExited(nil) = (%d, %v), want (0, false)", got, exited)
	}
	if got, exited := jobExited(&msbSdk.JobInfo{}); exited || got != 0 {
		t.Errorf("jobExited(no exit code) = (%d, %v), want (0, false)", got, exited)
	}
	code := 7
	got, exited := jobExited(&msbSdk.JobInfo{ExitCode: &code})
	if !exited || got != 7 {
		t.Fatalf("jobExited() = (%d, %v), want (7, true)", got, exited)
	}
}

func TestJobTailLogs(t *testing.T) {
	job := &msb.MockJob{Logs_: []msbSdk.JobLogEntry{{Data: []byte("hello ")}, {Data: []byte("world")}}}
	if got := jobTailLogs(context.Background(), job); got != "hello world" {
		t.Errorf("jobTailLogs() = %q, want %q", got, "hello world")
	}
	failing := &msb.MockJob{LogsErr: errors.New("no logs")}
	if got := jobTailLogs(context.Background(), failing); got != "" {
		t.Errorf("jobTailLogs() on error = %q, want empty", got)
	}
	// A read can return partial entries alongside an error; the tail must be
	// discarded then, not concatenated from the partial slice.
	partial := &msb.MockJob{LogsFn: func(context.Context, msbSdk.JobLogOptions) ([]msbSdk.JobLogEntry, error) {
		return []msbSdk.JobLogEntry{{Data: []byte("partial")}}, errors.New("mid-read")
	}}
	if got := jobTailLogs(context.Background(), partial); got != "" {
		t.Errorf("jobTailLogs() on partial-error = %q, want empty", got)
	}
}

// TestJobTailLogsRequestsTail pins the requested tail length with a literal: the
// runtime only returns the trailing bytes when Tail is set, and a symbolic
// expectation would move with a mutated constant instead of killing it.
func TestJobTailLogsRequestsTail(t *testing.T) {
	var captured msbSdk.JobLogOptions
	job := &msb.MockJob{LogsFn: func(_ context.Context, opts msbSdk.JobLogOptions) ([]msbSdk.JobLogEntry, error) {
		captured = opts
		return nil, nil
	}}
	jobTailLogs(context.Background(), job)
	if captured.Tail == nil || *captured.Tail != 100 {
		t.Fatalf("JobLogOptions.Tail = %v, want 100", captured.Tail)
	}
}

// TestDetachedHelperMarkedCommand pins the launch shape that carries the
// helper's identity, with literals: the marker and the env(1) hop are the
// contract the kill path matches against, so a rename that kept the constants
// in sync must still fail here.
func TestDetachedHelperMarkedCommand(t *testing.T) {
	args := []string{"-H", "unix:///var/run/docker.sock"}
	binary, commandArgs := dockerdHelper().markedCommand(args)

	if binary != "env" {
		t.Errorf("launch binary = %q, want %q", binary, "env")
	}
	want := []string{
		"AGENTS_SANDBOX_DETACHED_JOB=dockerd",
		"dockerd",
		"-H",
		"unix:///var/run/docker.sock",
	}
	if !slices.Equal(commandArgs, want) {
		t.Errorf("launch args = %v, want %v", commandArgs, want)
	}
	// The helper's own arguments are copied, never aliased: the caller keeps
	// using the slice it passed.
	if !slices.Equal(args, []string{"-H", "unix:///var/run/docker.sock"}) {
		t.Errorf("caller args = %v, want them unchanged", args)
	}
}

// TestDetachedHelperMarkerlessFallbackScope pins the exact scope of the
// transitional marker-less fallback: a marker-less command matches only the
// helper whose binary is its argv[0], whatever other arguments it carries, and
// nothing else. The fallback exists for helpers started by a launcher that
// predates the marker, so it must not widen into a catch-all — matching more
// binaries, a path to one, or a marker-less shape that is not the helper.
func TestDetachedHelperMarkerlessFallbackScope(t *testing.T) {
	tests := []struct {
		name    string
		helper  detachedHelper
		command []string
		want    bool
	}{
		{"dockerd", dockerdHelper(), []string{"dockerd"}, true},
		{
			name:    "dockerd with arguments the current release no longer sends",
			helper:  dockerdHelper(),
			command: []string{"dockerd", "--log-format=text"},
			want:    true,
		},
		{"an unrelated binary", dockerdHelper(), []string{"containerd"}, false},
		{"a prefix of the helper binary", dockerdHelper(), []string{"docker"}, false},
		{"a suffix of the helper binary", dockerdHelper(), []string{"erd"}, false},
		{"a path to the helper binary", dockerdHelper(), []string{"/usr/bin/dockerd"}, false},
		{"the launcher binary alone", dockerdHelper(), []string{"env"}, false},
		{"a shell command naming the helper", dockerdHelper(), []string{"sh", "-c", "dockerd"}, false},
		{"an empty command", dockerdHelper(), nil, false},
		{"the agent daemon binary", agentDaemonHelper("opencode"), []string{"opencode", "serve"}, true},
		{
			name:    "another agent build's daemon binary",
			helper:  agentDaemonHelper("opencode"),
			command: []string{"opencode2", "serve"},
			want:    false,
		},
		{
			name:    "the opencode2 daemon for its own identity",
			helper:  agentDaemonHelper("opencode2"),
			command: []string{"opencode2", "serve", "--hostname", "0.0.0.0"},
			want:    true,
		},
		{
			name:    "another helper's marker",
			helper:  dockerdHelper(),
			command: []string{"env", "AGENTS_SANDBOX_DETACHED_JOB=agent-daemon", "dockerd"},
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.helper.matches(tt.command); got != tt.want {
				t.Errorf("%s.matches(%v) = %v, want %v", tt.helper.marker, tt.command, got, tt.want)
			}
		})
	}
}

func TestDetachedHelperMatches(t *testing.T) {
	tests := []struct {
		name    string
		helper  detachedHelper
		command []string
		want    bool
	}{
		{
			name:    "marked",
			helper:  dockerdHelper(),
			command: []string{"env", "AGENTS_SANDBOX_DETACHED_JOB=dockerd", "dockerd", "-H", "x"},
			want:    true,
		},
		{
			name:   "marked with arguments a previous release used",
			helper: dockerdHelper(),
			command: []string{
				"env", "AGENTS_SANDBOX_DETACHED_JOB=dockerd", "dockerd",
				"-H", "unix:///var/run/docker.sock", "--log-format=text",
			},
			want: true,
		},
		{
			name:    "marked with no helper arguments",
			helper:  dockerdHelper(),
			command: []string{"env", "AGENTS_SANDBOX_DETACHED_JOB=dockerd", "dockerd"},
			want:    true,
		},
		{
			name:    "marker assignment only",
			helper:  dockerdHelper(),
			command: []string{"env", "AGENTS_SANDBOX_DETACHED_JOB=dockerd"},
			want:    true,
		},
		{
			name:    "launcher with no marker assignment",
			helper:  dockerdHelper(),
			command: []string{"env"},
			want:    false,
		},
		{
			name:    "marker-less legacy launch",
			helper:  dockerdHelper(),
			command: []string{"dockerd", "-H", "unix:///var/run/docker.sock"},
			want:    true,
		},
		{
			name:    "marker-less legacy launch with arguments the current release no longer sends",
			helper:  dockerdHelper(),
			command: []string{"dockerd", "-H", "unix:///var/run/docker.sock", "--log-format=text"},
			want:    true,
		},
		{
			name:    "another helper's marker",
			helper:  dockerdHelper(),
			command: []string{"env", "AGENTS_SANDBOX_DETACHED_JOB=agent-daemon", "opencode", "serve"},
			want:    false,
		},
		{
			name:    "another helper's marker on this helper's binary",
			helper:  dockerdHelper(),
			command: []string{"env", "AGENTS_SANDBOX_DETACHED_JOB=agent-daemon", "dockerd"},
			want:    false,
		},
		{
			name:    "marker in a later argument position",
			helper:  dockerdHelper(),
			command: []string{"sh", "-c", "AGENTS_SANDBOX_DETACHED_JOB=dockerd dockerd"},
			want:    false,
		},
		{
			name:    "unrelated command",
			helper:  dockerdHelper(),
			command: []string{"sleep", "1"},
			want:    false,
		},
		{
			name:    "empty command",
			helper:  dockerdHelper(),
			command: nil,
			want:    false,
		},
		{
			name:    "agent daemon marked",
			helper:  agentDaemonHelper("opencode"),
			command: []string{"env", "AGENTS_SANDBOX_DETACHED_JOB=agent-daemon", "opencode", "serve"},
			want:    true,
		},
		{
			name:    "agent daemon marker-less legacy launch with the previous serve hostname",
			helper:  agentDaemonHelper("opencode"),
			command: []string{"opencode", "serve", "--hostname", "127.0.0.1", "--port", "4096"},
			want:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.helper.matches(tt.command); got != tt.want {
				t.Errorf("%s.matches(%v) = %v, want %v", tt.helper.marker, tt.command, got, tt.want)
			}
		})
	}
}

// TestKillJobsTargetsStaleHelperWithChangedArgs is the #343 regression: after a
// release changes a helper's arguments (dockerd's storage-driver flag, the
// daemon's serve hostname), the stale helper is still running with the old
// arguments. Matching the recorded command against the new binary+args misses
// it, so it keeps the socket/port the fresh helper then races for and the launch
// fails with "exited before becoming ready".
func TestKillJobsTargetsStaleHelperWithChangedArgs(t *testing.T) {
	ui := termio.NewTestMock(t)
	legacy := &msb.MockJob{ID_: "legacy-dockerd"}
	marked := &msb.MockJob{ID_: "marked-dockerd"}
	otherHelper := &msb.MockJob{ID_: "daemon"}
	sb := &msb.MockSandbox{
		Jobs: []msbSdk.JobInfo{
			{ID: "legacy-dockerd", Command: []string{"dockerd", "-H", "unix:///var/run/docker.sock"}},
			{
				ID: "marked-dockerd",
				Command: []string{
					"env", "AGENTS_SANDBOX_DETACHED_JOB=dockerd", "dockerd", "-H", "x",
				},
			},
			{ID: "daemon", Command: []string{"env", "AGENTS_SANDBOX_DETACHED_JOB=agent-daemon", "opencode", "serve"}},
		},
		GetJobFn: func(_ context.Context, id string) (msb.Job, error) {
			switch id {
			case "legacy-dockerd":
				return legacy, nil
			case "marked-dockerd":
				return marked, nil
			default:
				return otherHelper, nil
			}
		},
	}

	killJobs(context.Background(), sb, dockerdHelper(), &ui)

	if !legacy.DidKill {
		t.Error("a stale helper launched with different arguments was not killed")
	}
	if !marked.DidKill {
		t.Error("a marked stale helper was not killed")
	}
	if otherHelper.DidKill {
		t.Error("another helper's job was killed")
	}
}

func TestKillJobsKillsMatches(t *testing.T) {
	ui := termio.NewTestMock(t)
	stale := &msb.MockJob{ID_: "stale"}
	other := &msb.MockJob{ID_: "other"}
	// The non-matching job comes first: a matcher that stopped at the first
	// non-match (instead of continuing) would leave the matching job alive.
	sb := &msb.MockSandbox{
		Jobs: []msbSdk.JobInfo{
			{ID: "other", Command: []string{"sleep", "1"}},
			{ID: "stale", Command: []string{"dockerd", "-H", "x"}},
		},
		GetJobFn: func(_ context.Context, id string) (msb.Job, error) {
			if id == "stale" {
				return stale, nil
			}
			return other, nil
		},
	}
	killJobs(context.Background(), sb, dockerdHelper(), &ui)

	if !stale.DidKill {
		t.Error("matching stale job was not killed")
	}
	if !stale.DidClose {
		t.Error("killed job handle was not closed")
	}
	if other.DidKill {
		t.Error("non-matching job was killed")
	}
	if !slices.Contains(ui.VerboseCalls, "killed stale job stale") {
		t.Errorf("expected a verbose kill note, got %v", ui.VerboseCalls)
	}
}

// TestKillJobsContinuesAfterKillFailure verifies a failed kill does not
// abandon the remaining matching jobs.
func TestKillJobsContinuesAfterKillFailure(t *testing.T) {
	failing := &msb.MockJob{ID_: "a", KillErr: errors.New("kill boom")}
	healthy := &msb.MockJob{ID_: "b"}
	sb := &msb.MockSandbox{
		Jobs: []msbSdk.JobInfo{
			{ID: "a", Command: []string{"dockerd"}},
			{ID: "b", Command: []string{"dockerd"}},
		},
		GetJobFn: func(_ context.Context, id string) (msb.Job, error) {
			if id == "a" {
				return failing, nil
			}
			return healthy, nil
		},
	}
	killJobs(context.Background(), sb, dockerdHelper(), &termio.Mock{})

	if !healthy.DidKill {
		t.Error("a kill failure must not stop later matching jobs from being killed")
	}
}

func TestKillJobsToleratesFailures(t *testing.T) {
	ui := termio.NewTestMock(t)
	sb := &msb.MockSandbox{
		ListJobsFn: func(context.Context, ...msbSdk.JobListOption) ([]msbSdk.JobInfo, error) {
			return nil, errors.New("list boom")
		},
	}
	killJobs(context.Background(), sb, dockerdHelper(), &ui)
	if !strings.Contains(strings.Join(ui.VerboseCalls, "\n"), "listing jobs") {
		t.Errorf("expected a verbose listing failure, got %v", ui.VerboseCalls)
	}
}

func TestKillJobsToleratesGetAndKillErrors(t *testing.T) {
	ui := termio.NewTestMock(t)
	killFailJob := &msb.MockJob{ID_: "b", KillErr: errors.New("kill boom")}
	sb := &msb.MockSandbox{
		Jobs: []msbSdk.JobInfo{
			{ID: "a", Command: []string{"dockerd"}},
			{ID: "b", Command: []string{"dockerd"}},
		},
		GetJobFn: func(_ context.Context, id string) (msb.Job, error) {
			if id == "a" {
				return nil, errors.New("get boom")
			}
			return killFailJob, nil
		},
	}
	killJobs(context.Background(), sb, dockerdHelper(), &ui)

	joined := strings.Join(ui.VerboseCalls, "\n")
	if !strings.Contains(joined, "resolving job") || !strings.Contains(joined, "killing job") {
		t.Errorf("expected verbose get/kill failures, got %v", ui.VerboseCalls)
	}
	if !killFailJob.DidClose {
		t.Error("job handle was not closed when Kill failed")
	}
}

func TestKillJobsReportsCloseFailure(t *testing.T) {
	ui := termio.NewTestMock(t)
	closing := &msb.MockJob{ID_: "closing", CloseErr: errors.New("close boom")}
	sb := &msb.MockSandbox{
		Jobs: []msbSdk.JobInfo{{ID: "closing", Command: []string{"dockerd"}}},
		GetJobFn: func(context.Context, string) (msb.Job, error) {
			return closing, nil
		},
	}
	killJobs(context.Background(), sb, dockerdHelper(), &ui)

	if !closing.DidKill {
		t.Error("expected the matching job to be killed")
	}
	if !strings.Contains(strings.Join(ui.VerboseCalls, "\n"), "closing job") {
		t.Errorf("expected a verbose close failure, got %v", ui.VerboseCalls)
	}
}

func TestAwaitJobReadyInspectErrorKeepsPolling(t *testing.T) {
	job := &msb.MockJob{
		InspectFn: func(context.Context) (*msbSdk.JobInfo, error) { return nil, errors.New("inspect boom") },
	}
	probe := func(context.Context) (bool, string) { return true, "" }
	if err := awaitJobReady(
		context.Background(), job, "dockerd", probe, nil, time.Second, time.Millisecond,
	); err != nil {
		t.Fatalf("awaitJobReady() = %v, want nil (inspect error must not be death)", err)
	}
}

// TestInspectJobErrorReturnsNil verifies a transient Inspect error is reported
// as no info, even when the runtime returns a non-nil info alongside the error,
// so the readiness loop keeps polling rather than treating a stale result as
// current.
func TestInspectJobErrorReturnsNil(t *testing.T) {
	job := &msb.MockJob{InspectFn: func(context.Context) (*msbSdk.JobInfo, error) {
		return &msbSdk.JobInfo{ID: "x"}, errors.New("inspect boom")
	}}
	if got := inspectJob(context.Background(), job); got != nil {
		t.Fatalf("inspectJob() = %v, want nil on error", got)
	}
}

func TestAwaitJobReadyReturnsOnProbe(t *testing.T) {
	calls := 0
	probe := func(context.Context) (bool, string) {
		calls++
		return calls >= 2, ""
	}
	if err := awaitJobReady(
		context.Background(), &msb.MockJob{}, "daemon", probe, nil, time.Second, time.Millisecond,
	); err != nil {
		t.Fatalf("awaitJobReady() = %v, want nil", err)
	}
}

func TestAwaitJobReadyCrashReturnsExitAndLogs(t *testing.T) {
	code := 1
	job := &msb.MockJob{
		Info:  &msbSdk.JobInfo{ExitCode: &code},
		Logs_: []msbSdk.JobLogEntry{{Data: []byte("containerd failed")}},
	}
	err := awaitJobReady(
		context.Background(),
		job,
		"dockerd",
		func(context.Context) (bool, string) { return false, "" },
		nil,
		time.Second,
		time.Millisecond,
	)
	if err == nil || !strings.Contains(err.Error(), "exit 1") || !strings.Contains(err.Error(), "containerd failed") {
		t.Fatalf("awaitJobReady() = %v, want exit-code and log tail", err)
	}
}

func TestAwaitJobReadyTimeoutIncludesLogs(t *testing.T) {
	job := &msb.MockJob{Logs_: []msbSdk.JobLogEntry{{Data: []byte("timed-out sentinel")}}}
	err := awaitJobReady(
		context.Background(),
		job,
		"dockerd",
		func(context.Context) (bool, string) { return false, "still down" },
		nil,
		time.Millisecond,
		time.Millisecond,
	)
	if err == nil || !strings.Contains(err.Error(), "timed-out sentinel") {
		t.Fatalf("awaitJobReady() = %v, want timeout with log tail", err)
	}
	if !strings.Contains(err.Error(), "still down") {
		t.Errorf("awaitJobReady() = %v, want the probe detail in the timeout", err)
	}
}

func TestAwaitJobReadyContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := awaitJobReady(
		ctx,
		&msb.MockJob{},
		"dockerd",
		func(context.Context) (bool, string) { return false, "" },
		nil,
		time.Hour,
		time.Hour,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("awaitJobReady() = %v, want context.Canceled", err)
	}
}

func TestProbeHealthy(t *testing.T) {
	failing := msb.NewMockSandbox(msb.SandboxOpts{ShellErr: errors.New("shell boom")})
	if healthy, detail := probeHealthy(context.Background(), failing, "docker info"); healthy {
		t.Error("shell error must be unhealthy")
	} else if !strings.Contains(detail, "shell boom") {
		t.Errorf("detail = %q, want the shell error", detail)
	}
	ok := msb.NewMockSandbox(msb.SandboxOpts{})
	if healthy, _ := probeHealthy(context.Background(), ok, "other probe"); !healthy {
		t.Error("successful shell must be healthy")
	}
	noResult := msb.NewMockSandbox(msb.SandboxOpts{ShellOut: map[string]msb.ShellResult{"nil probe": nil}})
	if healthy, detail := probeHealthy(
		context.Background(),
		noResult,
		"nil probe",
	); healthy ||
		detail != "probe returned no result" {
		t.Errorf("probeHealthy() = (%v, %q), want (false, %q)", healthy, detail, "probe returned no result")
	}
}

// TestProbeHealthyReportsFailedCommandDetail covers the diagnostic detail a
// failing (but executing) probe returns: stderr preferred, then stdout, then an
// empty-body placeholder.
func TestProbeHealthyReportsFailedCommandDetail(t *testing.T) {
	cases := []struct {
		name   string
		result msb.ShellResult
		want   string
	}{
		{
			name:   "stderr",
			result: msb.NewTestResult(false, 1, "", "cannot connect to the Docker daemon", nil),
			want:   "probe exit 1: cannot connect to the Docker daemon",
		},
		{
			name:   "stdout fallback",
			result: msb.NewTestResult(false, 2, "from stdout", "", nil),
			want:   "probe exit 2: from stdout",
		},
		{
			name:   "no output",
			result: msb.NewTestResult(false, 7, "", "", nil),
			want:   "probe exit 7: (no output)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sb := msb.NewMockSandbox(msb.SandboxOpts{
				ShellOut: map[string]msb.ShellResult{"docker info": tc.result},
			})
			healthy, detail := probeHealthy(context.Background(), sb, "docker info")
			if healthy {
				t.Fatal("non-zero probe must be unhealthy")
			}
			if detail != tc.want {
				t.Errorf("detail = %q, want %q", detail, tc.want)
			}
		})
	}
}
