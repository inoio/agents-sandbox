package opener

import (
	"errors"
	"io/fs"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

// stubHostCommand replaces execCommand with a shell snippet and records the argv
// the opener asked for.
func stubHostCommand(t *testing.T, script string) *[]string {
	t.Helper()
	var recorded []string
	original := execCommand
	execCommand = func(name string, arguments ...string) *exec.Cmd {
		recorded = append([]string{name}, arguments...)
		return exec.Command("sh", "-c", script)
	}
	t.Cleanup(func() { execCommand = original })
	return &recorded
}

func TestHostOpenCommandPerPlatform(t *testing.T) {
	if got := hostOpenCommand("darwin"); got != "open" {
		t.Fatalf("darwin: got %q", got)
	}
	if got := hostOpenCommand("linux"); got != "xdg-open" {
		t.Fatalf("linux: got %q", got)
	}
}

func TestOpenOnHostPassesTargetAsSingleArgument(t *testing.T) {
	recorded := stubHostCommand(t, "exit 0")
	target := "https://example.com/?a=1&b=$(id)"
	if err := OpenOnHost(target); err != nil {
		t.Fatalf("OpenOnHost: %v", err)
	}
	if len(*recorded) != 2 || (*recorded)[1] != target {
		t.Fatalf("argv = %q, want [<opener> %q]", *recorded, target)
	}
	if !slices.Contains([]string{"open", "xdg-open"}, (*recorded)[0]) {
		t.Fatalf("unexpected opener %q", (*recorded)[0])
	}
}

func TestOpenOnHostReportsHandlerFailure(t *testing.T) {
	stubHostCommand(t, "exit 4")
	err := OpenOnHost("https://example.com")
	if err == nil || !strings.Contains(err.Error(), "exit status 4") {
		t.Fatalf("err = %v, want exit status 4", err)
	}
	if _, ok := errors.AsType[*exec.ExitError](err); !ok {
		t.Fatalf("err = %v, want it to carry the *exec.ExitError", err)
	}
}

func TestOpenOnHostReportsStartFailure(t *testing.T) {
	original := execCommand
	execCommand = func(string, ...string) *exec.Cmd { return exec.Command("/nonexistent/opener") }
	t.Cleanup(func() { execCommand = original })
	err := OpenOnHost("https://example.com")
	if err == nil || !strings.Contains(err.Error(), "start ") {
		t.Fatalf("err = %v, want a start error", err)
	}
	if _, ok := errors.AsType[*fs.PathError](err); !ok {
		t.Fatalf("err = %v, want it to carry the *fs.PathError", err)
	}
}

// TestInstallFailFastWiresExecCommand covers InstallFailFast replacing the
// package's exec seam with the panic, so a test that forgets to stub it fails
// loud instead of opening a browser.
func TestInstallFailFastWiresExecCommand(t *testing.T) {
	original := execCommand
	t.Cleanup(func() { execCommand = original })
	execCommand = func(string, ...string) *exec.Cmd { return exec.Command("true") }

	InstallFailFast()

	defer func() {
		if recover() == nil {
			t.Fatal("InstallFailFast did not wire the fail-fast command")
		}
	}()
	_ = execCommand("xdg-open", "https://example.com")
}

func TestOpenOnHostTreatsLongRunningHandlerAsSuccess(t *testing.T) {
	stubHostCommand(t, "sleep 5")
	originalSettle := hostOpenSettleTime
	hostOpenSettleTime = 50 * time.Millisecond
	t.Cleanup(func() { hostOpenSettleTime = originalSettle })
	if err := OpenOnHost("https://example.com"); err != nil {
		t.Fatalf("OpenOnHost: %v", err)
	}
}

func TestFailFastExecCommandPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	failFastExecCommand("xdg-open", "https://example.com")
}
