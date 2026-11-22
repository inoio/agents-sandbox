package driver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

const (
	// ptyPollInterval is how often WaitForTUI re-inspects the captured output.
	ptyPollInterval = 50 * time.Millisecond
	// ptyDrainGrace bounds how long WaitForTUI waits for the master read loop
	// to flush the output a process wrote just before exiting.
	ptyDrainGrace = 500 * time.Millisecond
	// ptyOutputTailLimit caps the output an error message quotes.
	ptyOutputTailLimit = 500
	// fallbackTerminalRows and fallbackTerminalCols size the child's terminal
	// when the test process has none to inherit. A full-screen TUI renders
	// into zero columns without a size, so a fallback is required.
	fallbackTerminalRows = 40
	fallbackTerminalCols = 120
)

// altScreenLeave is the sequence a harness emits as it gives the alternate
// screen back while quitting: its own teardown signal, and the point past which
// a further quit tap would reach a terminal the harness has already restored to
// cooked mode.
const altScreenLeave = "\x1b[?1049l"

// Session drives one child process attached to a pseudo-terminal. A suite uses
// it to start the launcher the way a user does and to watch its output for the
// full-screen TUI.
type Session struct {
	cmd    *exec.Cmd
	master *os.File

	// cancel stops the child; Close uses it to kill a session the test left
	// running.
	cancel context.CancelFunc

	mu     sync.Mutex
	buffer bytes.Buffer

	// reaped is set under mu once cmd.Wait has returned; Close consults it
	// under the same mutex before signalling the child's process group.
	reaped bool

	// waitErr is the result of cmd.Wait, valid once waitDone is closed.
	waitErr error

	// waitDone is closed once cmd.Wait has returned and waitErr is final.
	waitDone chan struct{}

	// readDone is closed once the master read loop stopped (EOF or error).
	readDone chan struct{}

	closeOnce sync.Once
}

// StartUnderPTY starts argv attached to a new pseudo-terminal. An empty dir
// runs the child in the test's working directory; a nil env inherits the parent
// environment. The caller owns the returned session's cleanup; a suite wraps
// this in MustStartUnderPTY, which fails the test on error.
func StartUnderPTY(dir string, env []string, argv []string) (*Session, error) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // G204: the caller passes the argv to drive
	if dir != "" {
		cmd.Dir = dir
	}
	if env != nil {
		cmd.Env = env
	}
	// Cancel the whole process group rather than just the direct child: a
	// harness can leave grandchildren holding the pty.
	cmd.Cancel = func() error { return KillProcessGroup(cmd) }

	// pty.StartWithSize wires the child's stdio to the pty's slave side, puts
	// it in a new session (Setsid) and makes the pty its controlling terminal,
	// which is what lets the child's harness render full-screen. The new
	// session also makes the child a process-group leader, so its pid doubles
	// as its pgid and Close can signal the whole group. Adding Setpgid on top
	// would fail with EPERM: setpgid rejects a session leader.
	master, err := pty.StartWithSize(cmd, TerminalSize(os.Stdin))
	if err != nil {
		cancel()
		return nil, err
	}

	ses := &Session{
		cmd:       cmd,
		master:    master,
		cancel:    cancel,
		mu:        sync.Mutex{},
		buffer:    bytes.Buffer{},
		reaped:    false,
		waitErr:   nil,
		waitDone:  make(chan struct{}),
		readDone:  make(chan struct{}),
		closeOnce: sync.Once{},
	}
	go ses.readOutput()
	go ses.waitProcess()
	return ses, nil
}

// MustStartUnderPTY starts argv under a pseudo-terminal, failing tb if it
// cannot start and closing the session in tb's cleanup.
func MustStartUnderPTY(tb TB, dir string, env []string, argv ...string) *Session {
	tb.Helper()

	ses, err := StartUnderPTY(dir, env, argv)
	if err != nil {
		tb.Fatalf("start %v under pty: %v", argv, err)
	}
	tb.Cleanup(ses.Close)
	return ses
}

