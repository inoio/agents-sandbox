package harness

import (
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/creack/pty"
)

// mustStartUnderPTY starts argv under a pseudo-terminal, failing the test if it
// cannot start and closing the session in cleanup.
func mustStartUnderPTY(t *testing.T, dir string, env []string, argv ...string) *ptySession {
	t.Helper()

	ses, err := startUnderPTY(dir, env, argv)
	if err != nil {
		t.Fatalf("start %v under pty: %v", argv, err)
	}
	t.Cleanup(ses.close)
	return ses
}

func TestPTYRoundTrip(t *testing.T) {
	// Enter the alternate screen, then wait for one byte before exiting with 0.
	ses := mustStartUnderPTY(t, "", nil, "/bin/sh", "-c", `printf '\033[?1049h'; read x; exit 0`)
	defer ses.close()

	if err := ses.waitForTUI(5 * time.Second); err != nil {
		t.Fatalf("waitForTUI: %v", err)
	}
	if err := ses.write([]byte("\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	code, err := ses.waitExit(5 * time.Second)
	if err != nil {
		t.Fatalf("waitExit: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

func TestPTYWaitExitReportsNonZeroCode(t *testing.T) {
	ses := mustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "exit 3")
	defer ses.close()

	code, err := ses.waitExit(5 * time.Second)
	if err != nil {
		t.Fatalf("waitExit: %v", err)
	}
	if code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
}

func TestPTYWaitExitTimesOutWhileProcessRuns(t *testing.T) {
	ses := mustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "sleep 30")
	defer ses.close()

	if _, err := ses.waitExit(200 * time.Millisecond); err == nil {
		t.Fatal("waitExit = nil error, want a timeout error")
	}
}

func TestPTYWaitForTUIFailsWhenProcessExitsWithoutTUI(t *testing.T) {
	ses := mustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "echo launcher output; exit 0")
	defer ses.close()

	err := ses.waitForTUI(5 * time.Second)
	if err == nil {
		t.Fatal("waitForTUI = nil, want an error for a process that exited without a TUI")
	}
	if !strings.Contains(err.Error(), "no full-screen TUI") || !strings.Contains(err.Error(), "launcher output") {
		t.Errorf("waitForTUI error %q, want it to name the missing TUI and the output seen", err)
	}
}

func TestPTYWaitForTUITimesOutWhileProcessRuns(t *testing.T) {
	ses := mustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "sleep 30")
	defer ses.close()

	err := ses.waitForTUI(200 * time.Millisecond)
	if err == nil {
		t.Fatal("waitForTUI = nil, want a timeout error")
	}
	if !strings.Contains(err.Error(), "no full-screen TUI within") {
		t.Errorf("waitForTUI error %q, want it to name the timeout", err)
	}
}

// TestPTYWaitForMarkerReturnsOnceTheMarkerAppears pins the readiness signal for
// a normal-buffer harness that prints a stable startup line (hermes' banner):
// the wait ends when that line appears, not after a fixed settle.
func TestPTYWaitForMarkerReturnsOnceTheMarkerAppears(t *testing.T) {
	ses := mustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "printf 'Hermes Agent v1.2.3'; sleep 30")
	defer ses.close()

	start := time.Now()
	if err := ses.waitForMarker("Hermes Agent v", 5*time.Second); err != nil {
		t.Fatalf("waitForMarker: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("waitForMarker took %v, want it to return as soon as the marker appeared", elapsed)
	}
}

// TestMarkerTextCollapsesWhitespaceAndANSI pins the marker normalization: escape
// sequences and every whitespace character go, so a marker matches output whose
// spaces were rendered as cursor moves.
func TestMarkerTextCollapsesWhitespaceAndANSI(t *testing.T) {
	got := markerText("Welcome\033[1Cto\033[1CClaude\033[1CCode\n")
	if want := "WelcometoClaudeCode"; got != want {
		t.Errorf("markerText = %q, want %q", got, want)
	}
}

// TestPTYWaitForMarkerIgnoresRenderingArtifacts pins that the marker match
// tolerates a diff-painting TUI's output: escape sequences interleaved in the
// text and spaces rendered as cursor moves (claude-code's Ink), so a
// spaces-in marker matches though the line arrives space-less.
func TestPTYWaitForMarkerIgnoresRenderingArtifacts(t *testing.T) {
	ses := mustStartUnderPTY(t, "", nil, "/bin/sh", "-c",
		`printf 'Wel\033[0mcome\033[1Cto\033[1CClaude\033[1CCode'; sleep 30`)
	defer ses.close()

	if err := ses.waitForMarker("Welcome to Claude Code", 5*time.Second); err != nil {
		t.Fatalf("waitForMarker with rendering artifacts: %v", err)
	}
}

// TestPTYWaitForMarkerTimesOutWhileProcessRuns pins the timeout report: the
// error names the missing marker and the output seen, not the alternate screen.
func TestPTYWaitForMarkerTimesOutWhileProcessRuns(t *testing.T) {
	ses := mustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "sleep 30")
	defer ses.close()

	err := ses.waitForMarker("never-seen", 200*time.Millisecond)
	if err == nil {
		t.Fatal("waitForMarker = nil, want a timeout error")
	}
	if !strings.Contains(err.Error(), "readiness marker") || !strings.Contains(err.Error(), "never-seen") {
		t.Errorf("waitForMarker error %q, want it to name the missing readiness marker", err)
	}
}

