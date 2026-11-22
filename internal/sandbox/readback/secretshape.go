package readback

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// tokenChars is the character class of a credential token in a header, a query,
// or a command-line flag.
//
//nolint:gosec // a regexp character class, not a credential
const tokenChars = `[A-Za-z0-9._~+/=-]`

// secretValuePatterns match a credential by the shape of the value itself, so a
// secret is caught even under a key that names none. Each pattern requires a
// literal token where a placeholder could stand, so a correctly placeholdered
// value ("Authorization=Bearer ${TOKEN}") is not a secret.
var secretValuePatterns = []*regexp.Regexp{
	// A credential header: authorization (any scheme), an API key, an auth token.
	regexp.MustCompile(`(?i)\b(proxy-)?authorization\s*[:=]\s*((bearer|basic|token)\s+)?` + tokenChars + `{8,}`),
	regexp.MustCompile(`(?i)\b(x-)?api-?key\s*[:=]\s*` + tokenChars + `{8,}`),
	regexp.MustCompile(`(?i)\bx-auth-token\s*[:=]\s*` + tokenChars + `{8,}`),
	// A URL with userinfo: a password (possibly with an empty user or a '/'), or
	// a long token as the user name.
	regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s@?#'"]*:[^\s@?#'"$\{][^\s@?#'"]*@`),
	regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[A-Za-z0-9._~-]{12,}@`),
	// A credential in a query string or a key=value connection string.
	regexp.MustCompile(
		`(?i)[?&;](api[_-]?key|apikey|access[_-]?token|auth[_-]?token|token|key|secret|client[_-]?secret|` +
			`password|passwd|pwd|auth|sig|signature)=[^&\s'"$\{]`,
	),
	regexp.MustCompile(`(?i)(^|[\s;])(password|passwd|pwd)=[^\s;'"$\{]`),
	// A credential passed as a command-line flag value in the same string.
	regexp.MustCompile(`(?i)(^|\s)--?[a-z-]*(token|password|passwd|api-?key|secret|auth)(=|\s+)[^\s'"$\{-]`),
	// Well-known vendor token prefixes, also inside a command ("echo sk-...").
	regexp.MustCompile(`\b(sk|rk)_(live|test)_[A-Za-z0-9]{10,}`),
	regexp.MustCompile(`\b(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}`),
	regexp.MustCompile(`\bxai-[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`\b(hf|gsk|npm)_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`\bpypi-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
}

// digitTokenPatterns match a token only when it carries a digit or base64
// padding, so a word in prose ("bearer authentication", "basic functionality",
// "sk-learn-benchmarks") is not a secret.
var digitTokenPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bbearer\s+(` + tokenChars + `{8,})`),
	regexp.MustCompile(`(?i)\bbasic\s+([A-Za-z0-9+/]{12,}={0,2})`),
	regexp.MustCompile(`\bsk-([A-Za-z0-9_-]{8,})`),
}

// looksLikeSecretValue reports whether value has the shape of a credential
// regardless of the key it sits under. A placeholder or reference matches none
// of the shapes, so it needs no separate test (and a bare upper-case token such
// as an AWS key id would otherwise pass as an env-var reference).
func looksLikeSecretValue(value string) bool {
	for _, pattern := range secretValuePatterns {
		if pattern.MatchString(value) {
			return true
		}
	}
	for _, pattern := range digitTokenPatterns {
		// gomutants:disable-next-line INTEGER_INCREMENT reason="FindAll* treats any negative limit as all matches, and the increment turns the literal 1 into 2, so -1 becomes -2"
		for _, match := range pattern.FindAllStringSubmatch(value, -1) {
			// gomutants:disable-next-line INTEGER_DECREMENT reason="the whole match adds only the scheme word or sk- prefix, which holds no digit or '='"
			if strings.ContainsAny(match[1], "0123456789=") {
				return true
			}
		}
	}
	return false
}

// argumentsLeakValue reports whether a list of command arguments passes a
// literal credential through a flag that names a secret: "--access-token abc"
// as two elements, or "--client-secret=abc" as one. A flag naming where a
// secret lives (--token-file, --password-stdin) passes none.
func argumentsLeakValue(arguments []any) bool {
	for index, argument := range arguments {
		flag, _ := argument.(string)
		if !strings.HasPrefix(flag, "-") {
			continue
		}
		name, value, joined := strings.Cut(strings.TrimLeft(flag, "-"), "=")
		if !flagNamesASecret(name) {
			continue
		}
		if !joined {
			next, hasNext := "", index+1 < len(arguments)
			if hasNext {
				next, _ = arguments[index+1].(string)
			}
			value = next
		}
		if literalCredential(value) {
			return true
		}
	}
	return false
}

// flagNamesASecret reports whether a flag name names a secret itself, not a
// place one is read from.
func flagNamesASecret(name string) bool {
	if !looksLikeSecretShapedKey(name) && !leafMarkerName(name) {
		return false
	}
	segments := keySegments(name)
	last := segments[len(segments)-1]
	return !slices.Contains([]string{"FILE", "PATH", "ENV", "STDIN", "DIR", "NAME", "HELPER", "COMMAND", "CMD"}, last)
}

