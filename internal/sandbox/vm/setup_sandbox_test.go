package vm

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// TestRunStartupHooksExecutesEachHook pins how a hook is run: as the default
// sandbox user with its home, or as root with /root when it asks for root; a
// script with a shebang is executed directly so the kernel applies it, one
// without is run through /bin/sh. Hooks run through the non-interactive exec
// path, so a non-TTY run still runs them.
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

			runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{tc.hook}, &ui)

			if sb.ExecUser != tc.wantUser || sb.ExecEnv["HOME"] != tc.wantHome {
				t.Errorf("executed as %q with HOME %q, want %q with %q",
					sb.ExecUser, sb.ExecEnv["HOME"], tc.wantUser, tc.wantHome)
			}
			if sb.ExecCmd != tc.wantCmd || !slices.Equal(sb.ExecArgs, tc.wantArgs) {
				t.Errorf("executed %q %v, want %q %v", sb.ExecCmd, sb.ExecArgs, tc.wantCmd, tc.wantArgs)
			}
		})
	}
}

// TestRunStartupHooksContinuesPastAFailingHook pins that a hook that cannot run
// is a warning and the remaining hooks still run.
func TestRunStartupHooksContinuesPastAFailingHook(t *testing.T) {
	sb := &msbtest.MockSandbox{Name_: "vm", ExecErr: errors.New("exec failed")}
	ui := termiotest.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.first.sh", Source: "x", HasShebang: true},
		{Target: "/home/dev/.second.sh", Source: "x", HasShebang: true},
	}, &ui)

	if sb.ExecCmd != "/home/dev/.second.sh" {
		t.Errorf("last executed hook = %q, want the second hook to run after the first failed", sb.ExecCmd)
	}
	if len(ui.WarnCalls) != 2 || !strings.Contains(ui.WarnCalls[0], "startup hook /home/dev/.first.sh failed") {
		t.Errorf("warnings = %v, want one per failed hook", ui.WarnCalls)
	}
}

// TestRunStartupHooksWarnsWhenAHookExitsNonZero pins that a hook that runs but
// exits non-zero is reported with its target and the failure reason (the exit
// code and stderr), and does not stop the remaining hooks.
func TestRunStartupHooksWarnsWhenAHookExitsNonZero(t *testing.T) {
	sb := &msbtest.MockSandbox{
		Name_: "vm",
		ExecOut: map[string]msb.ShellResult{
			"/bin/sh /home/dev/.first.sh": msbtest.NewTestResult(false, 2, "", "ca import failed", nil),
		},
	}
	ui := termiotest.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.first.sh", Source: "x"},
		{Target: "/home/dev/.second.sh", Source: "x"},
	}, &ui)

	if sb.ExecCmd != "/bin/sh" || !slices.Equal(sb.ExecArgs, []string{"/home/dev/.second.sh"}) {
		t.Errorf("last executed hook = %q %v, want the second hook to run after the first failed",
			sb.ExecCmd, sb.ExecArgs)
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
