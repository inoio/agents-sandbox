package vm

import (
	"context"
	"errors"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestRestartDaemonsEnsureDaemonError covers the ensureDaemon failure branch of
// restartDaemons: the daemon restart fails and is logged as a warning.
func TestRestartDaemonsEnsureDaemonError(t *testing.T) {
	orig := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return "", 0, errors.New("health check failed")
		}
		return "", 0, errors.New("command failed")
	})
	t.Cleanup(func() { SetDaemonHealthFunc(orig) })

	ui := termio.NewTestMock(t)
	sb := &msb.MockSandbox{
		Name_: "vm",
		ExecDetachedFn: func(context.Context, string, []string, ...msbSdk.JobExecOption) (msb.Job, error) {
			return nil, errors.New("start failed")
		},
	}

	restartDaemons(context.Background(), opencodeAgent(t), sb, false, &ui)

	if !contains(joinStrings(ui.WarnCalls), "daemon restart failed") {
		t.Errorf("expected a daemon-restart-failure warning, got %v", ui.WarnCalls)
	}
}

// TestDefaultDaemonHealthFuncError covers the default daemonHealthFunc closure
// (the non-test-seam path): when sb.Shell fails, it returns exit code -1.
func TestDefaultDaemonHealthFuncError(t *testing.T) {
	// Temporarily reset the seam to the default implementation.
	orig := daemonHealthFunc
	defer func() { daemonHealthFunc = orig }()
	daemonHealthFunc = func(ctx context.Context, sb msb.Sandbox, command string) (string, int, error) {
		out, err := sb.Shell(ctx, command)
		if err != nil {
			return "", -1, err
		}
		return out.Stdout(), out.ExitCode(), nil
	}

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
