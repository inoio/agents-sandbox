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
	regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s@?#'"]*:[^\s@?#'"]*@`),
	regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[A-Za-z0-9._~-]{12,}@`),
	// A credential in a query string or a key=value connection string.
	regexp.MustCompile(
		`(?i)[?&;](api[_-]?key|apikey|access[_-]?token|auth[_-]?token|token|key|secret|client[_-]?secret|` +
			`password|passwd|pwd|auth|sig|signature)=[^&\s'"$\{]`,
	),
	regexp.MustCompile(`(?i)(^|[\s;])(password|passwd|pwd)=[^\s;'"$\{]`),
	// A credential passed as a command-line flag value in the same string.
	regexp.MustCompile(`(?i)(^|\s)--?(token|password|passwd|api-?key|secret|auth)(=|\s+)[^\s'"$\{-]`),
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
		for _, match := range pattern.FindAllStringSubmatch(value, -1) {
			if strings.ContainsAny(match[1], "0123456789=") {
				return true
			}
		}
	}
	return false
}

// credentialFlags are command-line flags whose next argument is a credential.
var credentialFlags = []string{"--token", "--password", "--passwd", "--api-key", "--apikey", "--secret", "--auth"}

// argumentsLeakValue reports whether a list of command arguments passes a
// literal credential after a credential flag (["--token", "abc..."]).
func argumentsLeakValue(arguments []any) bool {
	for index := 0; index+1 < len(arguments); index++ {
		flag, isFlag := arguments[index].(string)
		value, isValue := arguments[index+1].(string)
		if !isFlag || !isValue || !slices.Contains(credentialFlags, strings.ToLower(flag)) {
			continue
		}
		if !isPlaceholderReference(value) && strings.TrimSpace(value) != "" && !strings.HasPrefix(value, "-") {
			return true
		}
	}
	return false
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
	"anthropic-version", "openai-beta", "x-request-id", "cache-control",
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
	if isPlaceholderReference(value) || strings.TrimSpace(value) == "" || isFilesystemPath(value) ||
		onlyPlaceholdersCarrySecrets(value) {
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
// an explicit reference, empty, a number, or a header known to carry none.
func headerEntryIsSafe(name, value string) bool {
	if looksLikeSecretValue(value) {
		return false
	}
	if isPlaceholderReference(value) || strings.TrimSpace(value) == "" || isNonSecretScalar(value) ||
		onlyPlaceholdersCarrySecrets(value) {
		return true
	}
	return keyIsOneOf(name, safeHeaderNames)
}

// placeholder matches an explicit reference inside a longer value.
var placeholder = regexp.MustCompile(`\$\{[^}]*\}|\{(env|file):[^}]*\}`)

// tokenRun matches a run of token characters long enough to be a credential.
var tokenRun = regexp.MustCompile(`[A-Za-z0-9_-]{8,}`)

// onlyPlaceholdersCarrySecrets reports whether value holds a placeholder and,
// with every placeholder removed, no token-like run with a digit is left: a
// header line built around a placeholder ("x-api-key: ${X_API_KEY}") carries
// its credential only through the reference.
func onlyPlaceholdersCarrySecrets(value string) bool {
	if !placeholder.MatchString(value) {
		return false
	}
	for _, run := range tokenRun.FindAllString(placeholder.ReplaceAllString(value, ""), -1) {
		if strings.ContainsAny(run, "0123456789") {
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

// isFilesystemPath reports whether value is an absolute or home-relative path
// without spaces: configuration (a key file's location), never the key itself.
func isFilesystemPath(value string) bool {
	trimmed := strings.TrimSpace(value)
	return (strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "~/")) && !strings.ContainsAny(trimmed, " \t")
}

// isNonSecretScalar reports whether value is a number or a boolean word, which
// config files hold as strings (Claude Code requires env values to be strings).
func isNonSecretScalar(value string) bool {
	trimmed := strings.TrimSpace(value)
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
// an entry its rule does not clear. A non-string entry is not a credential.
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
		if text, ok := entryValue.(string); ok && !entryIsSafe(entryKey, text) {
			return true
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
