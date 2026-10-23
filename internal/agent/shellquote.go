package agent

import "strings"

// shellQuote wraps s in single quotes for safe interpolation into a command
// line executed by a POSIX shell (attach commands run via `bash -c`). Embedded
// single quotes are escaped as `'\”`. Callers pass literal guest paths, so no
// expansion is intended.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellQuoteArgs quotes each forwarded command-line argument. The launcher
// receives the arguments already split by the host shell and joins them into one
// command line that the guest shell splits again, so an argument containing
// spaces or shell metacharacters (`-- "explain this function"`) would otherwise
// reach the agent as several words.
func shellQuoteArgs(args []string) []string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	return quoted
}
