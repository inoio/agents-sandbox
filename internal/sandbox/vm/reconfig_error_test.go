package vm

import (
	"context"
	"errors"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	"github.com/inoio/opencode-sandbox/internal/sandbox/volume"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// TestRestartDaemonsEnsureDaemonError covers the ensureDaemon failure branch of
// restartDaemons: the daemon restart fails and is logged as a warning.
func TestRestartDaemonsEnsureDaemonError(t *testing.T) {
	orig := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return "", 0, errors.New("health check failed")
		}
		return "", 0, errors.New("command failed")
	})
	defer SetDaemonShellFunc(orig)

	ui := termio.NewTestMock(t)
	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{Name_: "vm", FSValue_: fs}

	restartDaemons(context.Background(), opencodeAgent(t), sb, false, &ui)

	if !contains(joinStrings(ui.WarnCalls), "daemon restart failed") {
		t.Errorf("expected a daemon-restart-failure warning, got %v", ui.WarnCalls)
	}
}

// TestDefaultDaemonShellFuncError covers the default daemonShellFunc closure
// (the non-test-seam path): when sb.Shell fails, it returns exit code -1.
func TestDefaultDaemonShellFuncError(t *testing.T) {
	// Temporarily reset the seam to the default implementation.
	orig := daemonShellFunc
	defer func() { daemonShellFunc = orig }()
	daemonShellFunc = func(ctx context.Context, sb msb.Sandbox, command string) (string, int, error) {
		out, err := sb.Shell(ctx, command)
		if err != nil {
			return "", -1, err
		}
		return out.Stdout(), out.ExitCode(), nil
	}

	sb := &msb.MockSandbox{ShellErr: errors.New("shell failed")}
	stdout, code, err := daemonShellFunc(context.Background(), sb, "some-command")
	if err == nil {
		t.Fatal("expected error from default daemonShellFunc")
	}
	if code != -1 {
		t.Errorf("expected exit code -1 on error, got %d", code)
	}
	if stdout != "" {
		t.Errorf("expected empty stdout on error, got %q", stdout)
	}
}

// TestDecideReconfigDetachErrorVerbose covers the failed-detach verbose branch
// in decideReconfig: a connected sandbox whose Detach fails is logged (not
// fatal).
func TestDecideReconfigDetachErrorVerbose(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	sh := &msb.MockSandboxHandle{
		Cfg: &msbSdk.SandboxConfig{Image: "img:tag", CPUs: 4, MemoryMiB: 4096},
	}
	liveSb := &msb.MockSandbox{DetachErr: errors.New("detach failed")}
	sh.ConnectSb = liveSb
	mock := &msb.MockMsbClient{}
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return sh, nil
	}
	msb.WithMsbMock(t, mock)

	vm := volume.NewManager(&termio.Mock{})
	persisted := state.HomeState{HomeVolume: "vol", ImageDigest: "sha256:same"}

	ui := termio.NewTestMock(t)
	cfs, err := reprovision.LoadConfigFiles(configpaths.Get().UserOpencodeConfigDir(), &ui)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	_, _, _, err = decideReconfig(
		context.Background(),
		mock,
		vm,
		options.RunOptions{},
		"img:tag",
		"sha256:same",
		"vol",
		persisted,
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("decideReconfig: %v", err)
	}
	if !contains(joinStrings(ui.VerboseCalls), "failed to detach") {
		t.Errorf("expected a verbose message about the failed detach, got %v", ui.VerboseCalls)
	}
}
