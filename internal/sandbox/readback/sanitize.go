package readback

import (
	"slices"
	"strings"
	"unicode"

	"github.com/inoio/agents-sandbox/internal/configdecode"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
)

// SanitizeParams configures SanitizeConfig.
type SanitizeParams struct {
	// Path names the document, so its format is chosen for the decoded checks.
	Path string
	// Credentials are the static credentials whose values are replaced.
	Credentials []Credential
	// AllCredentials is every credential whose value must be absent from the
	// result — the caller's whole static set, when a value classified from
	// another file could appear verbatim here. It may be empty.
	AllCredentials []Credential
	// AllowedEnvVars gates substitution: a credential whose env-var name is not
	// in the set is refused, so no placeholder is emitted without a matching
	// secret. A nil set allows every name.
	AllowedEnvVars map[string]bool
	// IndirectionKey is the harness's native env-indirection key. When set, an
	// assignment holding a value is rewritten to that key with the env-var name
	// as its value; when empty, the value becomes its ${ENVVAR} placeholder.
	IndirectionKey string
	// RefuseUndeclaredSecretKeys additionally refuses content that still holds a
	// non-reference string under a secret-shaped key — a secret the credential
	// walk did not name. It is the host-seed guard; it is off for the write-back
	// so the write-back's existing behaviour is unchanged.
	RefuseUndeclaredSecretKeys bool
}

// SanitizeConfig replaces every credential's value in content with its
// ${ENVVAR} placeholder, or the harness's native indirection form under
// IndirectionKey. ok is false when a credential cannot be substituted — it has
// no env-var name, or a name AllowedEnvVars does not allow — or when any
// AllCredentials value survives the substitution, so the caller refuses the
// file rather than copy a raw value or an orphan placeholder. With
// RefuseUndeclaredSecretKeys set, it is also false when the decoded document
// still holds a non-reference string under a secret-shaped key the credential
// walk did not name.
//
// The substitution is longest-value-first, so a value that is a prefix of
// another does not corrupt the longer one. The value-remains check is literal
// and, for a known format, decoded: a value stored in its escaped form is
// missed by the literal search even though the harness reads it, and a
// known-format document that no longer parses is refused because that escaped
// form cannot be ruled out.
func SanitizeConfig(content []byte, params SanitizeParams) (string, bool) {
	sanitized, ok := substituteCredentialValues(
		string(content),
		params.Credentials,
		params.IndirectionKey,
		params.AllowedEnvVars,
	)
	if !ok {
		return "", false
	}
	if retainsCredentialValue(params.Path, sanitized, params.AllCredentials) {
		return "", false
	}
	if params.RefuseUndeclaredSecretKeys && holdsUndeclaredSecretValue(params.Path, sanitized) {
		return "", false
	}
	return sanitized, true
}

// holdsUndeclaredSecretValue reports whether content still holds a non-reference
// string under a secret-shaped key the credential walk did not name. A known
// format that does not decode reports true, so a secret cannot hide behind an
// undecodable document; an unknown format reports false.
func holdsUndeclaredSecretValue(path, content string) bool {
	format, ok := configdecode.FormatFor(path)
	if !ok {
		return false
	}
	decoded, err := configdecode.Decode(format, []byte(content))
	if err != nil {
		return true
	}
	return decodedHoldsUndeclaredSecretValue(decoded)
}

// substituteCredentialValues replaces each credential's value in content. The
// default form replaces every occurrence of the value with its ${ENVVAR}
// placeholder; a harness with a native indirection key rewrites the whole
// assignment instead (its key becomes the indirection key and its value becomes
// the env-var name). ok is false when a credential cannot be substituted — it
// has no env-var name, or its name is not one the caller will propose as a
// secret (so its placeholder would have no env.secret.yaml entry). The
// value-remains check is the caller's. Credentials are substituted longest value
// first, so a value that is a prefix of another does not corrupt the longer one.
func substituteCredentialValues(
	content string,
	credentials []Credential,
	indirectionKey string,
	allowedEnvVars map[string]bool,
) (string, bool) {
	sanitized := content
	for _, credential := range longestValueFirst(credentials) {
		if credential.EnvVar == "" || (allowedEnvVars != nil && !allowedEnvVars[credential.EnvVar]) {
			return "", false
		}
		if indirectionKey != "" {
			sanitized = rewriteNativeAssignments(sanitized, credential, indirectionKey)
			continue
		}
		sanitized = strings.ReplaceAll(sanitized, credential.Value, envsecret.PlaceholderFor(credential.EnvVar))
	}
	return sanitized, true
}