// TestPTYWaitForMarkerReportsChildExit pins that a child stopping before its
// marker fails the wait at once, naming its exit and the output seen.
func TestPTYWaitForMarkerReportsChildExit(t *testing.T) {
	ses := mustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "echo launcher output; exit 3")
	defer ses.close()

	start := time.Now()
	err := ses.waitForMarker("never-seen", 30*time.Second)
	if err == nil {
		t.Fatal("waitForMarker = nil, want an error for a child that exited before its marker")
	}
	if !strings.Contains(err.Error(), "exit code 3") || !strings.Contains(err.Error(), "launcher output") {
		t.Errorf("waitForMarker error %q, want it to name the exit and the output seen", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("waitForMarker took %v, want an immediate report of the exit", elapsed)
	}
}

// TestPTYWaitSettledReturnsOnceTheChildOutlastsTheSettle pins the readiness
// signal for a harness whose TUI keeps the terminal's normal buffer: it is the
// settle, because no marker announces that such a harness has drawn its UI.
func TestPTYWaitSettledReturnsOnceTheChildOutlastsTheSettle(t *testing.T) {
	ses := mustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "sleep 30")
	defer ses.close()

	if err := ses.waitSettled(200 * time.Millisecond); err != nil {
		t.Fatalf("waitSettled: %v", err)
	}
}

// TestPTYWaitSettledReportsChildExit pins that a child stopping during the
// settle fails the readiness wait at once, naming its exit and the output seen,
// rather than waiting out the settle.
func TestPTYWaitSettledReportsChildExit(t *testing.T) {
	ses := mustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "echo launcher output; exit 3")
	defer ses.close()

	start := time.Now()
	err := ses.waitSettled(30 * time.Second)
	if err == nil {
		t.Fatal("waitSettled = nil, want an error for a child that exited during the settle")
	}
	if !strings.Contains(err.Error(), "exit code 3") || !strings.Contains(err.Error(), "launcher output") {
		t.Errorf("waitSettled error %q, want it to name the exit and the output seen", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("waitSettled took %v, want an immediate report of the exit", elapsed)
	}
}

// TestPTYWaitForTUIRejectsExitedTUI pins that entering the alternate screen is
// not accepted once the child has exited: a client that draws its TUI and exits
// on its own never handled the quit keys, so its exit must not pass as a clean
// quit.
func TestPTYWaitForTUIRejectsExitedTUI(t *testing.T) {
	// Enter the alternate screen, then exit without waiting for any input.
	ses := mustStartUnderPTY(t, "", nil, "/bin/sh", "-c", `printf '\033[?1049h'; exit 0`)
	defer ses.close()

	if code, err := ses.waitExit(5 * time.Second); err != nil {
		t.Fatalf("waitExit: %v", err)
	} else if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if err := ses.waitForTUI(5 * time.Second); err == nil {
		t.Fatal("waitForTUI = nil, want an error for a child that exited after entering the alternate screen")
	}
}