// TerminalSize returns the window size for the child's pty: the given terminal
// when it has one, so an interactive run renders at the real window size, else
// a generous fallback. Without a size a full-screen TUI renders into a
// zero-column terminal.
func TerminalSize(file *os.File) *pty.Winsize {
	if rows, cols, err := pty.Getsize(file); err == nil {
		return &pty.Winsize{
			Rows: TerminalDimension(rows),
			Cols: TerminalDimension(cols),
			X:    0,
			Y:    0,
		}
	}
	return &pty.Winsize{
		Rows: fallbackTerminalRows,
		Cols: fallbackTerminalCols,
		X:    0,
		Y:    0,
	}
}

// TerminalDimension narrows a terminal dimension reported as an int to the
// uint16 width of a pty.Winsize field.
func TerminalDimension(value int) uint16 {
	if value < 0 {
		return 0
	}
	if value > math.MaxUint16 {
		return math.MaxUint16
	}
	return uint16(value)
}

// readOutput captures everything the child writes until the pty reports EOF or
// an error, which happens once every slave descriptor is closed.
func (s *Session) readOutput() {
	defer close(s.readDone)
	buf := make([]byte, 4096)
	for {
		n, err := s.master.Read(buf)
		if n > 0 {
			s.mu.Lock()
			s.buffer.Write(buf[:n])
			s.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// waitProcess reaps the child exactly once; waitDone publishes the result.
func (s *Session) waitProcess() {
	waitErr := s.cmd.Wait()
	s.mu.Lock()
	s.waitErr = waitErr
	s.reaped = true
	s.mu.Unlock()
	close(s.waitDone)
}

// Output returns a copy of everything captured so far.
func (s *Session) Output() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bytes.Clone(s.buffer.Bytes())
}

// Write sends data to the child's terminal.
func (s *Session) Write(data []byte) error {
	_, err := s.master.Write(data)
	return err
}

// inRawMode reports whether the terminal is in raw mode. The launcher's msb
// attach client puts the test pty in raw mode while it drives the session and
// restores it before it exits, so the pty falling back to cooked mode is the
// signal that the attach is on its way out: a key written past that point is no
// longer a keystroke but the SIGINT the cooked terminal makes of it, which kills
// the launcher mid-teardown. Reading it through the master works because a pty
// master and slave share one termios (the master has no line discipline of its
// own). A read error reports false, which the quit loop treats as "stop tapping".
func (s *Session) inRawMode() bool {
	termios, err := unix.IoctlGetTermios(int(s.master.Fd()), ioctlReadTermios)
	if err != nil {
		return false
	}
	return termios.Lflag&unix.ICANON == 0
}

// WaitForTUI blocks until the child's output shows a full-screen TUI while the
// child is still alive, or the child exits, whichever comes first. A marker the
// child wrote seconds before exiting is not accepted: a client that draws its
// TUI and immediately exits never handled input, so its exit lets it pass the
// smoke test as if it had quit on the quit keys. The error names what was seen
// instead.
func (s *Session) WaitForTUI(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if TUIStarted(s.Output()) && !s.HasExited() {
			return nil
		}
		if s.HasExited() {
			return s.ExitedWithoutTUIError()
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf(
				"no full-screen TUI within %s (process still running); output: %s",
				timeout, s.OutputTail())
		}
		select {
		case <-time.After(ptyPollInterval):
		case <-s.waitDone:
		}
	}
}

// ExitedWithoutTUIError describes a child that stopped before a full-screen TUI
// could be observed while it was alive, draining pending output first so the
// message can show what the child did print.
func (s *Session) ExitedWithoutTUIError() error {
	s.DrainOutput(ptyDrainGrace)
	if TUIStarted(s.Output()) {
		return fmt.Errorf(
			"process entered the alternate screen but had already exited (%s); a TUI seen only after exit proves nothing about handling input; output: %s",
			s.ExitStatus(),
			s.OutputTail(),
		)
	}
	return fmt.Errorf(
		"no full-screen TUI before the process exited (%s); output: %s",
		s.ExitStatus(), s.OutputTail())
}

// WaitSettled blocks until settle has elapsed with the child still alive, and
// reports the child's exit as an error when it stops first. It is the readiness
// signal for a harness that keeps the terminal's normal buffer, where no
// terminal-level marker announces that the UI is up.
//
// It waits out the launcher rather than the child: before the harness runs, the
// launcher installs or loads an image and starts a VM, and its output is silent
// for seconds at a time while it does, so a quiet stream cannot stand in for a
// rendered screen.
func (s *Session) WaitSettled(settle time.Duration) error {
	deadline := time.Now().Add(settle)
	for {
		if s.HasExited() {
			return s.ExitedWithoutTUIError()
		}
		if !time.Now().Before(deadline) {
			return nil
		}
		select {
		case <-time.After(ptyPollInterval):
		case <-s.waitDone:
		}
	}
}

// MarkerText normalizes text for marker matching: escape sequences are removed,
// then every whitespace character, so a marker can be written with the spaces a
// human sees while still matching output whose spaces are rendered as cursor
// moves. It is deliberately lossy — two lines collapse together — which is safe
// because the tap-until-exit quit loop tolerates a marker seen a little early.
func MarkerText(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', '\v', '\f':
			// gomutants:disable-next-line INTEGER_INCREMENT reason="strings.Map drops the rune for any negative mapping, so -1 and -2 are indistinguishable"
			return -1
		}
		return r
	}, StripANSI([]byte(s)))
}

