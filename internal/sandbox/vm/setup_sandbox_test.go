package vm

import (
	"context"
	"errors"
	"slices"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestRunStartupHooksAttachError covers the startup-hook AttachWith failure
// branch in runStartupHooks: a failing hook is logged as a warning and the
// remaining hooks continue to run.
func TestRunStartupHooksAttachError(t *testing.T) {
	sb := &msb.MockSandbox{
		Name_:     "vm",
		AttachErr: errors.New("attach failed"),
	}
	ui := termio.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.vpn/connect.sh", Source: "x", HasShebang: true},
	}, &ui)

	if !contains(joinStrings(ui.WarnCalls), "startup hook") {
		t.Errorf("expected a startup-hook failure warning, got %v", ui.WarnCalls)
	}
}

// TestRunStartupHooksDefaultsToDevUser verifies that a startup hook without an
// explicit user runs as the default sandbox user (dev), and that a script
// without a shebang falls back to /bin/sh. This is the path most users hit,
// unlike the root-run case covered by the integration flow.
func TestRunStartupHooksDefaultsToDevUser(t *testing.T) {
	sb := &msb.MockSandbox{Name_: "vm"}
	ui := termio.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.vpn/connect.sh", Source: "x", Root: false},
	}, &ui)

	if sb.AttachUser != DefaultSandboxUser {
		t.Errorf("startup hook AttachWith user = %q, want default %q", sb.AttachUser, DefaultSandboxUser)
	}
	if home := sb.AttachEnv["HOME"]; home != "/home/dev" {
		t.Errorf("startup hook HOME = %q, want %q", home, "/home/dev")
	}
	if sb.AttachCmd != "/bin/sh" {
		t.Errorf("startup hook AttachWith cmd = %q, want fallback %q", sb.AttachCmd, "/bin/sh")
	}
	want := []string{"/home/dev/.vpn/connect.sh"}
	if !slices.Equal(sb.AttachArgs, want) {
		t.Errorf("startup hook AttachWith args = %v, want %v", sb.AttachArgs, want)
	}
}

// TestRunStartupHooksExecutesTargetDirectly verifies that a hook with a shebang
// is executed as the script path itself, so the VM kernel applies the shebang
// (issue #136).
func TestRunStartupHooksExecutesTargetDirectly(t *testing.T) {
	sb := &msb.MockSandbox{Name_: "vm"}
	ui := termio.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.foo.sh", Source: "x", Root: false, HasShebang: true},
	}, &ui)

	if sb.AttachCmd != "/home/dev/.foo.sh" {
		t.Errorf("startup hook AttachWith cmd = %q, want the script path", sb.AttachCmd)
	}
	if len(sb.AttachArgs) != 0 {
		t.Errorf("startup hook AttachWith args = %v, want none", sb.AttachArgs)
	}
}

// TestRunStartupHooksRunsAsRoot verifies that a hook with Root set attaches as
// the root user, with root's home as HOME.
func TestRunStartupHooksRunsAsRoot(t *testing.T) {
	sb := &msb.MockSandbox{Name_: "vm"}
	ui := termio.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.vpn/connect.sh", Source: "x", Root: true},
	}, &ui)

	if sb.AttachUser != "root" {
		t.Errorf("startup hook AttachWith user = %q, want %q", sb.AttachUser, "root")
	}
	if home := sb.AttachEnv["HOME"]; home != "/root" {
		t.Errorf("startup hook HOME = %q, want %q", home, "/root")
	}
}

// TestRestartDaemonsKillError covers the kill-stale-job failure branch of
// restartDaemons: the failure is reported as a verbose diagnostic and the
// daemon is still (re)started.
func TestRestartDaemonsKillError(t *testing.T) {
	seams := options.Seams{DaemonHealth: func(_ context.Context, _ msb.Sandbox, _ string) (string, int, error) {
		return `{"healthy":true,"version":"test"}`, 0, nil
	}}

	ui := termio.NewTestMock(t)
	stale := &msb.MockJob{ID_: "stale", KillErr: errors.New("kill failed")}
	command := opencodeProvider(t).DaemonCommand(false)
	sb := &msb.MockSandbox{
		Name_: "vm",
		Jobs: []msbSdk.JobInfo{
			{ID: "stale", Command: append([]string{command.Binary}, command.Args...)},
		},
		GetJobFn: func(context.Context, string) (msb.Job, error) { return stale, nil },
	}

	restartDaemons(context.Background(), opencodeResolved(t), sb, false, &ui, seams)

	if !contains(joinStrings(ui.VerboseCalls), "killing job") {
		t.Errorf("expected a kill-failure verbose message, got %v", ui.VerboseCalls)
	}
}

// TestRestartDaemonsNoProvider covers the early-return branch of
// restartDaemons for an agent that is not a DaemonProvider: it must no-op.
func TestRestartDaemonsNoProvider(t *testing.T) {
	ui := termio.NewTestMock(t)
	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{Name_: "vm", FSValue_: fs, ShellCalls: &[]string{}}

	restartDaemons(context.Background(), agent.Resolve(&fakeAgent{}), sb, false, &ui, options.Seams{})

	if len(*sb.ShellCalls) != 0 {
		t.Errorf("expected no shell calls for an agent without a DaemonProvider, got %v", *sb.ShellCalls)
	}
}

// TestRestartDaemonsEnsureDaemonError covers the ensureDaemon failure branch of
// restartDaemons: the daemon restart fails and is logged as a warning.
func TestRestartDaemonsEnsureDaemonError(t *testing.T) {
	seams := options.Seams{DaemonHealth: func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return "", 0, errors.New("health check failed")
		}
		return "", 0, errors.New("command failed")
	}}

	ui := termio.NewTestMock(t)
	sb := &msb.MockSandbox{
		Name_: "vm",
		ExecDetachedFn: func(context.Context, string, []string, ...msbSdk.JobExecOption) (msb.Job, error) {
			return nil, errors.New("start failed")
		},
	}

	restartDaemons(context.Background(), opencodeResolved(t), sb, false, &ui, seams)

	if !contains(joinStrings(ui.WarnCalls), "daemon restart failed") {
		t.Errorf("expected a daemon-restart-failure warning, got %v", ui.WarnCalls)
	}
}

func TestRestartDaemonsRestartsServe(t *testing.T) {
	seams := options.Seams{DaemonHealth: func(_ context.Context, _ msb.Sandbox, _ string) (string, int, error) {
		return `{"healthy":true,"version":"x"}`, 0, nil
	}}

	calls := &[]msb.MockDetachedCall{}
	sb := &msb.MockSandbox{Name_: "vm", ExecDetachedCalls: calls}
	ui := termio.NewTestMock(t)
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
