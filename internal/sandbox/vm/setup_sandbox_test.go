package vm

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/creack/pty"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// TestRunStartupHooksExecutesEachHook pins how a hook is run: as the default
// sandbox user with its home, or as root with /root when it asks for root; a
// script with a shebang is executed directly so the kernel applies it, one
// without is run through /bin/sh. Hooks run through the non-interactive
// streaming exec path, so a non-TTY run still runs them.
func TestRunStartupHooksExecutesEachHook(t *testing.T) {
	cases := []struct {
		name     string
		hook     homeconfig.HookSpec
		wantUser string
		wantHome string
		wantCmd  string
		wantArgs []string
	}{
		{
			name:     "default user, no shebang",
			hook:     homeconfig.HookSpec{Target: "/home/dev/.vpn/connect.sh", Source: "x"},
			wantUser: DefaultSandboxUser,
			wantHome: "/home/dev",
			wantCmd:  "/bin/sh",
			wantArgs: []string{"/home/dev/.vpn/connect.sh"},
		},
		{
			name:     "shebang",
			hook:     homeconfig.HookSpec{Target: "/home/dev/.foo.sh", Source: "x", HasShebang: true},
			wantUser: DefaultSandboxUser,
			wantHome: "/home/dev",
			wantCmd:  "/home/dev/.foo.sh",
		},
		{
			name: "root",
			hook: homeconfig.HookSpec{
				Target:     "/home/dev/.vpn/connect.sh",
				Source:     "x",
				Root:       true,
				HasShebang: true,
			},
			wantUser: "root",
			wantHome: "/root",
			wantCmd:  "/home/dev/.vpn/connect.sh",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sb := &msbtest.MockSandbox{Name_: "vm"}
			ui := termiotest.NewTestMock(t)

			runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{tc.hook}, &ui, false)

			if sb.ExecStreamUser != tc.wantUser || sb.ExecStreamEnv["HOME"] != tc.wantHome {
				t.Errorf("executed as %q with HOME %q, want %q with %q",
					sb.ExecStreamUser, sb.ExecStreamEnv["HOME"], tc.wantUser, tc.wantHome)
			}
			if sb.ExecStreamCmd != tc.wantCmd || !slices.Equal(sb.ExecStreamArgs, tc.wantArgs) {
				t.Errorf("executed %q %v, want %q %v", sb.ExecStreamCmd, sb.ExecStreamArgs, tc.wantCmd, tc.wantArgs)
			}
		})
	}
}

// TestRunStartupHooksContinuesPastAFailingHook pins that a hook that cannot
// start is a warning and the remaining hooks still run.
func TestRunStartupHooksContinuesPastAFailingHook(t *testing.T) {
	sb := &msbtest.MockSandbox{
		Name_: "vm",
		ExecStreamFn: func(_ context.Context, command string, _ []string, _ msb.ExecRequest) (msb.StreamHandle, error) {
			if command == "/home/dev/.first.sh" {
				return msbtest.NewScriptedStreamHandle(
					msb.StreamEvent{Kind: msb.StreamEventFailed},
				), nil
			}
			return msbtest.NewScriptedStreamHandle(
				msb.StreamEvent{Kind: msb.StreamEventExited, ExitCode: 0},
			), nil
		},
	}
	ui := termiotest.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.first.sh", Source: "x", HasShebang: true},
		{Target: "/home/dev/.second.sh", Source: "x", HasShebang: true},
	}, &ui, false)

	if sb.ExecStreamCmd != "/home/dev/.second.sh" {
		t.Errorf("last executed hook = %q, want the second hook to run after the first failed", sb.ExecStreamCmd)
	}
	if len(ui.WarnCalls) != 1 || !strings.Contains(ui.WarnCalls[0], "startup hook /home/dev/.first.sh failed") {
		t.Errorf("warnings = %v, want one for the failed hook", ui.WarnCalls)
	}
}

