package termio

import (
	"io"
	"os"

	"golang.org/x/term"
)

// TerminalResetSequence clears the emulator modes a guest TUI may have enabled
// (leave the alternate screen, disable mouse reporting, show the cursor, SGR
// reset). It is emitted to the host terminal after an interactive attach
// returns abnormally, before the launcher's next message, so that message is
// neither hidden by nor rendered into the leftover modes.
const TerminalResetSequence = "\x1b[?1049l\x1b[?1000l\x1b[?1006l\x1b[?25h\x1b[0m"

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

// ResetTerminal writes TerminalResetSequence to w when w is a terminal, and
// nothing otherwise.
func ResetTerminal(w io.Writer) {
	if !isTerminalWriter(w) {
		return
	}
	_, _ = w.Write([]byte(TerminalResetSequence))
}
