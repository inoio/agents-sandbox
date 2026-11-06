package session

import (
	"io"
	"os"

	"golang.org/x/term"
)

// terminalReset is the sequence that clears the guest TUI's emulator modes
// (leave alternate screen, disable mouse, show cursor, SGR reset). Emitted to
// the host terminal after an attach returns, before any launcher message.
const terminalReset = "\x1b[?1049l\x1b[?1000l\x1b[?1006l\x1b[?25h\x1b[0m"

// isTerminalWriter reports whether w is a terminal that can safely receive the
// reset control sequence. Only a real *os.File tied to a TTY qualifies, so a
// redirected or captured stdout is never polluted with raw escape bytes. It is
// a substitutable seam so tests can drive both paths without a real TTY.
var isTerminalWriter = defaultIsTerminalWriter

// defaultIsTerminalWriter is the production TTY check behind isTerminalWriter.
func defaultIsTerminalWriter(w io.Writer) bool {
	file, ok := w.(*os.File)
	// A typed-nil *os.File is safe: File.Fd() returns ^uintptr(0) on a nil
	// receiver, so term.IsTerminal reports false without a separate nil check.
	return ok && term.IsTerminal(int(file.Fd()))
}

// resetTerminal writes terminalReset to w when w is a terminal, and nothing
// otherwise.
func resetTerminal(w io.Writer) {
	if !isTerminalWriter(w) {
		return
	}
	_, _ = w.Write([]byte(terminalReset))
}