// TestRunStartupHooksWarnsWhenAHookExitsNonZero pins that a hook that runs but
// exits non-zero is reported with its target and the failure reason (the exit
// code and stderr), and does not stop the remaining hooks.
func TestRunStartupHooksWarnsWhenAHookExitsNonZero(t *testing.T) {
	sb := &msbtest.MockSandbox{
		Name_: "vm",
		ExecStreamFn: func(_ context.Context, command string, args []string, _ msb.ExecRequest) (msb.StreamHandle, error) {
			if command == "/bin/sh" && slices.Equal(args, []string{"/home/dev/.first.sh"}) {
				return msbtest.NewScriptedStreamHandle(
					msb.StreamEvent{Kind: msb.StreamEventStderr, Data: []byte("ca import failed\n")},
					msb.StreamEvent{Kind: msb.StreamEventExited, ExitCode: 2},
				), nil
			}
			return msbtest.NewScriptedStreamHandle(
				msb.StreamEvent{Kind: msb.StreamEventExited, ExitCode: 0},
			), nil
		},
	}
	ui := termiotest.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.first.sh", Source: "x"},
		{Target: "/home/dev/.second.sh", Source: "x"},
	}, &ui, false)

	if sb.ExecStreamCmd != "/bin/sh" || !slices.Equal(sb.ExecStreamArgs, []string{"/home/dev/.second.sh"}) {
		t.Errorf("last executed hook = %q %v, want the second hook to run after the first failed",
			sb.ExecStreamCmd, sb.ExecStreamArgs)
	}
	if len(ui.WarnCalls) != 1 {
		t.Fatalf("warnings = %v, want one for the failed hook", ui.WarnCalls)
	}
	for _, want := range []string{"/home/dev/.first.sh", "exit 2", "ca import failed"} {
		if !strings.Contains(ui.WarnCalls[0], want) {
			t.Errorf("warning %q missing %q", ui.WarnCalls[0], want)
		}
	}
}

// TestRunStartupHooksStreamsUnboundedHookOutput pins that a hook runs through
// the streaming exec seam, not the collected one: the collected response is
// capped at ~1 MiB, so a hook that emits more would be cut off and reported as
// failed even when it exits 0. The streaming seam reports the real exit code
// however much the hook prints. ExecErr is set to simulate the collected seam's
// bound, so a regression to the collected path fails this test.
func TestRunStartupHooksStreamsUnboundedHookOutput(t *testing.T) {
	sb := &msbtest.MockSandbox{
		Name_:   "vm",
		ExecErr: errors.New("exec output exceeds the collected response bound"),
		ExecStreamFn: func(_ context.Context, _ string, _ []string, _ msb.ExecRequest) (msb.StreamHandle, error) {
			// More output than the collected seam's 1 MiB response buffer.
			output := bytes.Repeat([]byte("x"), 2<<20)
			return msbtest.NewScriptedStreamHandle(
				msb.StreamEvent{Kind: msb.StreamEventStdout, Data: output},
				msb.StreamEvent{Kind: msb.StreamEventExited, ExitCode: 0},
			), nil
		},
	}
	ui := termiotest.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.big.sh", Source: "x", HasShebang: true},
	}, &ui, false)

	if len(ui.WarnCalls) != 0 {
		t.Errorf("warnings = %v, want none for a hook that exits 0 however much it prints", ui.WarnCalls)
	}
	if got := ui.StdOutBuffer.Len(); got != 2<<20 {
		t.Errorf("forwarded %d bytes of hook stdout, want %d", got, 2<<20)
	}
}