func leafMarkerName(name string) bool {
	return slices.ContainsFunc(keySegments(name), func(segment string) bool {
		return slices.Contains(leafSecretKeyMarkers, segment)
	})
}

// literalCredential reports whether value would be a literal credential: not
// empty, not a reference, and not another flag.
func literalCredential(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && !strings.HasPrefix(trimmed, "-") && !isPlaceholderReference(trimmed)
}

// assignmentPattern matches an environment assignment NAME=value.
var assignmentPattern = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)

// assignmentLeaksValue reports whether node is a NAME=value string (an env
// array entry, a docker "-e" argument) whose value the env-table rule does not
// clear.
func assignmentLeaksValue(node any) bool {
	text, _ := node.(string)
	match := assignmentPattern.FindStringSubmatch(text)
	return match != nil && !envEntryIsSafe(match[1], match[2])
}

// nameValuePairLeaksValue reports whether a mapping is a {name, value} env
// entry (the Kubernetes-style list form) whose value the env-table rule does
// not clear. The value is judged on its raw form: a number decoded as a
// float64 is a stored secret under a secret-named key, so the string rule alone
// would clear it.
func nameValuePairLeaksValue(entry map[string]any) bool {
	name, hasName := entry["name"].(string)
	if !hasName {
		return false
	}
	if value, isString := entry["value"].(string); isString {
		return !envEntryIsSafe(name, value)
	}
	return holdsRawSecretString(entry["value"], !namesAStoredSecret(name))
}

// isPlaceholderReference reports whether value is an explicit reference to a
// secret held elsewhere: a ${VAR} placeholder, or opencode's {env:VAR} and
// {file:path} forms. A table that sets variables holds literals, so a bare
// upper-case word there is a value, not a variable name.
func isPlaceholderReference(value string) bool {
	trimmed := strings.TrimSpace(value)
	return strings.HasPrefix(trimmed, "${") || strings.HasPrefix(trimmed, "{env:") ||
		strings.HasPrefix(trimmed, "{file:")
}

// envTableKeys are the keys whose table sets environment variables for the
// agent or a server it starts (Claude Code's and Qwen Code's "env", an MCP
// server's "env" or "environment"). Such a table is the usual place for a key,
// under names no rule can enumerate, so its entries are secret by default.
var envTableKeys = []string{"env", "environment"}

// headerTableKeys are the keys whose table sets HTTP headers (an MCP server's
// "headers", codex's "http_headers"). A header can carry a credential under
// any name, so its entries are secret by default too.
var headerTableKeys = []string{"headers", "http_headers"}

// safeHeaderNames are headers that carry no credential.
var safeHeaderNames = []string{
	"accept", "accept-encoding", "accept-language", "content-type", "user-agent", "anthropic-beta",
	"anthropic-version", "openai-beta", "x-request-id", "cache-control", "content-length",
}

func keyIsOneOf(key string, names []string) bool {
	return slices.ContainsFunc(names, func(name string) bool { return strings.EqualFold(key, name) })
}

// safeEnvKeySuffixes, safeEnvKeyPrefixes, and safeEnvKeys name environment
// variables that carry configuration, not a credential: endpoints, model and
// region choices, limits, switches, search paths. A value shaped like a secret
// is never safe under them.
var (
	safeEnvKeySuffixes = []string{
		"_URL", "_ENDPOINT", "_HOST", "_PORT", "_MODEL", "_REGION", "_VERSION", "_TIMEOUT", "_TIMEOUT_MS",
		"_LEVEL", "_MODE", "_DIR", "PATH", "_HOME", "_LOCALE", "_FORMAT", "_ENABLED", "_DISABLED",
		"_OPTIONS", "_PROFILE", "_PROJECT", "_PROJECT_ID", "_NAME", "_PROTOCOL", "_EXPORTER", "_TOOLSETS",
	}
	safeEnvKeyPrefixes = []string{"DISABLE_", "ENABLE_", "NO_", "USE_", "SKIP_"}
	safeEnvKeys        = []string{
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "TZ", "LANG", "DEBUG", "NODE_ENV", "HOME",
	}
)

// envEntryIsSafe reports whether an env-table entry is configuration rather than
// a credential: an explicit reference, empty, a filesystem path, a number or
// boolean word under a key that names no stored secret (MAX_THINKING_TOKENS), or
// a value under a key known to carry configuration. A secret-shaped value is
// never safe, and neither is any other value under a secret-named key.
func envEntryIsSafe(key, value string) bool {
	if looksLikeSecretValue(value) {
		return false
	}
	if strings.TrimSpace(value) == "" || isFilesystemPath(value) || onlyPlaceholdersCarrySecrets(value) {
		return true
	}
	if isNonSecretScalar(value) && !namesAStoredSecret(key) {
		return true
	}
	if looksLikeSecretShapedKey(key) {
		return false
	}
	upper := strings.ToUpper(key)
	if slices.Contains(safeEnvKeys, upper) {
		return true
	}
	for _, suffix := range safeEnvKeySuffixes {
		if strings.HasSuffix(upper, suffix) {
			return true
		}
	}
	for _, prefix := range safeEnvKeyPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}

