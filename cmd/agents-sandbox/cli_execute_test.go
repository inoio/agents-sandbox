package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	sandboxmsb "github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestExecuteVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}

	if err := execute([]string{"version"}, ui); err != nil {
		t.Fatalf("execute version: %v", err)
	}
	if len(ui.OutCalls) == 0 {
		t.Fatal("expected version output")
	}
	if !strings.Contains(ui.OutCalls[0], "dev") {
		t.Errorf("expected dev version in output, got %q", ui.OutCalls[0])
	}
}

func TestExecuteTree(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}

	if err := execute([]string{"tree"}, ui); err != nil {
		t.Fatalf("execute tree: %v", err)
	}
	if len(ui.InfoCalls) == 0 {
		t.Fatal("expected tree output")
	}
	if ui.InfoCalls[0] != "agents-sandbox" {
		t.Errorf("expected root name as first tree line, got %q", ui.InfoCalls[0])
	}
}

// TestExecuteRootPreRunResolverError exercises the PersistentPreRunE error
// branch in buildRootCmd: an invalid resolver value (cpus > 255) makes
// launcherconfig.NewResolver fail, so execute must surface that error.
func TestExecuteRootPreRunResolverError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("OPENCODE_SANDBOX_CPUS", "999")
	ui := &termio.Mock{}

	if err := execute([]string{"version"}, ui); err == nil {
		t.Fatal("expected an error from an invalid resolver config")
	}
}

func TestAutoPruneOutToVerboseRedirect(t *testing.T) {
	ui := &termio.Mock{}
	redirect := &autoPruneOutToVerboseRedirect{UI: ui}

	redirect.Out("plain message")
	redirect.Outf("formatted %d", 42)

	want := []string{"plain message", "formatted 42"}
	if len(ui.VerboseCalls) != len(want) {
		t.Fatalf("VerboseCalls = %v; want %v", ui.VerboseCalls, want)
	}
	for i := range want {
		if ui.VerboseCalls[i] != want[i] {
			t.Errorf("VerboseCalls[%d] = %q; want %q", i, ui.VerboseCalls[i], want[i])
		}
	}
}

// TestMainProcess exercises the real main() entrypoint in a subprocess so that
// os.Exit is not invoked in the test process itself. It covers both the
// successful version path (exit 0) and the error path (exit 1).
func TestMainProcess(t *testing.T) {
	t.Run("version exits zero with output", func(t *testing.T) {
		out, exitCode := runMainHelper(t, []string{"version"})
		if exitCode != 0 {
			t.Fatalf("exit code = %d, want 0; output: %s", exitCode, out)
		}
		if !strings.Contains(out, "agents-sandbox") {
			t.Errorf("expected version output, got %q", out)
		}
	})

	t.Run("error exits non-zero", func(t *testing.T) {
		out, exitCode := runMainHelper(t, []string{"run", "--agent", "bogus"})
		if exitCode != 1 {
			t.Fatalf("exit code = %d, want 1; output: %s", exitCode, out)
		}
	})

	t.Run("sandbox exit error propagates code", func(t *testing.T) {
		cmd := exec.Command(os.Args[0], "-test.run=TestMainHelperProcessExitError")
		cmd.Env = append(os.Environ(), "GO_WANT_MAIN_HELPER=1")
		out, err := cmd.CombinedOutput()
		exitCode := 0
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exitCode = exitErr.ExitCode()
			} else {
				t.Fatalf("running helper: %v", err)
			}
		}
		if exitCode != 5 {
			t.Errorf("exit code = %d, want 5; output: %s", exitCode, out)
		}
	})
}

// runMainHelper re-executes the test binary with GO_WANT_MAIN_HELPER=1 so the
// helper test bodies below run main() in a clean process.
func runMainHelper(t *testing.T, args []string) (string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestMainHelperProcess")
	cmd.Env = append(os.Environ(),
		"GO_WANT_MAIN_HELPER=1",
		"GO_MAIN_ARGS="+strings.Join(args, ","),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return string(out), exitErr.ExitCode()
		}
		t.Fatalf("running helper: %v", err)
	}
	return string(out), 0
}

func TestMainHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MAIN_HELPER") != "1" {
		t.Skip("helper process only")
	}
	args := strings.Split(os.Getenv("GO_MAIN_ARGS"), ",")
	configpaths.WithMockConfigPaths(t)
	os.Args = append([]string{"agents-sandbox"}, args...)
	main()
	os.Exit(0)
}

// TestMainHelperProcessExitError runs main() against a mock sandbox that exits
// with code 5, so main's sandbox.ExitError branch (os.Exit with the exit code)
// is exercised in a subprocess.
func TestMainHelperProcessExitError(t *testing.T) {
	if os.Getenv("GO_WANT_MAIN_HELPER") != "1" {
		t.Skip("helper process only")
	}
	initTestRepo(t)
	mock := &sandboxmsb.MockMsbClient{}
	setupRunMocks(t, mock, &sandboxmsb.MockSandbox{AttachCode: 5}, "run")
	os.Args = []string{"agents-sandbox", "run"}
	main()
	os.Exit(0)
}