// TestRunStartupHooksWritesOutputVerbatim pins that hook output goes to the raw
// stdout/stderr streams, not through a line logger: a chunk is not a line, so a
// line split across two chunks stays one line and no extra newlines are added.
func TestRunStartupHooksWritesOutputVerbatim(t *testing.T) {
	sb := &msbtest.MockSandbox{
		Name_: "vm",
		ExecStreamFn: func(_ context.Context, _ string, _ []string, _ msb.ExecRequest) (msb.StreamHandle, error) {
			return msbtest.NewScriptedStreamHandle(
				msb.StreamEvent{Kind: msb.StreamEventStdout, Data: []byte("hel")},
				msb.StreamEvent{Kind: msb.StreamEventStdout, Data: []byte("lo\n")},
				msb.StreamEvent{Kind: msb.StreamEventStderr, Data: []byte("warn")},
				msb.StreamEvent{Kind: msb.StreamEventExited, ExitCode: 0},
			), nil
		},
	}
	ui := termiotest.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.hook.sh", Source: "x", HasShebang: true},
	}, &ui, false)

	if got := ui.StdOutBuffer.String(); got != "hello\n" {
		t.Errorf("stdout = %q, want %q (verbatim, no added newlines)", got, "hello\n")
	}
	if got := ui.StdErrBuffer.String(); got != "warn" {
		t.Errorf("stderr = %q, want %q", got, "warn")
	}
}

// TestRunStartupHooksBoundsRetainedStderr pins that a hook with large stderr
// does not grow the launcher's memory for the hook's lifetime or produce an
// unbounded failure warning: only a bounded tail of stderr is retained for the
// reason.
func TestRunStartupHooksBoundsRetainedStderr(t *testing.T) {
	sb := &msbtest.MockSandbox{
		Name_: "vm",
		ExecStreamFn: func(_ context.Context, _ string, _ []string, _ msb.ExecRequest) (msb.StreamHandle, error) {
			// 1 MiB of stderr, then the meaningful tail and a non-zero exit.
			chunk := bytes.Repeat([]byte("e"), 64<<10)
			events := make([]msb.StreamEvent, 0, 18)
			for range 16 {
				events = append(events, msb.StreamEvent{Kind: msb.StreamEventStderr, Data: chunk})
			}
			events = append(events,
				msb.StreamEvent{Kind: msb.StreamEventStderr, Data: []byte("boom")},
				msb.StreamEvent{Kind: msb.StreamEventExited, ExitCode: 1},
			)
			return msbtest.NewScriptedStreamHandle(events...), nil
		},
	}
	ui := termiotest.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.big.sh", Source: "x", HasShebang: true},
	}, &ui, false)

	if len(ui.WarnCalls) != 1 {
		t.Fatalf("warnings = %v, want one for the failed hook", ui.WarnCalls)
	}
	const prefix = "startup hook /home/dev/.big.sh failed: exit 1: "
	if got := ui.WarnCalls[0]; len(got) > len(prefix)+stderrTailBytes {
		t.Errorf("warning is %d bytes, want at most %d", len(got), len(prefix)+stderrTailBytes)
	}
	if !strings.HasSuffix(ui.WarnCalls[0], "boom") {
		t.Errorf("warning %q does not keep the last stderr bytes", ui.WarnCalls[0])
	}
}

// TestRunStartupHooksReportsWhyAHookCouldNotStart pins that the streaming
// failure detail (the SDK's exec failure message) is surfaced, not just a bare
// "could not start".
func TestRunStartupHooksReportsWhyAHookCouldNotStart(t *testing.T) {
	sb := &msbtest.MockSandbox{
		Name_: "vm",
		ExecStreamFn: func(_ context.Context, _ string, _ []string, _ msb.ExecRequest) (msb.StreamHandle, error) {
			return msbtest.NewScriptedStreamHandle(
				msb.StreamEvent{
					Kind:    msb.StreamEventFailed,
					Failure: "spawn failed (EACCES): /home/dev/.hook.sh",
				},
			), nil
		},
	}
	ui := termiotest.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.hook.sh", Source: "x", HasShebang: true},
	}, &ui, false)

	if len(ui.WarnCalls) != 1 {
		t.Fatalf("warnings = %v, want one for the failed hook", ui.WarnCalls)
	}
	if !strings.Contains(ui.WarnCalls[0], "spawn failed (EACCES): /home/dev/.hook.sh") {
		t.Errorf("warning %q omits the SDK failure detail", ui.WarnCalls[0])
	}
}

