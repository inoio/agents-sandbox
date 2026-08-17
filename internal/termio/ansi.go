package termio

import "strings"

// Sandbox status strings styled by StyleStatus. They mirror the lowercase
// values reported by the msb sandbox SDK.
const (
	StatusCreated  = "created"
	StatusStarting = "starting"
	StatusRunning  = "running"
	StatusStopped  = "stopped"
	StatusPaused   = "paused"
	StatusDraining = "draining"
	StatusCrashed  = "crashed"
)

// StyleStatus returns a sandbox status rendered with the color microsandbox
// uses in its list output: running is green, stopped/created dim, transitional
// states yellow, crashed red. The returned string embeds ANSI codes; callers
// strip them via stripANSICodes when writing with color disabled. Unknown
// statuses are returned lowercased and unstyled.
func StyleStatus(status string) string {
	var style string
	switch status {
	case StatusRunning:
		style = ansiGreenBold
	case StatusStopped, StatusCreated:
		style = ansiDim
	case StatusStarting, StatusPaused, StatusDraining:
		style = ansiYellowBold
	case StatusCrashed:
		style = ansiRedBold
	default:
		return status
	}
	return style + status + ansiReset
}

// stripANSICodes removes ANSI escape sequences from s, returning the visible
// text. It handles CSI sequences (ESC [ ... final byte) and two-byte escapes
// (ESC followed by a single character), which covers every sequence the CLI
// emits.
func stripANSICodes(s string) string {
	if !strings.ContainsRune(s, '\x1b') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\x1b' {
			b.WriteByte(s[i])
			continue
		}
		i++
		if i >= len(s) {
			break
		}
		if s[i] == '[' {
			// CSI sequence: skip until a final byte in 0x40..0x7E.
			for i++; i < len(s); i++ {
				if s[i] >= 0x40 && s[i] <= 0x7e {
					break
				}
			}
			continue
		}
		// Two-byte escape: the ESC prefix consumes one more byte.
	}
	return b.String()
}
