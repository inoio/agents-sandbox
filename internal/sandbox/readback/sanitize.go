package readback

import (
	"regexp"
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
	// secret the credential walk did not name: a non-reference string under a
	// secret-shaped key, a secret-shaped value under any key, or an env-table
	// entry not known to be configuration. The host seed and the write-back both
	// set it.
	RefuseUndeclaredSecretKeys bool
}

// SanitizeConfig replaces every credential's value in content with its
// ${ENVVAR} placeholder, or the harness's native indirection form under
// IndirectionKey. ok is false when a credential cannot be substituted — it has
// no env-var name, or a name AllowedEnvVars does not allow — or when any
// AllCredentials value survives the substitution, so the caller refuses the
// file rather than copy a raw value or an orphan placeholder. With
// RefuseUndeclaredSecretKeys set, it is also false when the decoded document
// still holds a secret the credential walk did not name (see the field).
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
// string — directly or inside a collection — under a secret-shaped key the
// credential walk did not name. A known format that does not decode reports
// true, so a secret cannot hide behind an undecodable document; an unknown
// format reports false.
func holdsUndeclaredSecretValue(path, content string) bool {
	// The raw text is scanned for secret-shaped values too: a comment never
	// reaches the decoded walk but is copied verbatim.
	if looksLikeSecretValue(content) {
		return true
	}
	format, ok := configdecode.FormatFor(path)
	if !ok {
		return false
	}
	// The decoder reads only a YAML stream's first document, so a later one
	// cannot be checked and the file is refused.
	if format == configdecode.FormatYAML && holdsLaterYAMLDocument(content) {
		return true
	}
	decoded, err := configdecode.Decode(format, []byte(content))
	if err != nil {
		return true
	}
	return decodedHoldsUndeclaredSecretValue(decoded)
}

// yamlDocumentMarker matches a line that opens or closes a YAML document.
var yamlDocumentMarker = regexp.MustCompile(`^(---|\.\.\.)(\s|$)`)

// isYAMLLineBreak reports whether r terminates a line in a YAML stream. It is
// the break set go-yaml honours — LF, CR, NEL, LS, PS — so a stream whose lines
// end in a lone CR (or one of the others) is split the same way the decoder
// splits it. Splitting on "\n" alone would leave such a stream as one line and
// hide a later document's marker from the scan.
func isYAMLLineBreak(r rune) bool {
	switch r {
	case '\n', '\r', '\u0085', '\u2028', '\u2029':
		return true
	}
	return false
}