// recvErrorStreamHandle returns one error from Recv and then one event, so a
// test can drive a caller's stream-error branch while still letting the caller
// finish when that branch is skipped.
type recvErrorStreamHandle struct {
	err   error
	event msb.StreamEvent
	sent  bool
}

func (h *recvErrorStreamHandle) Recv(context.Context) (msb.StreamEvent, error) {
	if !h.sent {
		h.sent = true
		return msb.StreamEvent{}, h.err
	}
	return h.event, nil
}

func (*recvErrorStreamHandle) Close() error { return nil }

// TestRunStartupHooksWarnsWhenTheExecCannotStart pins that a streaming exec
// that cannot be opened is reported with its target and the error, not
// swallowed.
func TestRunStartupHooksWarnsWhenTheExecCannotStart(t *testing.T) {
	sb := &msbtest.MockSandbox{
		Name_: "vm",
		ExecStreamFn: func(_ context.Context, _ string, _ []string, _ msb.ExecRequest) (msb.StreamHandle, error) {
			return nil, errors.New("exec stream refused")
		},
	}
	ui := termiotest.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.hook.sh", Source: "x", HasShebang: true},
	}, &ui, false)

	if len(ui.WarnCalls) != 1 {
		t.Fatalf("warnings = %v, want one for the hook that could not start", ui.WarnCalls)
	}
	for _, want := range []string{"/home/dev/.hook.sh", "exec stream refused"} {
		if !strings.Contains(ui.WarnCalls[0], want) {
			t.Errorf("warning %q missing %q", ui.WarnCalls[0], want)
		}
	}
}

// TestRunStartupHooksWarnsWhenTheStreamFails pins that a receive error while
// the context is live is reported with its target and the error.
func TestRunStartupHooksWarnsWhenTheStreamFails(t *testing.T) {
	sb := &msbtest.MockSandbox{
		Name_: "vm",
		ExecStreamFn: func(_ context.Context, _ string, _ []string, _ msb.ExecRequest) (msb.StreamHandle, error) {
			return &recvErrorStreamHandle{
				err:   errors.New("stream broke"),
				event: msb.StreamEvent{Kind: msb.StreamEventExited, ExitCode: 0},
			}, nil
		},
	}
	ui := termiotest.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.hook.sh", Source: "x", HasShebang: true},
	}, &ui, false)

	if len(ui.WarnCalls) != 1 {
		t.Fatalf("warnings = %v, want one for the failed stream", ui.WarnCalls)
	}
	for _, want := range []string{"/home/dev/.hook.sh", "stream broke"} {
		if !strings.Contains(ui.WarnCalls[0], want) {
			t.Errorf("warning %q missing %q", ui.WarnCalls[0], want)
		}
	}
}

// TestRunStartupHooksStaysSilentOnACancelledStream pins that a receive that
// fails because the context was cancelled is not reported as a hook failure.
func TestRunStartupHooksStaysSilentOnACancelledStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sb := &msbtest.MockSandbox{
		Name_: "vm",
		ExecStreamFn: func(_ context.Context, _ string, _ []string, _ msb.ExecRequest) (msb.StreamHandle, error) {
			return &recvErrorStreamHandle{
				err:   context.Canceled,
				event: msb.StreamEvent{Kind: msb.StreamEventExited, ExitCode: 0},
			}, nil
		},
	}
	ui := termiotest.NewTestMock(t)

	runStartupHooks(ctx, sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.hook.sh", Source: "x", HasShebang: true},
	}, &ui, false)

	if len(ui.WarnCalls) != 0 {
		t.Errorf("warnings = %v, want none for a cancelled receive", ui.WarnCalls)
	}
	if ui.StdOutBuffer.Len() != 0 || ui.StdErrBuffer.Len() != 0 {
		t.Errorf("output = %q/%q, want none", ui.StdOutBuffer.String(), ui.StdErrBuffer.String())
	}
}