// TestPTYCloseKillsProcessGroup pins that close kills a still-running child
// (and its process group) and tolerates a second call.
func TestPTYCloseKillsProcessGroup(t *testing.T) {
	ses := mustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "sleep 30")
	ses.close()
	ses.close()

	code, err := ses.waitExit(5 * time.Second)
	if err != nil {
		t.Fatalf("waitExit after close: %v", err)
	}
	if code != -1 {
		t.Errorf("exit code = %d, want -1 for a process killed by a signal", code)
	}
}

// TestKillProcessGroup pins the process-group kill's outcomes: a command with
// no started process is already done, an "already gone" signal is treated as
// done, and a signal that fails for a reason other than "already gone" is
// reported rather than swallowed.
func TestKillProcessGroup(t *testing.T) {
	if err := killProcessGroup(&exec.Cmd{}); !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("killProcessGroup(command without a process) = %v, want os.ErrProcessDone", err)
	}

	process := &exec.Cmd{Process: &os.Process{Pid: 1}}
	if err := killProcessGroupWith(
		func(int, syscall.Signal) error { return syscall.ESRCH },
		process,
	); !errors.Is(
		err,
		os.ErrProcessDone,
	) {
		t.Errorf("killProcessGroupWith(already gone) = %v, want os.ErrProcessDone", err)
	}

	signalFailure := errors.New("operation not permitted")
	if err := killProcessGroupWith(
		func(int, syscall.Signal) error { return signalFailure },
		process,
	); !errors.Is(
		err,
		signalFailure,
	) {
		t.Errorf("killProcessGroupWith(failing signal) = %v, want %v", err, signalFailure)
	}
}

// TestPTYDrainOutputStopsAfterTheGrace pins that the drain before an
// exited-without-TUI report does not block forever when the read loop stalls: a
// grandchild still holding the pty keeps the master open, so the loop never
// sees EOF and the grace must end the wait.
func TestPTYDrainOutputStopsAfterTheGrace(t *testing.T) {
	ses := &ptySession{readDone: make(chan struct{})}
	const grace = 10 * time.Millisecond

	start := time.Now()
	ses.drainOutput(grace)
	if elapsed := time.Since(start); elapsed < grace {
		t.Errorf("drainOutput returned after %s, want it to wait out the %s grace", elapsed, grace)
	}
}

// TestPTYCloseAfterExit pins that close takes the reaped path once the child
// has already been waited for (no group signal, still idempotent).
func TestPTYCloseAfterExit(t *testing.T) {
	ses := mustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "exit 0")
	if code, err := ses.waitExit(5 * time.Second); err != nil {
		t.Fatalf("waitExit: %v", err)
	} else if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	ses.close()
	ses.close()
}

// TestExitErrorCode pins the shared *exec.ExitError unwrapping used by the exit
// status, wait, and tail helpers.
func TestExitErrorCode(t *testing.T) {
	ses := mustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "exit 7")
	defer ses.close()
	if _, err := ses.waitExit(5 * time.Second); err != nil {
		t.Fatalf("waitExit: %v", err)
	}

	code, ok := exitErrorCode(ses.waitErr)
	if !ok || code != 7 {
		t.Errorf("exitErrorCode(exit 7 wait) = (%d, %t), want (7, true)", code, ok)
	}
	if _, ok := exitErrorCode(nil); ok {
		t.Error("exitErrorCode(nil) reported an exit error, want false")
	}
	if got := ses.exitStatus(); got != "exit code 7" {
		t.Errorf("exitStatus = %q, want %q", got, "exit code 7")
	}
}

// TestExitStatusAndCodeForAFailedWait pins the mappings for a wait that failed
// outright rather than reporting a process exit: the status string, the exit
// code, and waitExit all surface the error instead of inventing a code.
func TestExitStatusAndCodeForAFailedWait(t *testing.T) {
	waitFailure := errors.New("wait failed")
	waitDone := make(chan struct{})
	close(waitDone)
	ses := &ptySession{waitErr: waitFailure, waitDone: waitDone}

	if got := ses.exitStatus(); got != waitFailure.Error() {
		t.Errorf("exitStatus = %q, want %q", got, waitFailure.Error())
	}
	if got := exitCode(waitFailure); got != -1 {
		t.Errorf("exitCode(failed wait) = %d, want -1", got)
	}
	if _, err := ses.waitExit(time.Second); err == nil {
		t.Error("waitExit = nil error, want an error for a failed wait")
	}
}

