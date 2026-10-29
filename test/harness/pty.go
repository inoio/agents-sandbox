//deadcode:ignore reason="test support: referenced only by _test.go files"
package harness

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
)

const (
	// ptyPollInterval is how often waitForTUI re-inspects the captured output.
	ptyPollInterval = 50 * time.Millisecond
	// ptyDrainGrace bounds how long waitForTUI waits for the master read loop
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

// ptySession drives one child process attached to a pseudo-terminal. The
// harness smoke test uses it to start the launcher the way a user does and to
// watch its output for the full-screen TUI.
type ptySession struct {
	cmd    *exec.Cmd
	master *os.File

	// cancel stops the child; close uses it to kill a session the test left
	// running.
	cancel context.CancelFunc

	mu     sync.Mutex
	buffer bytes.Buffer

	// reaped is set under mu once cmd.Wait has returned; close consults it
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

// startUnderPTY starts argv attached to a new pseudo-terminal. An empty dir
// runs the child in the test's working directory; a nil env inherits the parent
// environment. The caller owns the returned session's cleanup; the tests wrap
// this in a must-start helper that fails the test on error.
func startUnderPTY(dir string, env []string, argv []string) (*ptySession, error) {
	ctx, cancel := context.WithCancel(context.Background())
	//nolint:gosec // G204: the caller names the binary to drive; no untrusted input reaches it.
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if dir != "" {
		cmd.Dir = dir
	}
	if env != nil {
		cmd.Env = env
	}
	// Cancel the whole process group rather than just the direct child: a
	// harness can leave grandchildren holding the pty.
	cmd.Cancel = func() error { return killProcessGroup(cmd) }

	// pty.StartWithSize wires the child's stdio to the pty's slave side, puts
	// it in a new session (Setsid) and makes the pty its controlling terminal,
	// which is what lets the child's harness render full-screen. The new
	// session also makes the child a process-group leader, so its pid doubles
	// as its pgid and close can signal the whole group. Adding Setpgid on top
	// would fail with EPERM: setpgid rejects a session leader.
	master, err := pty.StartWithSize(cmd, terminalSize(os.Stdin))
	if err != nil {
		cancel()
		return nil, err
	}

	ses := &ptySession{
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

// terminalSize returns the window size for the child's pty: the given terminal
// when it has one, so an interactive run renders at the real window size, else
// a generous fallback. Without a size a full-screen TUI renders into a
// zero-column terminal.
func terminalSize(file *os.File) *pty.Winsize {
	if rows, cols, err := pty.Getsize(file); err == nil {
		return &pty.Winsize{
			Rows: terminalDimension(rows),
			Cols: terminalDimension(cols),
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

// terminalDimension narrows a terminal dimension reported as an int to the
// uint16 width of a pty.Winsize field.
func terminalDimension(value int) uint16 {
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
func (s *ptySession) readOutput() {
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
func (s *ptySession) waitProcess() {
	waitErr := s.cmd.Wait()
	s.mu.Lock()
	s.waitErr = waitErr
	s.reaped = true
	s.mu.Unlock()
	close(s.waitDone)
}

// output returns a copy of everything captured so far.
func (s *ptySession) output() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bytes.Clone(s.buffer.Bytes())
}

// write sends data to the child's terminal.
func (s *ptySession) write(data []byte) error {
	_, err := s.master.Write(data)
	return err
}

// waitForTUI blocks until the child's output shows a full-screen TUI while the
// child is still alive, or the child exits, whichever comes first. A marker the
// child wrote seconds before exiting is not accepted: a client that draws its
// TUI and immediately exits never handled input, so its exit lets it pass the
// smoke test as if it had quit on the quit keys. The error names what was seen
// instead.
func (s *ptySession) waitForTUI(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if tuiStarted(s.output()) && !s.hasExited() {
			return nil
		}
		if s.hasExited() {
			return s.exitedWithoutTUIError()
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf(
				"no full-screen TUI within %s (process still running); output: %s",
				timeout, s.outputTail())
		}
		select {
		case <-time.After(ptyPollInterval):
		case <-s.waitDone:
		}
	}
}

// exitedWithoutTUIError describes a child that stopped before a full-screen TUI
// could be observed while it was alive, draining pending output first so the
// message can show what the child did print.
func (s *ptySession) exitedWithoutTUIError() error {
	s.drainOutput(ptyDrainGrace)
	if tuiStarted(s.output()) {
		return fmt.Errorf(
			"process entered the alternate screen but had already exited (%s); a TUI seen only after exit proves nothing about handling input; output: %s",
			s.exitStatus(),
			s.outputTail(),
		)
	}
	return fmt.Errorf(
		"no full-screen TUI before the process exited (%s); output: %s",
		s.exitStatus(), s.outputTail())
}

// waitSettled blocks until settle has elapsed with the child still alive, and
// reports the child's exit as an error when it stops first. It is the readiness
// signal for a harness that keeps the terminal's normal buffer, where no
// terminal-level marker announces that the UI is up.
//
// It waits out the launcher rather than the child: before the harness runs, the
// launcher installs or loads an image and starts a VM, and its output is silent
// for seconds at a time while it does, so a quiet stream cannot stand in for a
// rendered screen.
func (s *ptySession) waitSettled(settle time.Duration) error {
	deadline := time.Now().Add(settle)
	for {
		if s.hasExited() {
			return s.exitedWithoutTUIError()
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

// waitForMarker blocks until the child's output contains marker while the child
// is still alive, or the child exits, or timeout elapses. It is the readiness
// signal for a normal-buffer harness whose CLI prints a stable startup line: it
// ends as soon as that line appears, where waitSettled always waits out the full
// budget. timeout is a ceiling, not the expected wait.
func (s *ptySession) waitForMarker(marker string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if bytes.Contains(s.output(), []byte(marker)) && !s.hasExited() {
			return nil
		}
		if s.hasExited() {
			s.drainOutput(ptyDrainGrace)
			return fmt.Errorf(
				"no readiness marker %q before the process exited (%s); output: %s",
				marker, s.exitStatus(), s.outputTail())
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf(
				"no readiness marker %q within %s (process still running); output: %s",
				marker, timeout, s.outputTail())
		}
		select {
		case <-time.After(ptyPollInterval):
		case <-s.waitDone:
		}
	}
}

// waitExit blocks until the child exits and returns its exit code. A process
// that exited successfully yields 0; one killed by a signal, -1. Only a wait
// that does not complete within the timeout (or fails outright) is an error.
func (s *ptySession) waitExit(timeout time.Duration) (int, error) {
	select {
	case <-s.waitDone:
	case <-time.After(timeout):
		return 0, fmt.Errorf("process did not exit within %s", timeout)
	}
	if s.waitErr != nil {
		if _, ok := exitErrorCode(s.waitErr); !ok {
			return 0, fmt.Errorf("wait for process: %w", s.waitErr)
		}
	}
	return exitCode(s.waitErr), nil
}

// hasExited reports whether the child has been reaped. It reads the flag under
// the same mutex waitProcess records it under.
func (s *ptySession) hasExited() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reaped
}

// close kills the child's process group if it is still running and closes the
// pty master. It is idempotent and safe to call from a cleanup.
func (s *ptySession) close() {
	s.closeOnce.Do(func() {
		// Once the child is reaped its pid could belong to an unrelated
		// process, so signalling the group would hit the wrong process.
		if !s.hasExited() {
			s.cancel()
		}
		_ = s.master.Close()
	})
}

// killProcessGroup SIGKILLs the command's process group. The child runs in its
// own session, so its pid is also its pgid and the group covers any
// grandchildren.
func killProcessGroup(cmd *exec.Cmd) error {
	return killProcessGroupWith(syscall.Kill, cmd)
}

// killProcessGroupWith SIGKILLs the command's process group using the given
// signal function. The signal is a parameter rather than a package variable so
// a test can exercise the failure paths without a shared variable the cancel
// goroutine a timed-out session leaves behind could still be reading. It
// reports os.ErrProcessDone, the "no needless error" signal exec.Cmd.Wait
// understands: the process is finished either way, and reporting a
// cancellation error would hide its real exit status.
func killProcessGroupWith(signal func(pid int, sig syscall.Signal) error, cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	err := signal(-cmd.Process.Pid, syscall.SIGKILL)
	if err == nil || errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

// drainOutput waits up to grace for the read loop to flush output written just
// before exit. The read loop can stall past the exit, so it bounds the wait: a
// grandchild still holding the pty keeps the master open, and the loop then
// never sees EOF.
func (s *ptySession) drainOutput(grace time.Duration) {
	select {
	case <-s.readDone:
	case <-time.After(grace):
	}
}

// exitStatus names the child's exit for error messages.
func (s *ptySession) exitStatus() string {
	if s.waitErr == nil {
		return "exit code 0"
	}
	if code, ok := exitErrorCode(s.waitErr); ok {
		return fmt.Sprintf("exit code %d", code)
	}
	return s.waitErr.Error()
}

// outputTail renders the tail of the captured output, without escape
// sequences, for an error message. The cut lands on a rune boundary by
// dropping any partial rune the byte offset splits.
func (s *ptySession) outputTail() string {
	text := stripANSI(s.output())
	if len(text) > ptyOutputTailLimit {
		text = "…" + strings.ToValidUTF8(text[len(text)-ptyOutputTailLimit:], "")
	}
	return fmt.Sprintf("%q", text)
}

// exitErrorCode returns the exit code carried by err and whether err is an
// *exec.ExitError.
func exitErrorCode(err error) (int, bool) {
	exitErr, ok := errors.AsType[*exec.ExitError](err)
	if !ok {
		return 0, false
	}
	return exitErr.ExitCode(), true
}

// exitCode maps the error from cmd.Wait to a process exit code: 0 for a clean
// exit, the process's code for an *exec.ExitError, and -1 otherwise (a process
// killed by a signal, or a wait that failed outright).
func exitCode(waitErr error) int {
	if code, ok := exitErrorCode(waitErr); ok {
		return code
	}
	if waitErr == nil {
		return 0
	}
	return -1
}
