package driver

import (
	"bytes"
	"strings"
)

// daemonTimeoutMarker is what the launcher prints when its daemon health probe
// never succeeds. A harness whose output contains it never reached the TUI.
const daemonTimeoutMarker = "did not become healthy"

// altScreenEnter is the terminal control sequence a harness emits as its
// full-screen TUI takes over the alternate screen buffer.
const altScreenEnter = "\x1b[?1049h"

// escapeByte starts an ANSI escape sequence.
const escapeByte = 0x1b

const (
	// EnvTerm is the terminal type variable a full-screen TUI reads.
	EnvTerm = "TERM"
	// DefaultTerm is the terminfo entry a harness runs under when the test
	// process has no usable one.
	DefaultTerm = "xterm-256color"
	// TermDumb is the terminfo entry that suppresses terminal control
	// sequences; a harness cannot render its TUI under it.
	TermDumb = "dumb"
)

// UsableTerm returns the TERM to run the child under: the inherited value
// unless it is absent, empty, or "dumb", in which case the harness is given a
// modern terminfo entry so its full-screen TUI can render.
func UsableTerm(value string) string {
	if value == "" || value == TermDumb {
		return DefaultTerm
	}
	return value
}

// StripANSI removes CSI escape sequences (ESC '[' ... final byte) from b so
// captured pty output can be searched as plain text.
func StripANSI(b []byte) string {
	stripped := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] == escapeByte && i+1 < len(b) && b[i+1] == '[' {
			i = ansiSequenceEnd(b, i)
			continue
		}
		stripped = append(stripped, b[i])
	}
	return string(stripped)
}

// ansiSequenceEnd returns the index of the final byte of the CSI sequence
// starting at b[start] (ESC '['), or len(b)-1 when the sequence is truncated.
func ansiSequenceEnd(b []byte, start int) int {
	for i := start + 2; i < len(b); i++ {
		if isANSIFinalByte(b[i]) {
			return i
		}
	}
	return len(b) - 1
}

// isANSIFinalByte reports whether c terminates a CSI escape sequence; final
// bytes occupy the range '@' through '~' (0x40 to 0x7e).
func isANSIFinalByte(c byte) bool {
	return c >= 0x40 && c <= 0x7e
}

// TUIStarted reports whether the accumulated output shows a harness taking
// over the alternate screen, the signal that its full-screen TUI came up.
func TUIStarted(output []byte) bool {
	return bytes.Contains(output, []byte(altScreenEnter))
}

// SawDaemonTimeout reports whether the captured output contains the launcher's
// daemon-timeout marker; a harness whose output does never reached the TUI.
func SawDaemonTimeout(output []byte) bool {
	return strings.Contains(string(output), daemonTimeoutMarker)
}
