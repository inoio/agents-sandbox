package driver

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
	"golang.org/x/term"
)

// TestSessionInRawMode pins inRawMode against a real pty: a fresh pty is cooked,
// and making the slave raw is visible through the master — the signal the quit
// loop uses to stop tapping once the launcher's attach has restored the
// terminal to cooked mode. Reading the master works because a pty master and
// slave share one termios.
func TestSessionInRawMode(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	defer master.Close()
	defer slave.Close()

	ses := &Session{master: master}
	if ses.inRawMode() {
		t.Error("inRawMode = true for a fresh pty, want false")
	}

	state, err := term.MakeRaw(int(slave.Fd()))
	if err != nil {
		t.Fatalf("MakeRaw: %v", err)
	}
	if !ses.inRawMode() {
		t.Error("inRawMode = false after MakeRaw, want true")
	}

	if err := term.Restore(int(slave.Fd()), state); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if ses.inRawMode() {
		t.Error("inRawMode = true after Restore, want false")
	}
}

// TestTeardownStartedStopsOnAlternateScreenLeaveOrCookedPty pins the quit loop's
// stop policy. The host-cooked check (inRawMode) cannot see an alternate-screen
// harness restore its guest terminal earlier, which is when qwen-code's slow
// teardown gets tapped past; the alternate-screen-leave check is that guest-side
// signal, and it must not end the loop before the first tap.
func TestTeardownStartedStopsOnAlternateScreenLeaveOrCookedPty(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	defer master.Close()
	defer slave.Close()

	ses := &Session{master: master}
	// A cooked pty is already tearing down: stop from the first press.
	if !TeardownStarted(ses, 1) {
		t.Error("TeardownStarted(cooked pty, 1) = false, want true")
	}
	if !TeardownStarted(ses, 2) {
		t.Error("TeardownStarted(cooked pty, 2) = false, want true")
	}

	state, err := term.MakeRaw(int(slave.Fd()))
	if err != nil {
		t.Fatalf("MakeRaw: %v", err)
	}
	t.Cleanup(func() { _ = term.Restore(int(slave.Fd()), state) })

	// A raw pty with no leave is still listening: keep tapping.
	if TeardownStarted(ses, 1) {
		t.Error("TeardownStarted(raw pty, 1) = true, want false")
	}
	if TeardownStarted(ses, 2) {
		t.Error("TeardownStarted(raw pty, 2) = true, want false")
	}

	// A leave on the raw pty ends it from the second press...
	ses.buffer.WriteString(altScreenLeave)
	if !TeardownStarted(ses, 2) {
		t.Error("TeardownStarted(raw pty with leave, 2) = false, want true")
	}
	// ...but never before the loop has tapped once.
	beforeFirstPress := &Session{master: master}
	beforeFirstPress.buffer.WriteString(altScreenLeave)
	if TeardownStarted(beforeFirstPress, 1) {
		t.Error("TeardownStarted(leave, 1) = true, want false before the first tap")
	}
}