// TestStartUnderPTYReportsAStartFailure pins that a child that cannot be
// started yields an error instead of a half-built session.
func TestStartUnderPTYReportsAStartFailure(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-binary")
	if _, err := startUnderPTY("", nil, []string{missing}); err == nil {
		t.Fatal("startUnderPTY = nil error, want a start failure for a missing binary")
	}
}

// TestTerminalSize pins how the child's terminal is sized: a real terminal's
// size is honoured and clamped to the pty field width, while a file that is not
// a terminal falls back to the generous default.
func TestTerminalSize(t *testing.T) {
	for _, test := range []struct {
		name  string
		value int
		want  uint16
	}{
		{name: "negative", value: -1, want: 0},
		{name: "too wide", value: math.MaxUint16 + 1, want: math.MaxUint16},
		{name: "in range", value: 80, want: 80},
	} {
		if got := terminalDimension(test.value); got != test.want {
			t.Errorf("terminalDimension(%d) = %d, want %d", test.value, got, test.want)
		}
	}

	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("open pty: %v", err)
	}
	defer master.Close()
	defer slave.Close()
	if err := pty.Setsize(master, &pty.Winsize{Rows: 24, Cols: 100}); err != nil {
		t.Fatalf("set pty size: %v", err)
	}
	if got := terminalSize(slave); got.Rows != 24 || got.Cols != 100 {
		t.Errorf("terminalSize(terminal) = %dx%d, want 24x100", got.Rows, got.Cols)
	}

	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatalf("open pipe: %v", err)
	}
	defer readEnd.Close()
	defer writeEnd.Close()
	got := terminalSize(readEnd)
	if got.Rows != fallbackTerminalRows || got.Cols != fallbackTerminalCols {
		t.Errorf("terminalSize(non-terminal) = %dx%d, want %dx%d",
			got.Rows, got.Cols, fallbackTerminalRows, fallbackTerminalCols)
	}
}

// TestPTYOutputTailCutsOnRuneBoundary pins that outputTail never splits a
// multi-byte rune at the byte-offset cut used to trim the tail.
func TestPTYOutputTailCutsOnRuneBoundary(t *testing.T) {
	ses := &ptySession{}
	// Three-byte runes sized so the cut lands one byte into a rune: a naive
	// byte slice would start mid-rune and yield invalid UTF-8.
	ses.buffer.WriteString(strings.Repeat("€", ptyOutputTailLimit/3+1))

	quoted := ses.outputTail()
	text, err := strconv.Unquote(quoted)
	if err != nil {
		t.Fatalf("Unquote(%s): %v", quoted, err)
	}
	if !utf8.ValidString(text) {
		t.Errorf("outputTail = %q, want valid UTF-8", text)
	}
}

// TestPTYHonoursWorkingDirectoryAndEnv pins that a non-empty dir and a
// non-nil env reach the child (and that the env replaces the inherited one).
func TestPTYHonoursWorkingDirectoryAndEnv(t *testing.T) {
	dir := t.TempDir()
	env := []string{"PATH=/usr/bin:/bin", "HARNESS_PTY_TEST=marker"}
	// Unset PWD so the shell's pwd builtin asks the kernel for the real
	// working directory instead of echoing the inherited one.
	ses := mustStartUnderPTY(t, dir, env, "/bin/sh", "-c",
		`unset PWD; printf '\033[?1049h'; read x; pwd; echo "$HARNESS_PTY_TEST"; exit 0`)
	defer ses.close()

	if err := ses.waitForTUI(5 * time.Second); err != nil {
		t.Fatalf("waitForTUI: %v", err)
	}
	if err := ses.write([]byte("\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ses.waitExit(5 * time.Second); err != nil {
		t.Fatalf("waitExit: %v", err)
	}

	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	for _, want := range []string{resolved, "marker"} {
		deadline := time.Now().Add(time.Second)
		for !strings.Contains(string(ses.output()), want) {
			if time.Now().After(deadline) {
				t.Fatalf("output %q does not contain %q", ses.output(), want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}