// TestExitReason pins a hook's failure reason: the exit code alone when the
// hook produced no stderr, and the code with the retained tail otherwise.
func TestExitReason(t *testing.T) {
	if got := exitReason(3, ""); got != "exit 3" {
		t.Errorf("exitReason(3, %q) = %q, want %q", "", got, "exit 3")
	}
	if got := exitReason(3, "boom"); got != "exit 3: boom" {
		t.Errorf("exitReason(3, %q) = %q, want %q", "boom", got, "exit 3: boom")
	}
}

// TestStderrTailKeepsOnlyTheLastBytes covers the bounded stderr buffer: it
// retains at most stderrTailBytes and drops the oldest bytes first.
func TestStderrTailKeepsOnlyTheLastBytes(t *testing.T) {
	var tail stderrTail
	tail.Write([]byte("old"))
	tail.Write(bytes.Repeat([]byte("x"), stderrTailBytes))
	tail.Write([]byte("new"))

	got := tail.String()
	if len(got) != stderrTailBytes {
		t.Fatalf("retained %d bytes, want %d", len(got), stderrTailBytes)
	}
	if !strings.HasSuffix(got, "new") {
		t.Errorf("retained stderr does not end with the last bytes")
	}
	if strings.Contains(got, "old") {
		t.Errorf("retained stderr still holds bytes dropped from the front")
	}
}

// TestRestartDaemonsRestartsPastAFailedKill pins that a stale daemon job that
// cannot be killed is a verbose diagnostic, and the daemon is still started.
func TestRestartDaemonsRestartsPastAFailedKill(t *testing.T) {
	seams := options.Seams{DaemonHealth: func(_ context.Context, _ msb.Sandbox, _ string) (string, int, error) {
		return `{"healthy":true,"version":"test"}`, 0, nil
	}}

	ui := termiotest.NewTestMock(t)
	stale := &msbtest.MockJob{KillErr: errors.New("kill failed")}
	command := opencodeProvider(t).DaemonCommand(false)
	sb := &msbtest.MockSandbox{
		Name_: "vm",
		Jobs: []msb.JobInfo{
			{ID: "stale", Command: append([]string{command.Binary}, command.Args...)},
		},
		GetJobFn:          func(context.Context, string) (msb.Job, error) { return stale, nil },
		ExecDetachedCalls: &[]msbtest.MockDetachedCall{},
	}

	restartDaemons(context.Background(), opencodeResolved(t), sb, false, &ui, seams)

	if !strings.Contains(strings.Join(ui.VerboseCalls, "\n"), "killing job") {
		t.Errorf("expected a kill-failure verbose message, got %v", ui.VerboseCalls)
	}
	assertDetachedHelperLaunch(t, *sb.ExecDetachedCalls, "agent-daemon", "opencode")
}

// TestRestartDaemonsWithoutADaemonDoesNothing pins that an agent without a
// serve daemon has nothing to restart.
func TestRestartDaemonsWithoutADaemonDoesNothing(t *testing.T) {
	ui := termiotest.NewTestMock(t)
	sb := &msbtest.MockSandbox{Name_: "vm", ShellCalls: &[]string{}, ExecDetachedCalls: &[]msbtest.MockDetachedCall{}}

	restartDaemons(context.Background(), agent.Resolve(&fakeAgent{}), sb, false, &ui, options.Seams{})

	if len(*sb.ShellCalls) != 0 || len(*sb.ExecDetachedCalls) != 0 {
		t.Errorf("shell calls %v, launches %+v; want none without a daemon", *sb.ShellCalls, *sb.ExecDetachedCalls)
	}
}