func TestPTYRoundTrip(t *testing.T) {
	// Enter the alternate screen, then wait for one byte before exiting with 0.
	ses := MustStartUnderPTY(t, "", nil, "/bin/sh", "-c", `printf '\033[?1049h'; read x; exit 0`)
	defer ses.Close()

	if err := ses.WaitForTUI(5 * time.Second); err != nil {
		t.Fatalf("WaitForTUI: %v", err)
	}
	if err := ses.Write([]byte("\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	code, err := ses.WaitExit(5 * time.Second)
	if err != nil {
		t.Fatalf("WaitExit: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

func TestPTYWaitExitReportsNonZeroCode(t *testing.T) {
	ses := MustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "exit 3")
	defer ses.Close()

	code, err := ses.WaitExit(5 * time.Second)
	if err != nil {
		t.Fatalf("WaitExit: %v", err)
	}
	if code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
}

func TestPTYWaitExitTimesOutWhileProcessRuns(t *testing.T) {
	ses := MustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "sleep 30")
	defer ses.Close()

	if _, err := ses.WaitExit(20 * time.Millisecond); err == nil {
		t.Fatal("WaitExit = nil error, want a timeout error")
	}
}

func TestPTYWaitForTUIFailsWhenProcessExitsWithoutTUI(t *testing.T) {
	ses := MustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "echo launcher output; exit 0")
	defer ses.Close()

	err := ses.WaitForTUI(5 * time.Second)
	if err == nil {
		t.Fatal("WaitForTUI = nil, want an error for a process that exited without a TUI")
	}
	if !strings.Contains(err.Error(), "no full-screen TUI") || !strings.Contains(err.Error(), "launcher output") {
		t.Errorf("WaitForTUI error %q, want it to name the missing TUI and the output seen", err)
	}
}

func TestPTYWaitForTUITimesOutWhileProcessRuns(t *testing.T) {
	ses := MustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "sleep 30")
	defer ses.Close()

	err := ses.WaitForTUI(50 * time.Millisecond)
	if err == nil {
		t.Fatal("WaitForTUI = nil, want a timeout error")
	}
	if !strings.Contains(err.Error(), "no full-screen TUI within") {
		t.Errorf("WaitForTUI error %q, want it to name the timeout", err)
	}
}

// TestPTYWaitForMarkerReturnsOnceTheMarkerAppears pins the readiness signal for
// a normal-buffer harness that prints a stable startup line (hermes' banner):
// the wait ends when that line appears, not after a fixed settle.
func TestPTYWaitForMarkerReturnsOnceTheMarkerAppears(t *testing.T) {
	ses := MustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "printf 'Hermes Agent v1.2.3'; sleep 30")
	defer ses.Close()

	start := time.Now()
	if err := ses.WaitForMarker("Hermes Agent v", 5*time.Second); err != nil {
		t.Fatalf("WaitForMarker: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("WaitForMarker took %v, want it to return as soon as the marker appeared", elapsed)
	}
}

// TestMarkerTextCollapsesWhitespaceAndANSI pins the marker normalization: escape
// sequences and every whitespace character go, so a marker matches output whose
// spaces were rendered as cursor moves.
func TestMarkerTextCollapsesWhitespaceAndANSI(t *testing.T) {
	got := MarkerText("Welcome\033[1Cto\033[1CClaude\033[1CCode\n")
	if want := "WelcometoClaudeCode"; got != want {
		t.Errorf("MarkerText = %q, want %q", got, want)
	}
}

// TestPTYWaitForMarkerIgnoresRenderingArtifacts pins that the marker match
// tolerates a diff-painting TUI's output: escape sequences interleaved in the
// text and spaces rendered as cursor moves (claude-code's Ink), so a
// spaces-in marker matches though the line arrives space-less.
func TestPTYWaitForMarkerIgnoresRenderingArtifacts(t *testing.T) {
	ses := MustStartUnderPTY(t, "", nil, "/bin/sh", "-c",
		`printf 'Wel\033[0mcome\033[1Cto\033[1CClaude\033[1CCode'; sleep 30`)
	defer ses.Close()

	if err := ses.WaitForMarker("Welcome to Claude Code", 5*time.Second); err != nil {
		t.Fatalf("WaitForMarker with rendering artifacts: %v", err)
	}
}

// TestPTYWaitForMarkerTimesOutWhileProcessRuns pins the timeout report: the
// error names the missing marker and the output seen, not the alternate screen.
func TestPTYWaitForMarkerTimesOutWhileProcessRuns(t *testing.T) {
	ses := MustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "sleep 30")
	defer ses.Close()

	err := ses.WaitForMarker("never-seen", 50*time.Millisecond)
	if err == nil {
		t.Fatal("WaitForMarker = nil, want a timeout error")
	}
	if !strings.Contains(err.Error(), "readiness marker") || !strings.Contains(err.Error(), "never-seen") {
		t.Errorf("WaitForMarker error %q, want it to name the missing readiness marker", err)
	}
}

// TestPTYWaitForMarkerReportsChildExit pins that a child stopping before its
// marker fails the wait at once, naming its exit and the output seen.
func TestPTYWaitForMarkerReportsChildExit(t *testing.T) {
	ses := MustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "echo launcher output; exit 3")
	defer ses.Close()

	start := time.Now()
	err := ses.WaitForMarker("never-seen", 30*time.Second)
	if err == nil {
		t.Fatal("WaitForMarker = nil, want an error for a child that exited before its marker")
	}
	if !strings.Contains(err.Error(), "exit code 3") || !strings.Contains(err.Error(), "launcher output") {
		t.Errorf("WaitForMarker error %q, want it to name the exit and the output seen", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("WaitForMarker took %v, want an immediate report of the exit", elapsed)
	}
}

