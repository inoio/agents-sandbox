package readback

import (
	"regexp"
	"strings"
)

// textAssignment matches a NAME=value or NAME: value assignment anywhere in a
// line of free text, with the name optionally quoted (a JSON or JS key) and the
// value's first word optionally opened by a quote or backtick.
var textAssignment = regexp.MustCompile(
	"([A-Za-z_][A-Za-z0-9_.-]*)[\"'`]?\\s*[:=]\\s*[\"'`]?([^\\s\"'`]+)",
)

// literalTokenValue matches a whole value made only of credential-token
// characters, so a reference ($VAR, $(cmd), ${VAR}) or a template (<your-key>)
// is never a literal.
var literalTokenValue = regexp.MustCompile(`^` + tokenChars + `{8,}$`)

// TextLeaksSecret reports whether free text (an instruction or prompt file)
// carries a literal credential on any line: a value shaped like one, or an
// assignment of a secret-named variable to a literal token (export
// OPENAI_API_KEY=..., TOKEN=..., password: ...). The structured config walk
// never sees free text, so this is the guard for a file copied verbatim; it errs
// toward refusing, since a refused file is only left uncopied.
func TextLeaksSecret(content []byte) bool {
	for line := range strings.Lines(string(content)) {
		if looksLikeSecretValue(line) {
			return true
		}
		// gomutants:disable-next-line INTEGER_INCREMENT,INTEGER_DECREMENT reason="any negative limit means all matches"
		for _, match := range textAssignment.FindAllStringSubmatch(line, -1) {
			if assignsLiteralSecret(match[1], strings.TrimRight(match[2], ".,;:)")) {
				return true
			}
		}
	}
	return false
}

// assignsLiteralSecret reports whether value, assigned to name, is a literal
// credential: name names a secret itself (not where one lives), value is a whole
// token, and it carries a digit or is long enough to be a key even without one
// (so prose such as "Password: required" passes). A number under a name that
// can only count tokens (max_tokens) is configuration.
func assignsLiteralSecret(name, value string) bool {
	if !flagNamesASecret(name) || !literalTokenValue.MatchString(value) {
		return false
	}
	if isNonSecretScalar(value) && !namesAStoredSecret(name) {
		return false
	}
	return strings.ContainsAny(value, "0123456789") || len(value) >= literalSecretLength
}
