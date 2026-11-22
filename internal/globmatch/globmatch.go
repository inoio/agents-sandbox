// Package globmatch matches a file name against a snippet glob, supporting *,
// **, ?, and {a,b} brace alternatives. It is a leaf so both the agent profiles
// and the config merger can share one matcher without an import cycle.
package globmatch

import (
	"path"
	"strings"
)

// NameMatches reports whether name matches pattern, supporting *, **, ?, and
// {a,b} brace alternatives. Matching is done against the basename only.
func NameMatches(pattern, name string) bool {
	base := path.Base(name)
	for _, concrete := range expandBraces(pattern) {
		// path.Match reports ok=false for a malformed pattern, so its error
		// cannot change the result: only the match outcome matters.
		if ok, _ := path.Match(concrete, base); ok {
			return true
		}
	}
	return false
}

// expandBraces expands {a,b,c} alternatives into a list of concrete patterns.
// Patterns without braces are returned unchanged.
func expandBraces(pattern string) []string {
	open := strings.Index(pattern, "{")
	if open < 0 {
		return []string{pattern}
	}
	closeIdx := strings.Index(pattern[open:], "}")
	// gomutants:disable-next-line CONDITIONALS_BOUNDARY,INTEGER_INCREMENT reason="the searched substring starts with '{', so the '}' index is -1 or at least 1, never 0"
	if closeIdx < 0 {
		return []string{pattern}
	}
	closeIdx += open
	prefix := pattern[:open]
	suffix := pattern[closeIdx+1:]
	var expanded []string
	for alt := range strings.SplitSeq(pattern[open+1:closeIdx], ",") {
		expanded = append(expanded, expandBraces(prefix+alt+suffix)...)
	}
	return expanded
}
