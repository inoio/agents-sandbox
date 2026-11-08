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
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// TestRunStartupHooksAttachesEachHook pins how a hook is run: as the default
// sandbox user with its home, or as root with /root when it asks for root; a
// script with a shebang is executed directly so the kernel applies it, one
// without is run through /bin/sh.
func TestRunStartupHooksAttachesEachHook(t *testing.T) {
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
			sb := &msb.MockSandbox{Name_: "vm"}
			ui := termiotest.NewTestMock(t)

			runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{tc.hook}, &ui)

			if sb.AttachUser != tc.wantUser || sb.AttachEnv["HOME"] != tc.wantHome {
				t.Errorf("attached as %q with HOME %q, want %q with %q",
					sb.AttachUser, sb.AttachEnv["HOME"], tc.wantUser, tc.wantHome)
			}
			if sb.AttachCmd != tc.wantCmd || !slices.Equal(sb.AttachArgs, tc.wantArgs) {
				t.Errorf("attached %q %v, want %q %v", sb.AttachCmd, sb.AttachArgs, tc.wantCmd, tc.wantArgs)
			}
		})
	}
}

// TestRunStartupHooksContinuesPastAFailingHook pins that a failing hook is a
// warning and the remaining hooks still run.
func TestRunStartupHooksContinuesPastAFailingHook(t *testing.T) {
	sb := &msb.MockSandbox{Name_: "vm", AttachErr: errors.New("attach failed")}
	ui := termiotest.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.first.sh", Source: "x", HasShebang: true},
		{Target: "/home/dev/.second.sh", Source: "x", HasShebang: true},
	}, &ui)

	if sb.AttachCmd != "/home/dev/.second.sh" {
		t.Errorf("last attached hook = %q, want the second hook to run after the first failed", sb.AttachCmd)
	}
	if len(ui.WarnCalls) != 2 || !strings.Contains(ui.WarnCalls[0], "startup hook /home/dev/.first.sh failed") {
		t.Errorf("warnings = %v, want one per failed hook", ui.WarnCalls)
	}
}

// TestRestartDaemonsRestartsPastAFailedKill pins that a stale daemon job that
// cannot be killed is a verbose diagnostic, and the daemon is still started.
func TestRestartDaemonsRestartsPastAFailedKill(t *testing.T) {
	seams := options.Seams{DaemonHealth: func(_ context.Context, _ msb.Sandbox, _ string) (string, int, error) {
		return `{"healthy":true,"version":"test"}`, 0, nil
	}}

	ui := termiotest.NewTestMock(t)
	stale := &msb.MockJob{KillErr: errors.New("kill failed")}
	command := opencodeProvider(t).DaemonCommand(false)
	sb := &msb.MockSandbox{
		Name_: "vm",
		Jobs: []msb.JobInfo{
			{ID: "stale", Command: append([]string{command.Binary}, command.Args...)},
		},
		GetJobFn:          func(context.Context, string) (msb.Job, error) { return stale, nil },
		ExecDetachedCalls: &[]msb.MockDetachedCall{},
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
	sb := &msb.MockSandbox{Name_: "vm", ShellCalls: &[]string{}, ExecDetachedCalls: &[]msb.MockDetachedCall{}}

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
	sb := &msb.MockSandbox{
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

	calls := &[]msb.MockDetachedCall{}
	sb := &msb.MockSandbox{Name_: "vm", ExecDetachedCalls: calls}
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