// WaitForMarker blocks until the child's output contains marker while the child
// is still alive, or the child exits, or timeout elapses. It is the readiness
// signal for a normal-buffer harness whose CLI prints a stable startup line: it
// ends as soon as that line appears, where WaitSettled always waits out the full
// budget. timeout is a ceiling, not the expected wait.
//
// The match runs on MarkerText of both sides: escape sequences removed, then all
// whitespace. A diff-painting TUI (claude-code's Ink) both interleaves escape
// sequences and renders spaces by moving the cursor, so "Welcome to Claude Code"
// arrives as "Welcome\e[1Cto\e[1CClaude\e[1CCode" and neither a raw nor an
// ANSI-only match would find it.
func (s *Session) WaitForMarker(marker string, timeout time.Duration) error {
	needle := MarkerText(marker)
	deadline := time.Now().Add(timeout)
	for {
		if strings.Contains(MarkerText(string(s.Output())), needle) && !s.HasExited() {
			return nil
		}
		if s.HasExited() {
			s.DrainOutput(ptyDrainGrace)
			return fmt.Errorf(
				"no readiness marker %q before the process exited (%s); output: %s",
				marker, s.ExitStatus(), s.OutputTail())
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf(
				"no readiness marker %q within %s (process still running); output: %s",
				marker, timeout, s.OutputTail())
		}
		select {
		case <-time.After(ptyPollInterval):
		case <-s.waitDone:
		}
	}
}

// WaitExit blocks until the child exits and returns its exit code. A process
// that exited successfully yields 0; one killed by a signal, -1. Only a wait
// that does not complete within the timeout (or fails outright) is an error.
func (s *Session) WaitExit(timeout time.Duration) (int, error) {
	select {
	case <-s.waitDone:
	case <-time.After(timeout):
		return 0, fmt.Errorf("process did not exit within %s", timeout)
	}
	if s.waitErr != nil {
		if _, ok := ExitErrorCode(s.waitErr); !ok {
			return 0, fmt.Errorf("wait for process: %w", s.waitErr)
		}
	}
	return ExitCode(s.waitErr), nil
}

// HasExited reports whether the child has been reaped. It reads the flag under
// the same mutex waitProcess records it under.
func (s *Session) HasExited() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reaped
}

