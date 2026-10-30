package opener

import "strings"

// maxReportedRefusals bounds the report so a flood of refused requests cannot
// fill the terminal after the session ends.
const maxReportedRefusals = 10

// maxReportedTargetLength shortens long targets (auth URLs) in the report.
const maxReportedTargetLength = 200

// Refusal is one target that was not opened on the host.
type Refusal struct {
	Target string
	Reason string
}

// Report collects what a watcher refused during a session. It is printed after
// the attached TUI exits, because writing to the terminal while attached would
// corrupt the agent's screen.
type Report struct {
	Refusals []Refusal
	// Omitted counts refusals beyond maxReportedRefusals.
	Omitted int
}

func (r *Report) addRefusal(target, reason string) {
	if len(r.Refusals) >= maxReportedRefusals {
		r.Omitted++
		return
	}
	r.Refusals = append(r.Refusals, Refusal{Target: shorten(sanitize(target)), Reason: reason})
}

func shorten(text string) string {
	if len(text) <= maxReportedTargetLength {
		return text
	}
	return text[:maxReportedTargetLength] + "…"
}

// sanitize replaces control characters, so a guest-supplied string can neither
// break the one-line response protocol nor inject terminal escape sequences
// into the session report.
func sanitize(text string) string {
	return strings.Map(func(character rune) rune {
		if isControlCharacter(character) {
			return ' '
		}
		return character
	}, text)
}