// TestPTYWaitForMarkerRejectsAMarkerSeenOnlyAfterExit pins that a marker left in
// the buffer by a child that then exits is not readiness: the wait must report
// the exit instead of accepting output the child can no longer act on.
func TestPTYWaitForMarkerRejectsAMarkerSeenOnlyAfterExit(t *testing.T) {
	ses := MustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "printf 'Hermes Agent v'; exit 3")
	defer ses.Close()

	if _, err := ses.WaitExit(5 * time.Second); err != nil {
		t.Fatalf("WaitExit: %v", err)
	}
	waitForOutput(t, ses, "Hermes Agent v")

	if err := ses.WaitForMarker("Hermes Agent v", 5*time.Second); err == nil {
		t.Fatal("WaitForMarker = nil, want an error for a marker seen only after the child exited")
	}
}

// TestPTYWaitForMarkerDrainsBeforeReportingChildExit pins that the exit report
// gives the read loop its grace before quoting the output: a session whose
// output arrives only as the read loop finishes must still include it.
func TestPTYWaitForMarkerDrainsBeforeReportingChildExit(t *testing.T) {
	waitDone := make(chan struct{})
	close(waitDone)
	readDone := make(chan struct{})
	ses := &Session{reaped: true, waitDone: waitDone, readDone: readDone}
	ses.buffer.WriteString("output before exit")

	returned := make(chan error, 1)
	go func() { returned <- ses.WaitForMarker("late marker", 5*time.Second) }()

	// The correct code cannot report while the read loop is still open and the
	// drain grace has not elapsed; a mutant that skips the drain returns at once.
	select {
	case <-returned:
		t.Fatal("WaitForMarker returned before draining the read loop")
	case <-time.After(100 * time.Millisecond):
	}

	ses.mu.Lock()
	ses.buffer.WriteString(" late marker")
	ses.mu.Unlock()
	close(readDone)

	if err := <-returned; err == nil || !strings.Contains(err.Error(), "late marker") {
		t.Fatalf("WaitForMarker error = %v, want it to include the output drained before reporting", err)
	}
}