// Close kills the child's process group if it is still running and closes the
// pty master. It is idempotent and safe to call from a cleanup.
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		// Once the child is reaped its pid could belong to an unrelated
		// process, so signalling the group would hit the wrong process.
		if !s.HasExited() {
			s.cancel()
		}
		_ = s.master.Close()
	})
}

// KillProcessGroup SIGKILLs the command's process group. The child runs in its
// own session, so its pid is also its pgid and the group covers any
// grandchildren.
func KillProcessGroup(cmd *exec.Cmd) error {
	return KillProcessGroupWith(syscall.Kill, cmd)
}

// KillProcessGroupWith SIGKILLs the command's process group using the given
// signal function. The signal is a parameter rather than a package variable so
// a test can exercise the failure paths without a shared variable the cancel
// goroutine a timed-out session leaves behind could still be reading. It
// reports os.ErrProcessDone, the "no needless error" signal exec.Cmd.Wait
// understands: the process is finished either way, and reporting a
// cancellation error would hide its real exit status.
func KillProcessGroupWith(signal func(pid int, sig syscall.Signal) error, cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	err := signal(-cmd.Process.Pid, syscall.SIGKILL)
	if err == nil || errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

// DrainOutput waits up to grace for the read loop to flush output written just
// before exit. The read loop can stall past the exit, so it bounds the wait: a
// grandchild still holding the pty keeps the master open, and the loop then
// never sees EOF.
func (s *Session) DrainOutput(grace time.Duration) {
	select {
	case <-s.readDone:
	case <-time.After(grace):
	}
}

// ExitStatus names the child's exit for error messages.
func (s *Session) ExitStatus() string {
	if s.waitErr == nil {
		return "exit code 0"
	}
	if code, ok := ExitErrorCode(s.waitErr); ok {
		return fmt.Sprintf("exit code %d", code)
	}
	return s.waitErr.Error()
}

// OutputTail renders the tail of the captured output, without escape
// sequences, for an error message. The cut lands on a rune boundary by
// dropping any partial rune the byte offset splits.
func (s *Session) OutputTail() string {
	text := StripANSI(s.Output())
	if len(text) > ptyOutputTailLimit {
		text = "…" + strings.ToValidUTF8(text[len(text)-ptyOutputTailLimit:], "")
	}
	return fmt.Sprintf("%q", text)
}

// ExitErrorCode returns the exit code carried by err and whether err is an
// *exec.ExitError.
func ExitErrorCode(err error) (int, bool) {
	exitErr, ok := errors.AsType[*exec.ExitError](err)
	if !ok {
		return 0, false
	}
	return exitErr.ExitCode(), true
}

// ExitCode maps the error from cmd.Wait to a process exit code: 0 for a clean
// exit, the process's code for an *exec.ExitError, and -1 otherwise (a process
// killed by a signal, or a wait that failed outright).
func ExitCode(waitErr error) int {
	if code, ok := ExitErrorCode(waitErr); ok {
		return code
	}
	if waitErr == nil {
		return 0
	}
	return -1
}

// TuiLeft reports whether the accumulated output shows a harness giving the
// alternate screen back, the signal that an alternate-screen harness caught the
// quit and is tearing down rather than still listening for a further tap.
func TuiLeft(output []byte) bool {
	return bytes.Contains(output, []byte(altScreenLeave))
}

// TeardownStarted reports whether the quit loop should stop tapping and only
// wait for the exit: the harness gave the alternate screen back, or the
// launcher's attach has restored the test pty to cooked mode. The
// alternate-screen check starts from the second press, so a leave sequence left
// by the readiness wait cannot end the loop before it has tapped once; the
// cooked check applies from the first press, so a session that is already
// tearing down is never tapped.
func TeardownStarted(ses *Session, presses int) bool {
	if presses > 1 && TuiLeft(ses.Output()) {
		return true
	}
	return !ses.inRawMode()
}
