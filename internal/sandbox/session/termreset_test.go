package session

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/creack/pty"
)

// stubIsTerminalWriter forces the TTY check to report isTerminal and restores
// the production predicate afterwards.
func stubIsTerminalWriter(t *testing.T, isTerminal bool) {
	t.Helper()
	original := isTerminalWriter
	isTerminalWriter = func(io.Writer) bool { return isTerminal }
	t.Cleanup(func() { isTerminalWriter = original })
}

// TestResetTerminalWritesSequenceToTerminal drives the seam directly: with the
// TTY check forced true, even a non-file writer receives the sequence.
func TestResetTerminalWritesSequenceToTerminal(t *testing.T) {
	stubIsTerminalWriter(t, true)
	var b bytes.Buffer
	resetTerminal(&b)
	if b.String() != terminalReset {
		t.Fatalf("got %q", b.String())
	}
}

// TestResetTerminalWritesSequenceToRealTTY exercises the production predicate
// end to end: a real pty slave is a terminal, so it receives the sequence.
func TestResetTerminalWritesSequenceToRealTTY(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer func() { _ = master.Close() }()

	resetTerminal(slave)
	_ = slave.Close()

	got, _ := io.ReadAll(master)
	if !bytes.Contains(got, []byte(terminalReset)) {
		t.Fatalf("terminal received %q, want the reset sequence %q", got, terminalReset)
	}
}

// TestResetTerminalSkipsNonTerminalFile pins that a real *os.File that is not a
// TTY (a redirected stdout) receives nothing.
func TestResetTerminalSkipsNonTerminalFile(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer func() { _ = file.Close() }()

	resetTerminal(file)

	got, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("redirected stdout received %q, want nothing", got)
	}
}

// TestResetTerminalSkipsNonFileWriter pins that a writer that is not an
// *os.File receives nothing.
func TestResetTerminalSkipsNonFileWriter(t *testing.T) {
	var buffer bytes.Buffer
	resetTerminal(&buffer)
	if buffer.Len() != 0 {
		t.Fatalf("captured output received %q, want nothing", buffer.String())
	}
}

// TestResetTerminalSkipsNonTerminalWriter pins the Greptile fix through the
// seam: a writer the check reports as not a terminal receives nothing.
func TestResetTerminalSkipsNonTerminalWriter(t *testing.T) {
	stubIsTerminalWriter(t, false)
	var b bytes.Buffer
	resetTerminal(&b)
	if b.Len() != 0 {
		t.Fatalf("got %q, want nothing written to a non-terminal", b.String())
	}
}

// TestDefaultIsTerminalWriter pins the production predicate: a non-file writer
// and a nil *os.File are not terminals, and a pipe end is not a terminal.
func TestDefaultIsTerminalWriter(t *testing.T) {
	if defaultIsTerminalWriter(&bytes.Buffer{}) {
		t.Error("a bytes.Buffer must not be reported as a terminal")
	}
	if defaultIsTerminalWriter((*os.File)(nil)) {
		t.Error("a nil *os.File must not be reported as a terminal")
	}
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { _ = readEnd.Close(); _ = writeEnd.Close() })
	if defaultIsTerminalWriter(writeEnd) {
		t.Error("a pipe write end must not be reported as a terminal")
	}
}

func TestResetTerminalNilWriter(t *testing.T) {
	resetTerminal(nil) // must not panic
}

// TestResetTerminalTypedNilFile pins the production predicate against a
// typed-nil *os.File: File.Fd() returns ^uintptr(0) on a nil receiver, so the
// writer is not a terminal and nothing is written (and nothing panics).
func TestResetTerminalTypedNilFile(t *testing.T) {
	resetTerminal((*os.File)(nil)) // must not panic or write
}