// TestRestartDaemonsWarnsWhenTheDaemonCannotStart pins that a failed restart
// is a warning, not a setup failure: the config is already provisioned.
func TestRestartDaemonsWarnsWhenTheDaemonCannotStart(t *testing.T) {
	seams := options.Seams{DaemonHealth: func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return "", 0, errors.New("health check failed")
		}
		return "", 0, errors.New("command failed")
	}}

	ui := termiotest.NewTestMock(t)
	sb := &msbtest.MockSandbox{
		Name_: "vm",
		ExecDetachedFn: func(context.Context, string, []string, msb.JobSpec) (msb.Job, error) {
			return nil, errors.New("start failed")
		},
	}

	restartDaemons(context.Background(), opencodeResolved(t), sb, false, &ui, seams)

	if !strings.Contains(strings.Join(ui.WarnCalls, "\n"), "daemon restart failed: ") {
		t.Errorf("expected a daemon-restart-failure warning, got %v", ui.WarnCalls)
	}
}

// TestRestartDaemonsRestartsServe pins that a restart launches the agent's
// serve daemon even while the running one is healthy.
func TestRestartDaemonsRestartsServe(t *testing.T) {
	seams := options.Seams{DaemonHealth: func(_ context.Context, _ msb.Sandbox, _ string) (string, int, error) {
		return `{"healthy":true,"version":"x"}`, 0, nil
	}}

	calls := &[]msbtest.MockDetachedCall{}
	sb := &msbtest.MockSandbox{Name_: "vm", ExecDetachedCalls: calls}
	ui := termiotest.NewTestMock(t)
	restartDaemons(
		context.Background(),
		opencodeResolved(t),
		sb,
		false,
		&ui,
		seams,
	)

	if len(*calls) != 1 {
		t.Errorf("expected one opencode serve launch, got %+v", *calls)
	}
	assertDetachedHelperLaunch(t, *calls, "agent-daemon", "opencode")
}

// TestHookInteractiveDefaultRequiresBothStdinAndStdout pins the production
// predicate: a hook may run interactively only when both the launcher's stdin
// and stdout are terminals, so a redirected or piped run stays non-interactive.
func TestHookInteractiveDefaultRequiresBothStdinAndStdout(t *testing.T) {
	orig := hookFDIsTerminal
	t.Cleanup(func() { hookFDIsTerminal = orig })

	for _, tc := range []struct {
		stdin, stdout, want bool
	}{
		{true, true, true},
		{true, false, false},
		{false, true, false},
		{false, false, false},
	} {
		hookFDIsTerminal = func(fd int) bool {
			if fd == int(os.Stdin.Fd()) {
				return tc.stdin
			}
			return tc.stdout
		}
		if got := hookInteractiveDefault(); got != tc.want {
			t.Errorf("hookInteractiveDefault(stdin=%v, stdout=%v) = %v, want %v",
				tc.stdin, tc.stdout, got, tc.want)
		}
	}
}

// TestHookInteractiveForUsesTheSeamOrTheDefault pins the resolution: the
// injected seam wins, and absent one the production default answers.
func TestHookInteractiveForUsesTheSeamOrTheDefault(t *testing.T) {
	if got := hookInteractiveFor(options.Seams{HookInteractive: func() bool { return true }}); !got {
		t.Error("hookInteractiveFor() = false, want the seam's true")
	}
	if got := hookInteractiveFor(options.Seams{HookInteractive: func() bool { return false }}); got {
		t.Error("hookInteractiveFor() = true, want the seam's false")
	}
}