// longestValueFirst orders credentials so a longer value is substituted before a
// shorter one that is its prefix: replacing "sk-abc" first would corrupt a
// sibling "sk-abcdef" into the placeholder for "sk-abc" followed by "def".
func longestValueFirst(credentials []Credential) []Credential {
	ordered := slices.Clone(credentials)
	slices.SortStableFunc(ordered, func(a, b Credential) int {
		return len(b.Value) - len(a.Value)
	})
	return ordered
}

// retainsCredentialValue reports whether content may still hold any of the
// credentials' values after substitution. It tests the literal content and,
// when the path's format decodes, every string the decoded document holds: a
// config file may store a value in its escaped form (JSON's \" and \\), which a
// literal search misses even though the harness reads the value. A literal hit
// is deliberately kept as well, so a value left in a comment is still caught. A
// known format that does not parse is reported as retaining a value: the decoded
// walk cannot run, so an escaped form cannot be ruled out.
func retainsCredentialValue(path, content string, credentials []Credential) bool {
	if containsCredentialValue(content, credentials) {
		return true
	}
	format, ok := configdecode.FormatFor(path)
	if !ok {
		return false
	}
	decoded, err := configdecode.Decode(format, []byte(content))
	if err != nil {
		return true
	}
	return decodedHoldsCredentialValue(decoded, credentials)
}

// decodedHoldsCredentialValue reports whether any string a decoded document
// holds — a key or a leaf value — contains one of the credentials' values. A
// YAML mapping with a non-string key decodes to map[any]any rather than
// map[string]any, so both shapes are walked; a non-string key is not a value.
func decodedHoldsCredentialValue(node any, credentials []Credential) bool {
	switch typed := node.(type) {
	case map[string]any:
		for key, child := range typed {
			if containsCredentialValue(key, credentials) || decodedHoldsCredentialValue(child, credentials) {
				return true
			}
		}
	case map[any]any:
		return anyMapHoldsCredentialValue(typed, credentials)
	case []any:
		for _, child := range typed {
			if decodedHoldsCredentialValue(child, credentials) {
				return true
			}
		}
	case string:
		return containsCredentialValue(typed, credentials)
	}
	return false
}

// anyMapHoldsCredentialValue walks a mapping that decoded to map[any]any (a YAML
// mapping with a non-string key): a string key or any descendant holds a value.
func anyMapHoldsCredentialValue(document map[any]any, credentials []Credential) bool {
	for key, child := range document {
		if keyString, ok := key.(string); ok && containsCredentialValue(keyString, credentials) {
			return true
		}
		if decodedHoldsCredentialValue(child, credentials) {
			return true
		}
	}
	return false
}

// containsCredentialValue reports whether any of the credentials' values appears
// in text.
func containsCredentialValue(text string, credentials []Credential) bool {
	for _, credential := range credentials {
		if credential.Value != "" && strings.Contains(text, credential.Value) {
			return true
		}
	}
	return false
}

// rewriteNativeAssignments rewrites each assignment holding credential's value
// so the harness reads credential.EnvVar through indirectionKey: the key that
// introduces the value becomes indirectionKey, and the value becomes the
// env-var name. An occurrence whose key is already the env-var name — an
// env-table entry such as `KIMI_API_KEY = "…"` — keeps its key and takes the
// ${ENVVAR} placeholder instead, because that name is what the harness reads.
// An occurrence with no recognizable assignment key before it is left untouched,
// so the caller's value-remains check refuses the file rather than emitting the
// value.
func rewriteNativeAssignments(content string, credential Credential, indirectionKey string) string {
	value := credential.Value
	if value == "" {
		return content
	}
	var builder strings.Builder
	remaining := content
	for {
		index := strings.Index(remaining, value)
		if index < 0 {
			builder.WriteString(remaining)
			return builder.String()
		}
		keyStart, keyEnd, ok := assignmentKeyRange(remaining, index)
		if !ok {
			builder.WriteString(remaining[:index+len(value)])
			remaining = remaining[index+len(value):]
			continue
		}
		if unquotedKey(remaining[keyStart:keyEnd]) == credential.EnvVar {
			builder.WriteString(remaining[:index])
			builder.WriteString(envsecret.PlaceholderFor(credential.EnvVar))
			remaining = remaining[index+len(value):]
			continue
		}
		builder.WriteString(remaining[:keyStart])
		builder.WriteString(indirectionKey)
		builder.WriteString(remaining[keyEnd:index])
		builder.WriteString(credential.EnvVar)
		remaining = remaining[index+len(value):]
	}
}

// unquotedKey returns a key token without its surrounding quotes, if any.
func unquotedKey(key string) string {
	if len(key) >= 2 && isQuote(key[0]) && isQuote(key[len(key)-1]) {
		return key[1 : len(key)-1]
	}
	return key
}

