package readback

import (
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// unprovenLeafPath returns the key path of the first decoded leaf the guard
// cannot prove is not a secret, or "" when every leaf is proven safe. It is the
// fail-closed half of the undeclared-secret guard: a leaf is refused unless a
// rule clears it, so a credential in a spelling no rule anticipated is refused
// rather than copied. The shape rules (decodedHoldsUndeclaredSecretValue) still
// refuse a value that looks like a secret under a cleared key.
func unprovenLeafPath(node any) string {
	return firstUnprovenLeaf(node, nil)
}

// firstUnprovenLeaf walks node in a stable key order, carrying the key path.
func firstUnprovenLeaf(node any, path []string) string {
	switch typed := node.(type) {
	case map[string]any:
		for _, key := range sortedKeys(typed) {
			if found := firstUnprovenLeaf(typed[key], append(path, key)); found != "" {
				return found
			}
		}
		return ""
	case map[any]any:
		return firstUnprovenLeaf(stringKeyedEntries(typed), path)
	case []any:
		for index, child := range typed {
			if found := firstUnprovenLeaf(child, append(path, "["+strconv.Itoa(index)+"]")); found != "" {
				return found
			}
		}
		return ""
	}
	if leafIsProvablySafe(nearestKey(path), node) {
		return ""
	}
	if len(path) == 0 {
		return documentRootPath
	}
	return strings.Join(path, ".")
}

// documentRootPath names a document that is itself one unproven scalar, so the
// refusal is never the empty "nothing found" path.
const documentRootPath = "(document)"

// sortedKeys returns a mapping's keys sorted, so the reported path is stable.
func sortedKeys(entries map[string]any) []string {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// nearestKey is the last named key on path: a list element is judged by the key
// that holds the list.
func nearestKey(path []string) string {
	for _, element := range slices.Backward(path) {
		if !strings.HasPrefix(element, "[") {
			return element
		}
	}
	return ""
}

// leafIsProvablySafe reports whether one decoded leaf under key is proven not to
// be a secret. A null or boolean never is one. A number or a timestamp is safe
// unless the key names a stored secret. A string is safe when it is empty, a
// reference or a template whose only secret is a reference, a filesystem path —
// these hold under any key — or, under a key that names no secret, a number or
// boolean word, a URL without credentials or a query, or any value under a key
// the safe vocabulary clears.
func leafIsProvablySafe(key string, leaf any) bool {
	switch typed := leaf.(type) {
	case nil, bool:
		return true
	case string:
		return stringLeafIsProvablySafe(key, typed)
	}
	// A number under a count key (maxTokens) is configuration; under a key that
	// names a stored secret (pin, access_token) it is the secret.
	return !keyNamesAStoredSecret(key)
}

// stringLeafIsProvablySafe is leafIsProvablySafe for a string leaf. Under a
// secret-shaped key, only a whole reference, a placeholder template, a path, or
// an environment-variable name with an underscore (an apiKeys list of names,
// codex's env_key, kimi's api_key_env) clears.
func stringLeafIsProvablySafe(key, value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || isWholeReference(trimmed) || onlyPlaceholdersCarrySecrets(trimmed) ||
		isFilesystemPath(trimmed) {
		return true
	}
	if isNonSecretScalar(trimmed) && !keyNamesAStoredSecret(key) {
		return true
	}
	if keyNamesASecret(key) {
		return isEnvVarIdentifier(trimmed) && strings.Contains(trimmed, "_")
	}
	if embeddedURL.FindString(trimmed) == trimmed {
		// A URL is judged as one, under any key: a credential can sit in its
		// userinfo, query, or a token-like path segment (a webhook).
		return isPlainURL(trimmed)
	}
	return keyIsSafeVocabulary(key) && !holdsUserinfo(trimmed) && embeddedURLsArePlain(trimmed) &&
		embeddedPairsAreProvablySafe(trimmed)
}

// embeddedURL matches a URL inside a string, up to whitespace or a quote.
var embeddedURL = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s"'<>]+`)

// embeddedURLsArePlain reports whether every URL written inside a cleared
// string (a command line, prose) carries no credential.
func embeddedURLsArePlain(value string) bool {
	// gomutants:disable-next-line INTEGER_INCREMENT reason="FindAll* treats any negative limit as all matches, and the increment turns the literal 1 into 2, so -1 becomes -2"
	return !slices.ContainsFunc(embeddedURL.FindAllString(value, -1), func(found string) bool {
		return !isPlainURL(found)
	})
}

// embeddedPairsAreProvablySafe reports whether every key-value pair written
// inside a cleared string — a NAME=value assignment or a --flag=value, also
// when glued to shell punctuation or quoted — is itself provably safe, judged
// like a decoded entry. A command line, a permission rule, or prose cleared by
// its key can still set a secret ("DB_PASSWORD=… mytool", "export TOKEN=…",
// "true;API_KEY=…"), so its pairs are not cleared with it.
func embeddedPairsAreProvablySafe(value string) bool {
	unquoted := strings.NewReplacer(`"`, "", "'", "", "`", "").Replace(value)
	// gomutants:disable-next-line INTEGER_INCREMENT reason="FindAll* treats any negative limit as all matches, and the increment turns the literal 1 into 2, so -1 becomes -2"
	for _, match := range embeddedPairPattern.FindAllStringSubmatch(unquoted, -1) {
		if !pairIsProvablySafe(match[1], match[2]) {
			return false
		}
	}
	return true
}

// embeddedPairPattern matches NAME=value inside a string: a name that starts a
// word or follows shell punctuation, and a value up to whitespace or the next
// shell operator.
var embeddedPairPattern = regexp.MustCompile(`(?:^|[\s;&|()-])([A-Za-z_][A-Za-z0-9_.-]*)=([^\s;&|()]*)`)

// pairIsProvablySafe judges one embedded pair. A value that is itself an
// environment assignment (--env=NAME=value) is judged as that inner pair, and
// the outer name must not name a secret, so a secret-named outer key is never
// lost. Only a variable name counts as the inner name, so a value such as
// "hunter2-x=1" is not read as a pair named after the secret.
func pairIsProvablySafe(name, value string) bool {
	if innerName, innerValue, isPair := strings.Cut(value, "="); isPair && environmentName.MatchString(innerName) {
		return !keyNamesASecret(name) && pairIsProvablySafe(innerName, innerValue)
	}
	return stringLeafIsProvablySafe(name, value)
}

// environmentName matches an environment-variable name.
var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// holdsUserinfo reports whether value starts with a user:password@ part, a
// credential a cleared key (a host) can carry without a scheme.
func holdsUserinfo(value string) bool {
	return userinfoPrefix.MatchString(value)
}

var userinfoPrefix = regexp.MustCompile(`^[^\s/@:]*:[^\s/@]+@`)

// isWholeReference reports whether value is, as a whole, a reference the
// template rule does not cover: microsandbox's $MSB_<NAME>, or a leading-!
// command reference. A ${VAR}, {env:VAR}, or {file:path} placeholder, alone or
// beside other text, goes through the template rule.
func isWholeReference(value string) bool {
	return msbDefaultPlaceholder.MatchString(value) || strings.HasPrefix(value, "!")
}

// keyNamesASecret reports whether key names a secret by a marker segment, a
// plural one included (tokens), or a leaf-only marker (auth, cookie, session).
func keyNamesASecret(key string) bool {
	return looksLikeSecretShapedKey(key) || keyHasLeafSecretMarker(key)
}

// keyNamesAStoredSecret reports whether key names a secret a number can hold
// (a pin, password, or access_token) rather than a token count, or carries a
// leaf-only marker.
func keyNamesAStoredSecret(key string) bool {
	return namesAStoredSecret(key) || keyHasLeafSecretMarker(key)
}

// keyHasLeafSecretMarker reports whether one of key's segments is a leaf-only
// secret marker.
func keyHasLeafSecretMarker(key string) bool {
	return slices.ContainsFunc(keySegments(key), func(segment string) bool {
		return slices.Contains(leafSecretKeyMarkers, segment)
	})
}

// isPlainURL reports whether value is an http(s) or ws(s) URL that carries no
// credential: no userinfo, no query, no fragment, and no token-like path
// segment.
func isPlainURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" {
		return false
	}
	switch parsed.Scheme {
	case "http", "https", "ws", "wss":
	default:
		return false
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery {
		return false
	}
	return !slices.ContainsFunc(strings.Split(parsed.Path, "/"), isTokenLikeSegment)
}

// isTokenLikeSegment reports whether a URL path segment can be a credential: a
// long run of token characters, or a shorter one mixing letters and digits (a
// webhook's T0123ABCD/B0456EFGH/… or a bot token). A version (v1), a word, or a
// short id passes.
func isTokenLikeSegment(segment string) bool {
	if len(segment) >= literalSecretLength {
		return true
	}
	return len(segment) >= 8 && strings.ContainsAny(segment, "0123456789") &&
		strings.ContainsFunc(segment, unicode.IsLetter)
}

// safeKeyWords are the final key segments that name configuration, never a
// credential: a model, a name, a location, prose, a command line, or a mode.
// A key is cleared by its last segment, so OTEL_METRICS_EXPORTER and
// ANTHROPIC_MODEL are configuration while DB_PASSWORD is not. A key that also
// carries a secret marker is never cleared by this list.
var safeKeyWords = []string{
	// What runs and how it is presented.
	"MODEL", "MODELS", "PROVIDER", "PROVIDERS", "AGENT", "VARIANT", "EFFORT",
	"NAME", "TITLE", "DESCRIPTION", "THEME", "STYLE", "LAYOUT", "ICON", "COLOR",
	"MODE", "ENV", "TYPE", "KIND", "LEVEL", "FORMAT", "LANGUAGE", "LOCALE", "VERSION", "BETA", "SCHEMA",
	// Prose the user wrote for the agent.
	"PROMPT", "INSTRUCTION", "INSTRUCTIONS",
	// Where things are.
	pathKeyWord, "PATHS", "DIR", "DIRS", "DIRECTORY", "FILE", "FILES", "URL", "URLS", "ENDPOINT", "HOST", "PORT",
	"REGION", "PROFILE", "PROTOCOL", "EXPORTER", "ID",
	// Command lines and permission rules; the shape rules still judge them.
	"COMMAND", "ARGS", "OPTIONS", "ALLOW", "DENY", "ASK", "TOOLSETS", "SHELL", "EDITOR",
}

// pathKeyWord is the key segment that names a location (PATH, SSH_KEY_PATH).
const pathKeyWord = "PATH"

// keyIsSafeVocabulary reports whether key's last segment is a safe key word.
func keyIsSafeVocabulary(key string) bool {
	segments := keySegments(key)
	return len(segments) > 0 && slices.Contains(safeKeyWords, segments[len(segments)-1])
}