// TestRunStartupHooksAttachesInteractively pins the interactive arm: the hook
// runs through the PTY attach as the right user with its HOME, and the streaming
// exec is not used.
func TestRunStartupHooksAttachesInteractively(t *testing.T) {
	for _, tc := range []struct {
		name     string
		hook     homeconfig.HookSpec
		wantUser string
		wantHome string
		wantCmd  string
		wantArgs []string
	}{
		{
			name:     "shebang runs directly",
			hook:     homeconfig.HookSpec{Target: "/home/dev/.foo.sh", Source: "x", HasShebang: true},
			wantUser: DefaultSandboxUser,
			wantHome: "/home/dev",
			wantCmd:  "/home/dev/.foo.sh",
		},
		{
			name:     "no shebang runs through /bin/sh",
			hook:     homeconfig.HookSpec{Target: "/home/dev/.vpn/connect.sh", Source: "x"},
			wantUser: DefaultSandboxUser,
			wantHome: "/home/dev",
			wantCmd:  "/bin/sh",
			wantArgs: []string{"/home/dev/.vpn/connect.sh"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sb := &msbtest.MockSandbox{Name_: "vm"}
			ui := termiotest.NewTestMock(t)

			runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{tc.hook}, &ui, true)

			if sb.AttachCmd != tc.wantCmd || !slices.Equal(sb.AttachArgs, tc.wantArgs) {
				t.Errorf("attached %q %v, want %q %v", sb.AttachCmd, sb.AttachArgs, tc.wantCmd, tc.wantArgs)
			}
			if sb.AttachUser != tc.wantUser || sb.AttachEnv["HOME"] != tc.wantHome {
				t.Errorf("attached as %q with HOME %q, want %q with %q",
					sb.AttachUser, sb.AttachEnv["HOME"], tc.wantUser, tc.wantHome)
			}
			if sb.ExecStreamCmd != "" {
				t.Errorf("interactive hook used the streaming exec %q", sb.ExecStreamCmd)
			}
		})
	}
}

// TestRunStartupHooksInteractiveAttachFailureWarnsAndContinues pins that an
// attach that cannot start is a warning and the remaining hooks still run, with
// no fallback to the streaming exec.
func TestRunStartupHooksInteractiveAttachFailureWarnsAndContinues(t *testing.T) {
	attaches := 0
	sb := &msbtest.MockSandbox{
		Name_: "vm",
		AttachFn: func(_ context.Context) (int, error) {
			attaches++
			if attaches == 1 {
				return 0, errors.New("no such device or address")
			}
			return 0, nil
		},
	}
	ui := termiotest.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.first.sh", Source: "x", HasShebang: true},
		{Target: "/home/dev/.second.sh", Source: "x", HasShebang: true},
	}, &ui, true)

	if len(ui.WarnCalls) != 1 {
		t.Fatalf("warnings = %v, want one for the failed attach", ui.WarnCalls)
	}
	for _, want := range []string{"/home/dev/.first.sh", "no such device or address"} {
		if !strings.Contains(ui.WarnCalls[0], want) {
			t.Errorf("warning %q missing %q", ui.WarnCalls[0], want)
		}
	}
	if sb.AttachCmd != "/home/dev/.second.sh" {
		t.Errorf("last attached hook = %q, want the second hook to run after the first failed", sb.AttachCmd)
	}
	if sb.ExecStreamCmd != "" {
		t.Errorf("a failed attach fell back to the streaming exec %q", sb.ExecStreamCmd)
	}
}

// TestRunStartupHooksInteractiveWarnsOnNonZeroExit pins the interactive failure
// warning: the exit code, with no stderr tail (the attach streams output itself).
func TestRunStartupHooksInteractiveWarnsOnNonZeroExit(t *testing.T) {
	attaches := 0
	sb := &msbtest.MockSandbox{
		Name_: "vm",
		AttachFn: func(_ context.Context) (int, error) {
			attaches++
			if attaches == 1 {
				return 2, nil
			}
			return 0, nil
		},
	}
	ui := termiotest.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.first.sh", Source: "x", HasShebang: true},
		{Target: "/home/dev/.second.sh", Source: "x", HasShebang: true},
	}, &ui, true)

	if len(ui.WarnCalls) != 1 || !strings.Contains(ui.WarnCalls[0], "exit 2") {
		t.Fatalf("warnings = %v, want one naming exit 2", ui.WarnCalls)
	}
	if sb.AttachCmd != "/home/dev/.second.sh" {
		t.Errorf("last attached hook = %q, want the second hook to run on", sb.AttachCmd)
	}
}