// headerEntryIsSafe reports whether a header-table entry carries no credential:
// a value carried by placeholders only, empty, or a header known to carry none (a number is a
// credential under any other header: an API key can be numeric).
func headerEntryIsSafe(name, value string) bool {
	if looksLikeSecretValue(value) {
		return false
	}
	if strings.TrimSpace(value) == "" || onlyPlaceholdersCarrySecrets(value) {
		return true
	}
	return keyIsOneOf(name, safeHeaderNames)
}

// placeholder matches an explicit reference inside a longer value.
var placeholder = regexp.MustCompile(`\$\{[^}]*\}|\{(env|file):[^}]*\}`)

// tokenRun matches a run of token characters long enough to be a credential.
var tokenRun = regexp.MustCompile(`[A-Za-z0-9_-]{8,}`)

// literalSecretLength is the length from which a token run left beside a
// placeholder counts as a literal secret even without a digit.
const literalSecretLength = 16

// onlyPlaceholdersCarrySecrets reports whether value holds a placeholder and,
// with every placeholder removed, no token-like run with a digit, and none of
// literalSecretLength or more, is left: a
// header line built around a placeholder ("x-api-key: ${X_API_KEY}") carries
// its credential only through the reference.
func onlyPlaceholdersCarrySecrets(value string) bool {
	if !placeholder.MatchString(value) {
		return false
	}
	// gomutants:disable-next-line INTEGER_INCREMENT reason="FindAll* treats any negative limit as all matches, and the increment turns the literal 1 into 2, so -1 becomes -2"
	for _, run := range tokenRun.FindAllString(placeholder.ReplaceAllString(value, ""), -1) {
		if strings.ContainsAny(run, "0123456789") || len(run) >= literalSecretLength {
			return false
		}
	}
	return true
}

// isSecretTable reports whether key names an env or header table holding a
// mapping, which its own rule judges.
func isSecretTable(key string, value any) bool {
	return (keyIsOneOf(key, envTableKeys) || keyIsOneOf(key, headerTableKeys)) && stringKeyedEntries(value) != nil
}

// isFilesystemPath reports whether value is an absolute or home-relative path:
// configuration (a key file's location), never the key itself.
func isFilesystemPath(value string) bool {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "/") && !strings.HasPrefix(trimmed, "~/") {
		return false
	}
	// A path has at least two components and path characters only; base64
	// (which can start with '/') carries '+' or '=' or has a single slash.
	return strings.Count(trimmed, "/") >= 2 && filesystemPathChars.MatchString(trimmed)
}

var filesystemPathChars = regexp.MustCompile(`^[A-Za-z0-9._~/-]+$`)

// isNonSecretScalar reports whether value is a number or a boolean word, which
// config files hold as strings (Claude Code requires env values to be strings).
func isNonSecretScalar(value string) bool {
	trimmed := strings.TrimSpace(value)
	// gomutants:disable-next-line INTEGER_INCREMENT,INTEGER_DECREMENT reason="the bit size only bounds precision; any number parses at both sizes"
	if _, err := strconv.ParseFloat(trimmed, 64); err == nil {
		return true
	}
	switch strings.ToLower(trimmed) {
	case "true", "false", "yes", "no", "on", "off":
		return true
	}
	return false
}

// tableLeaksValue reports whether key names an env or header table that holds
// an entry its rule does not clear. A nested table or list under it holds no
// configuration names, so any raw value inside it is a credential.
func tableLeaksValue(key string, value any) bool {
	var entryIsSafe func(name, value string) bool
	switch {
	case keyIsOneOf(key, envTableKeys):
		entryIsSafe = envEntryIsSafe
	case keyIsOneOf(key, headerTableKeys):
		entryIsSafe = headerEntryIsSafe
	default:
		return false
	}
	for entryKey, entryValue := range stringKeyedEntries(value) {
		switch typed := entryValue.(type) {
		case string:
			if !entryIsSafe(entryKey, typed) {
				return true
			}
		default:
			// A nested table or list holds no configuration names, so any raw
			// value inside it is a credential.
			if holdsRawSecretString(typed, false) {
				return true
			}
		}
	}
	return false
}

// stringKeyedEntries returns a decoded table's entries with string keys, for
// both decoded mapping shapes; anything else has none.
func stringKeyedEntries(node any) map[string]any {
	switch typed := node.(type) {
	case map[string]any:
		return typed
	case map[any]any:
		entries := map[string]any{}
		for key, child := range typed {
			if keyString, ok := key.(string); ok {
				entries[keyString] = child
			}
		}
		return entries
	}
	return nil
}