// holdsLaterYAMLDocument reports whether content holds a YAML document after its
// first one. A '---' line on its own only opens the first document as long as
// nothing precedes it but blank lines and comments, and the opening '---' is
// consumed once: a second one is followed by a document the caller's
// first-document decode cannot check, as is a '...' after any content. A '---'
// inside a block scalar is indented or a plain scalar's continuation, so it
// opens no line of its own. Lines are split on every break the decoder honours
// (see isYAMLLineBreak), so a CR-delimited stream's markers are seen.
func holdsLaterYAMLDocument(content string) bool {
	seenContent := false
	seenMarker := false
	for _, line := range strings.FieldsFunc(content, isYAMLLineBreak) {
		switch {
		case yamlDocumentMarker.MatchString(line):
			if seenContent || seenMarker || strings.HasPrefix(line, "...") {
				return true
			}
			seenMarker = true
		case strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#"):
			// A blank line or a comment may precede the first document marker.
		default:
			seenContent = true
		}
	}
	return false
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
		if keyString, ok := key.(string); ok {
			if containsCredentialValue(keyString, credentials) {
				return true
			}
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
	// endsWithQuote carries the bounds check, so stepping back over a quote needs
	// no index bound comparison here.
	if endsWithQuote(content[:separator]) {
		separator--
	}
	separator = skipSpaceBackward(content, separator)
	if separator == 0 || (content[separator-1] != '=' && content[separator-1] != ':') {
		return 0, 0, false
	}
	end := skipSpaceBackward(content, separator-1)
	if endsWithQuote(content[:end]) {
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
		// whole token does not leave a dangling quote behind. end is in range
		// here: it was bounded by the separator guard above.
		if isQuote(content[end]) {
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

// endsWithQuote reports whether s ends with a double or single quote. It is the
// slice form of isQuote, so a caller can step an index back over a just-passed
// quote without a separate bounds check.
func endsWithQuote(s string) bool {
	return strings.HasSuffix(s, `"`) || strings.HasSuffix(s, `'`)
}

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
// secret. Each marker matches both the singular and the plural segment, because
// a secret list is routinely spelled in the plural (tokens, apiKeys).
var secretShapedKeyMarkers = []string{
	"KEY", tokenCountMarker, "SECRET", "PASSWORD", "PASS", "PASSWD", "PWD", "AUTHORIZATION", "CREDENTIAL",
	"APIKEY", "ACCESSKEY", "PRIVATEKEY", "AUTHTOKEN", "PIN",
}

// tokenCountMarker is the one marker that also names a count (max tokens), so a
// number under it is not a secret.
const tokenCountMarker = "TOKEN"

// leafSecretKeyMarkers mark a key as secret only when it holds a string
// directly: "auth", "cookie" or "session" also name tables of settings (Qwen
// Code's security.auth.selectedType), whose values are not credentials.
var leafSecretKeyMarkers = []string{"AUTH", "COOKIE", "SESSION"}

// namesAStoredSecret reports whether key names a secret that can be a number
// (a password, a PIN, a key) rather than only a token count: every secret
// marker except TOKEN.
func namesAStoredSecret(key string) bool {
	return slices.ContainsFunc(keySegments(key), func(segment string) bool {
		singular := strings.TrimSuffix(segment, "S")
		return singular != tokenCountMarker &&
			(slices.Contains(secretShapedKeyMarkers, segment) || slices.Contains(secretShapedKeyMarkers, singular))
	})
}

// leafSecretKeyLeaksValue reports whether a key with a leaf-only marker holds a
// raw string directly.
func leafSecretKeyLeaksValue(key string, value any) bool {
	text, _ := value.(string)
	if !slices.ContainsFunc(keySegments(key), func(segment string) bool {
		return slices.Contains(leafSecretKeyMarkers, segment)
	}) {
		return false
	}
	return isUndeclaredSecretValue(text, false)
}

// looksLikeSecretShapedKey reports whether key names a secret, matching the
// markers against the key's camelCase- and separator-aware segments: accessToken
// matches TOKEN, apiKey matches KEY, and apiKeys matches the plural KEYS. A key
// with no marker segment, such as maxSize, is not secret-shaped. A run-together
// apikey has no segment boundary, so it is matched as a whole.
func looksLikeSecretShapedKey(key string) bool {
	switch normalizeToken(key) {
	case "apikey", "apikeys":
		// A run-together api-key name has no segment boundary for the marker
		// walk, so both the singular and the plural are matched whole.
		return true
	}
	return slices.ContainsFunc(keySegments(key), isSecretShapedSegment)
}

// isSecretShapedSegment reports whether an uppercased key segment is a marker,
// accepting the plural form as well: TOKENS matches TOKEN, so a plural secret
// list key such as tokens or refresh_tokens is secret-shaped. Trimming one
// trailing S folds the plural to its marker; the singular markers carry no
// trailing S, so a singular segment is unchanged.
func isSecretShapedSegment(segment string) bool {
	return slices.Contains(secretShapedKeyMarkers, segment) ||
		slices.Contains(secretShapedKeyMarkers, strings.TrimSuffix(segment, "S"))
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
	for _, r := range key {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r) && unicode.IsLower(previous):
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
		if nameValuePairLeaksValue(typed) {
			return true
		}
		for key, child := range typed {
			if entryLeaksValue(key, child) {
				return true
			}
		}
	case map[any]any:
		for key, child := range typed {
			keyString, _ := key.(string)
			if entryLeaksValue(keyString, child) {
				return true
			}
		}
	case []any:
		return argumentsLeakValue(typed) || slices.ContainsFunc(typed, assignmentLeaksValue) ||
			slices.ContainsFunc(typed, decodedHoldsUndeclaredSecretValue)
	case string:
		return looksLikeSecretValue(typed)
	}
	return false
}

// entryLeaksValue reports whether one mapping entry leaks a raw secret: a
// secret-shaped key holding one, an env table holding an entry not known to be
// configuration, or a secret-shaped value anywhere below it.
func entryLeaksValue(key string, child any) bool {
	if isSecretTable(key, child) {
		// An env or header table's entries are judged by its own rule alone.
		return tableLeaksValue(key, child)
	}
	return secretKeyLeaksValue(key, child) || leafSecretKeyLeaksValue(key, child) ||
		decodedHoldsUndeclaredSecretValue(child)
}

// secretKeyLeaksValue reports whether a secret-shaped key holds a raw secret
// value rather than a placeholder, a reference, an empty string, or a non-string
// scalar. An array or object under the key leaks when any descendant string is a
// raw secret, so a list such as {"apiKeys":["sk-raw"]} is refused.
func secretKeyLeaksValue(key string, value any) bool {
	return looksLikeSecretShapedKey(key) && holdsRawSecretString(value, !namesAStoredSecret(key))
}

// holdsRawSecretString reports whether node is, or contains at any depth, a
// non-empty string that is not a reference. It threads the secret-shaped key's
// context through its descendants so a collection under the key is inspected,
// not only a direct string value.
func holdsRawSecretString(node any, allowNumbers bool) bool {
	switch typed := node.(type) {
	case []any:
		return slices.ContainsFunc(typed, func(child any) bool { return holdsRawSecretString(child, allowNumbers) })
	case map[string]any:
		for _, child := range typed {
			if holdsRawSecretString(child, allowNumbers) {
				return true
			}
		}
	case map[any]any:
		for _, child := range typed {
			if holdsRawSecretString(child, allowNumbers) {
				return true
			}
		}
	case string:
		return isUndeclaredSecretValue(typed, allowNumbers)
	case int, int64, uint64, float64:
		// A number decoded as a number: a token count under TOKEN, otherwise a
		// stored secret such as a numeric password or PIN.
		return !allowNumbers
	}
	return false
}

// isUndeclaredSecretValue reports whether value is a non-empty string that is
// not a reference, so it would be a raw secret under a secret-shaped key. With
// allowNumbers, a number or boolean word is not one either: under a token count
// (MAX_THINKING_TOKENS: "31999"), never under a password, PIN, or key.
func isUndeclaredSecretValue(value string, allowNumbers bool) bool {
	if strings.TrimSpace(value) == "" || (allowNumbers && isNonSecretScalar(value)) {
		return false
	}
	return !isReferenceValue(value)
}