// TestRunStartupHooksInteractiveStaysSilentOnACancelledContext pins that a
// cancelled context during the attach is not reported as a hook failure.
func TestRunStartupHooksInteractiveStaysSilentOnACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sb := &msbtest.MockSandbox{
		Name_:    "vm",
		AttachFn: func(context.Context) (int, error) { return 0, context.Canceled },
	}
	ui := termiotest.NewTestMock(t)

	runStartupHooks(ctx, sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.hook.sh", Source: "x", HasShebang: true},
	}, &ui, true)

	if len(ui.WarnCalls) != 0 {
		t.Errorf("warnings = %v, want none for a cancelled attach", ui.WarnCalls)
	}
}

// TestRunStartupHooksNonInteractivePassesNoStdin pins that the non-interactive
// arm leaves stdin unconnected, so a prompting hook reads EOF instead of
// blocking on input no terminal can supply.
func TestRunStartupHooksNonInteractivePassesNoStdin(t *testing.T) {
	var got msb.ExecRequest
	sb := &msbtest.MockSandbox{
		Name_: "vm",
		ExecStreamFn: func(_ context.Context, _ string, _ []string, req msb.ExecRequest) (msb.StreamHandle, error) {
			got = req
			return msbtest.NewScriptedStreamHandle(msb.StreamEvent{Kind: msb.StreamEventExited}), nil
		},
	}
	ui := termiotest.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.hook.sh", Source: "x", HasShebang: true},
	}, &ui, false)

	if got.TTY || got.StdinPipe {
		t.Errorf("exec request = %+v, want no TTY and no stdin pipe", got)
	}
}

// terminalStdoutUI is a termiotest.Mock whose stdout is a pty, so a hook attach
// sees a real terminal and its reset reaches it.
type terminalStdoutUI struct {
	*termiotest.Mock

	stdout *os.File
}

func (u terminalStdoutUI) StdOut() io.Writer { return u.stdout }

// newTerminalStdoutUI returns a UI writing stdout to a pty and a function that
// closes the terminal side and returns everything written to it.
func newTerminalStdoutUI(t *testing.T) (terminalStdoutUI, func() string) {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	t.Cleanup(func() { _ = master.Close(); _ = slave.Close() })
	return terminalStdoutUI{Mock: &termiotest.Mock{}, stdout: slave}, func() string {
		_ = slave.Close()
		written, _ := io.ReadAll(master)
		return string(written)
	}
}

// TestRunStartupHooksInteractiveResetsTerminalOnAbnormalEnd pins that a hook
// attach that ends abnormally — a signal death or an attach error — clears the
// guest TUI's emulator modes before the next launcher message, and that a clean
// exit leaves the terminal untouched.
func TestRunStartupHooksInteractiveResetsTerminalOnAbnormalEnd(t *testing.T) {
	for _, tc := range []struct {
		name     string
		attachFn func(context.Context) (int, error)
		want     bool
	}{
		{"signal death resets", func(context.Context) (int, error) { return -1, nil }, true},
		{"attach error resets", func(context.Context) (int, error) { return 0, errors.New("boom") }, true},
		{"clean exit does not reset", func(context.Context) (int, error) { return 0, nil }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ui, written := newTerminalStdoutUI(t)
			sb := &msbtest.MockSandbox{Name_: "vm", AttachFn: tc.attachFn}

			runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
				{Target: "/home/dev/.hook.sh", Source: "x", HasShebang: true},
			}, ui, true)

			got := written()
			if contains := strings.Contains(got, termio.TerminalResetSequence); contains != tc.want {
				t.Errorf("terminal received %q, want reset=%v", got, tc.want)
			}
		})
	}
}