// waitForOutput polls the session's captured output until it contains want.
func waitForOutput(t *testing.T, ses *Session, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(string(ses.Output()), want) {
		if time.Now().After(deadline) {
			t.Fatalf("output %q never contained %q", ses.Output(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestPTYWaitSettledReturnsOnceTheChildOutlastsTheSettle pins the readiness
// signal for a harness whose TUI keeps the terminal's normal buffer: it is the
// settle, because no marker announces that such a harness has drawn its UI.
func TestPTYWaitSettledReturnsOnceTheChildOutlastsTheSettle(t *testing.T) {
	ses := MustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "sleep 30")
	defer ses.Close()

	if err := ses.WaitSettled(50 * time.Millisecond); err != nil {
		t.Fatalf("WaitSettled: %v", err)
	}
}

// TestPTYWaitSettledReportsChildExit pins that a child stopping during the
// settle fails the readiness wait at once, naming its exit and the output seen,
// rather than waiting out the settle.
func TestPTYWaitSettledReportsChildExit(t *testing.T) {
	ses := MustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "echo launcher output; exit 3")
	defer ses.Close()

	start := time.Now()
	err := ses.WaitSettled(30 * time.Second)
	if err == nil {
		t.Fatal("WaitSettled = nil, want an error for a child that exited during the settle")
	}
	if !strings.Contains(err.Error(), "exit code 3") || !strings.Contains(err.Error(), "launcher output") {
		t.Errorf("WaitSettled error %q, want it to name the exit and the output seen", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("WaitSettled took %v, want an immediate report of the exit", elapsed)
	}
}

// TestPTYWaitForTUIRejectsExitedTUI pins that entering the alternate screen is
// not accepted once the child has exited: a client that draws its TUI and exits
// on its own never handled the quit keys, so its exit must not pass as a clean
// quit.
func TestPTYWaitForTUIRejectsExitedTUI(t *testing.T) {
	// Enter the alternate screen, then exit without waiting for any input.
	ses := MustStartUnderPTY(t, "", nil, "/bin/sh", "-c", `printf '\033[?1049h'; exit 0`)
	defer ses.Close()

	if code, err := ses.WaitExit(5 * time.Second); err != nil {
		t.Fatalf("WaitExit: %v", err)
	} else if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if err := ses.WaitForTUI(5 * time.Second); err == nil {
		t.Fatal("WaitForTUI = nil, want an error for a child that exited after entering the alternate screen")
	}
}

// TestPTYCloseKillsProcessGroup pins that Close kills a still-running child
// (and its process group) and tolerates a second call.
func TestPTYCloseKillsProcessGroup(t *testing.T) {
	ses := MustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "sleep 30")
	ses.Close()
	ses.Close()

	code, err := ses.WaitExit(5 * time.Second)
	if err != nil {
		t.Fatalf("WaitExit after Close: %v", err)
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
	if err := KillProcessGroup(&exec.Cmd{}); !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("KillProcessGroup(command without a process) = %v, want os.ErrProcessDone", err)
	}

	process := &exec.Cmd{Process: &os.Process{Pid: 1}}
	if err := KillProcessGroupWith(
		func(int, syscall.Signal) error { return syscall.ESRCH },
		process,
	); !errors.Is(
		err,
		os.ErrProcessDone,
	) {
		t.Errorf("KillProcessGroupWith(already gone) = %v, want os.ErrProcessDone", err)
	}

	signalFailure := errors.New("operation not permitted")
	if err := KillProcessGroupWith(
		func(int, syscall.Signal) error { return signalFailure },
		process,
	); !errors.Is(
		err,
		signalFailure,
	) {
		t.Errorf("KillProcessGroupWith(failing signal) = %v, want %v", err, signalFailure)
	}
}

// TestPTYDrainOutputStopsAfterTheGrace pins that the drain before an
// exited-without-TUI report does not block forever when the read loop stalls: a
// grandchild still holding the pty keeps the master open, so the loop never
// sees EOF and the grace must end the wait.
func TestPTYDrainOutputStopsAfterTheGrace(t *testing.T) {
	ses := &Session{readDone: make(chan struct{})}
	const grace = 10 * time.Millisecond

	start := time.Now()
	ses.DrainOutput(grace)
	if elapsed := time.Since(start); elapsed < grace {
		t.Errorf("DrainOutput returned after %s, want it to wait out the %s grace", elapsed, grace)
	}
}

// TestPTYCloseAfterExit pins that Close takes the reaped path once the child
// has already been waited for (no group signal, still idempotent).
func TestPTYCloseAfterExit(t *testing.T) {
	ses := MustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "exit 0")
	if code, err := ses.WaitExit(5 * time.Second); err != nil {
		t.Fatalf("WaitExit: %v", err)
	} else if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	ses.Close()
	ses.Close()
}

// TestExitErrorCode pins the shared *exec.ExitError unwrapping used by the exit
// status, wait, and tail helpers.
func TestExitErrorCode(t *testing.T) {
	ses := MustStartUnderPTY(t, "", nil, "/bin/sh", "-c", "exit 7")
	defer ses.Close()
	if _, err := ses.WaitExit(5 * time.Second); err != nil {
		t.Fatalf("WaitExit: %v", err)
	}

	code, ok := ExitErrorCode(ses.waitErr)
	if !ok || code != 7 {
		t.Errorf("ExitErrorCode(exit 7 wait) = (%d, %t), want (7, true)", code, ok)
	}
	if _, ok := ExitErrorCode(nil); ok {
		t.Error("ExitErrorCode(nil) reported an exit error, want false")
	}
	if got := ses.ExitStatus(); got != "exit code 7" {
		t.Errorf("ExitStatus = %q, want %q", got, "exit code 7")
	}
}

// TestExitStatusAndCodeForAFailedWait pins the mappings for a wait that failed
// outright rather than reporting a process exit: the status string, the exit
// code, and WaitExit all surface the error instead of inventing a code.
func TestExitStatusAndCodeForAFailedWait(t *testing.T) {
	waitFailure := errors.New("wait failed")
	waitDone := make(chan struct{})
	close(waitDone)
	ses := &Session{waitErr: waitFailure, waitDone: waitDone}

	if got := ses.ExitStatus(); got != waitFailure.Error() {
		t.Errorf("ExitStatus = %q, want %q", got, waitFailure.Error())
	}
	if got := ExitCode(waitFailure); got != -1 {
		t.Errorf("ExitCode(failed wait) = %d, want -1", got)
	}
	if _, err := ses.WaitExit(time.Second); err == nil {
		t.Error("WaitExit = nil error, want an error for a failed wait")
	}
}

// TestStartUnderPTYReportsAStartFailure pins that a child that cannot be
// started yields an error instead of a half-built session.
func TestStartUnderPTYReportsAStartFailure(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-binary")
	if _, err := StartUnderPTY("", nil, []string{missing}); err == nil {
		t.Fatal("StartUnderPTY = nil error, want a start failure for a missing binary")
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
		if got := TerminalDimension(test.value); got != test.want {
			t.Errorf("TerminalDimension(%d) = %d, want %d", test.value, got, test.want)
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
	if got := TerminalSize(slave); got.Rows != 24 || got.Cols != 100 {
		t.Errorf("TerminalSize(terminal) = %dx%d, want 24x100", got.Rows, got.Cols)
	}

	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatalf("open pipe: %v", err)
	}
	defer readEnd.Close()
	defer writeEnd.Close()
	got := TerminalSize(readEnd)
	if got.Rows != fallbackTerminalRows || got.Cols != fallbackTerminalCols {
		t.Errorf("TerminalSize(non-terminal) = %dx%d, want %dx%d",
			got.Rows, got.Cols, fallbackTerminalRows, fallbackTerminalCols)
	}
}

// TestPTYOutputTailCutsOnRuneBoundary pins that OutputTail never splits a
// multi-byte rune at the byte-offset cut used to trim the tail.
func TestPTYOutputTailCutsOnRuneBoundary(t *testing.T) {
	ses := &Session{}
	// Three-byte runes sized so the cut lands one byte into a rune: a naive
	// byte slice would start mid-rune and yield invalid UTF-8.
	ses.buffer.WriteString(strings.Repeat("€", ptyOutputTailLimit/3+1))

	quoted := ses.OutputTail()
	text, err := strconv.Unquote(quoted)
	if err != nil {
		t.Fatalf("Unquote(%s): %v", quoted, err)
	}
	if !utf8.ValidString(text) {
		t.Errorf("OutputTail = %q, want valid UTF-8", text)
	}
}

// TestPTYHonoursWorkingDirectoryAndEnv pins that a non-empty dir and a
// non-nil env reach the child (and that the env replaces the inherited one).
func TestPTYHonoursWorkingDirectoryAndEnv(t *testing.T) {
	dir := t.TempDir()
	env := []string{"PATH=/usr/bin:/bin", "HARNESS_PTY_TEST=marker"}
	// Unset PWD so the shell's pwd builtin asks the kernel for the real
	// working directory instead of echoing the inherited one.
	ses := MustStartUnderPTY(t, dir, env, "/bin/sh", "-c",
		`unset PWD; printf '\033[?1049h'; read x; pwd; echo "$HARNESS_PTY_TEST"; exit 0`)
	defer ses.Close()

	if err := ses.WaitForTUI(5 * time.Second); err != nil {
		t.Fatalf("WaitForTUI: %v", err)
	}
	if err := ses.Write([]byte("\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := ses.WaitExit(5 * time.Second); err != nil {
		t.Fatalf("WaitExit: %v", err)
	}

	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	for _, want := range []string{resolved, "marker"} {
		deadline := time.Now().Add(time.Second)
		for !strings.Contains(string(ses.Output()), want) {
			if time.Now().After(deadline) {
				t.Fatalf("output %q does not contain %q", ses.Output(), want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// TestTuiLeft pins the alternate-screen-leave signal the quit loop stops tapping
// on: an alternate-screen harness emits it while giving the screen back as it
// tears down, and falls back to cooked mode there, so a further tap would be
// reprocessed as SIGINT and kill it mid-teardown. It must not fire on the enter
// sequence the readiness wait watches for.
func TestTuiLeft(t *testing.T) {
	if !TuiLeft([]byte("tui output\n\x1b[?1049lbye")) {
		t.Error("TuiLeft = false, want true for the alternate-screen leave sequence")
	}
	if TuiLeft([]byte("tui output\nstill up")) {
		t.Error("TuiLeft = true, want false without the leave sequence")
	}
	if TuiLeft([]byte("launcher output\n\x1b[?1049htui")) {
		t.Error("TuiLeft = true for the enter sequence, want false")
	}
}