// assignmentKeyRange returns the half-open range of the key token that
// introduces the value starting at valueStart, when that value sits on the
// right-hand side of a '=' or ':' assignment. The range includes the key's
// quotes, if any. It reports false when no key token precedes the value.
func assignmentKeyRange(content string, valueStart int) (int, int, bool) {
	separator := skipSpaceBackward(content, valueStart)
	if separator > 0 && isQuote(content[separator-1]) {
		separator--
	}
	separator = skipSpaceBackward(content, separator)
	if separator == 0 || (content[separator-1] != '=' && content[separator-1] != ':') {
		return 0, 0, false
	}
	end := skipSpaceBackward(content, separator-1)
	if end > 0 && isQuote(content[end-1]) {
		end--
	}
	start := end
	for start > 0 && isKeyChar(content[start-1]) {
		start--
	}
	if start == end {
		return 0, 0, false
	}
	if start > 0 && isQuote(content[start-1]) {
		start--
		// The key token is quoted; consume its closing quote so replacing the
		// whole token does not leave a dangling quote behind.
		if end < len(content) && isQuote(content[end]) {
			end++
		}
	}
	return start, end, true
}

// skipSpaceBackward returns index moved left past any ASCII whitespace.
func skipSpaceBackward(content string, index int) int {
	for index > 0 && isSpaceByte(content[index-1]) {
		index--
	}
	return index
}

// isSpaceByte reports whether b is ASCII whitespace.
func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

// isQuote reports whether b opens or closes a quoted config value or key.
func isQuote(b byte) bool { return b == '"' || b == '\'' }

// isKeyChar reports whether b is part of a config key token. A '.' is excluded,
// so a dotted key's final segment is the token that is rewritten.
func isKeyChar(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b == '_' || b == '-':
		return true
	default:
		return false
	}
}

// secretShapedKeyMarkers are the whole key segments that mark a value as a
// secret, so a non-reference string under one is refused as an undeclared
// secret.
var secretShapedKeyMarkers = []string{
	"KEY", "TOKEN", "SECRET", "PASSWORD", "AUTHORIZATION", "CREDENTIAL",
}

// looksLikeSecretShapedKey reports whether key names a secret, matching the
// markers against the key's camelCase- and separator-aware segments: accessToken
// matches TOKEN, apiKey matches KEY, and Authorization matches AUTHORIZATION,
// while a token count such as maxTokens is a different segment (TOKENS). A
// run-together apikey has no segment boundary, so it is matched as a whole.
func looksLikeSecretShapedKey(key string) bool {
	if normalizeToken(key) == "apikey" {
		return true
	}
	for _, segment := range keySegments(key) {
		if slices.Contains(secretShapedKeyMarkers, segment) {
			return true
		}
	}
	return false
}

// keySegments splits key into its uppercased word segments on separators and
// camelCase boundaries, so accessToken yields ACCESS and TOKEN and api_key
// yields API and KEY.
func keySegments(key string) []string {
	var segments []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			segments = append(segments, current.String())
			current.Reset()
		}
	}
	var previous rune
	for index, r := range key {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case index > 0 && unicode.IsUpper(r) && unicode.IsLower(previous):
			flush()
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(unicode.ToUpper(r))
		}
		previous = r
	}
	flush()
	return segments
}

// decodedHoldsUndeclaredSecretValue reports whether a decoded document holds a
// secret-shaped key whose string value is neither empty nor a reference: a
// secret the credential walk did not name, which would otherwise be copied
// verbatim. A numeric value (a token count) and a reference (`${…}`, a leading
// `!` command, or a bare env-var identifier) are not leaks.
func decodedHoldsUndeclaredSecretValue(node any) bool {
	switch typed := node.(type) {
	case map[string]any:
		for key, child := range typed {
			if secretKeyLeaksValue(key, child) || decodedHoldsUndeclaredSecretValue(child) {
				return true
			}
		}
	case map[any]any:
		for key, child := range typed {
			if keyString, ok := key.(string); ok && secretKeyLeaksValue(keyString, child) {
				return true
			}
			if decodedHoldsUndeclaredSecretValue(child) {
				return true
			}
		}
	case []any:
		return slices.ContainsFunc(typed, decodedHoldsUndeclaredSecretValue)
	}
	return false
}

// secretKeyLeaksValue reports whether a secret-shaped key holds a raw secret
// value rather than a placeholder, a reference, an empty string, or a non-string
// (a numeric token count).
func secretKeyLeaksValue(key string, value any) bool {
	return looksLikeSecretShapedKey(key) && isUndeclaredSecretValue(value)
}

// isUndeclaredSecretValue reports whether node is a non-empty string that is not
// a reference, so it would be a raw secret under a secret-shaped key.
func isUndeclaredSecretValue(node any) bool {
	value, ok := node.(string)
	if !ok || strings.TrimSpace(value) == "" {
		return false
	}
	return !isReferenceValue(value)
}
