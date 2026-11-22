package readback

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// secretValuePatterns match a credential by the shape of the value itself, so a
// secret is caught even under a key that names none: a header credential, a URL
// with userinfo, or a token whose vendor prefix is well known (also inside a
// command such as "echo sk-...").
var secretValuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(proxy-)?authorization\s*[:=]`),
	regexp.MustCompile(`(?i)\b(x-)?api-?key\s*[:=]`),
	regexp.MustCompile(`(?i)\b(x-)?auth-token\s*[:=]`),
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/-]{8,}`),
	regexp.MustCompile(`(?i)\bbasic\s+[A-Za-z0-9+/]{8,}={0,2}`),
	regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s/@:]+:[^\s/@]+@`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}`),
	regexp.MustCompile(`\b(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}`),
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
	return false
}

// envTableKeys are the keys whose table sets environment variables for the
// agent or a server it starts (Claude Code's and Qwen Code's "env", an MCP
// server's "env" or "environment"). Such a table is the usual place for a key,
// under names no rule can enumerate, so its entries are secret by default.
var envTableKeys = []string{"env", "environment"}

func isEnvTableKey(key string) bool {
	for _, name := range envTableKeys {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

// safeEnvKeySuffixes and safeEnvKeyPrefixes name environment variables that
// carry configuration, not a credential: endpoints, model and region choices,
// limits, switches. A key that also names a secret, or a value shaped like one,
// is never safe.
var (
	safeEnvKeySuffixes = []string{
		"_URL", "_ENDPOINT", "_HOST", "_PORT", "_MODEL", "_REGION", "_VERSION", "_TIMEOUT", "_TIMEOUT_MS",
		"_LEVEL", "_MODE", "_DIR", "_PATH", "_HOME", "_LOCALE", "_FORMAT", "_ENABLED", "_DISABLED",
	}
	safeEnvKeyPrefixes = []string{"DISABLE_", "ENABLE_", "NO_", "USE_", "SKIP_"}
	safeEnvKeys        = []string{
		"HTTP_PROXY",
		"HTTPS_PROXY",
		"NO_PROXY",
		"ALL_PROXY",
		"TZ",
		"LANG",
		"DEBUG",
		"NODE_ENV",
	}
)

// envEntryIsSafe reports whether an env-table entry is configuration rather than
// a credential: a reference, a numeric or boolean value (MAX_THINKING_TOKENS),
// or a value under a key known to carry configuration. A secret-shaped value is
// never safe, and neither is any other value under a secret-named key.
func envEntryIsSafe(key, value string) bool {
	if looksLikeSecretValue(value) {
		return false
	}
	if isReferenceValue(value) || strings.TrimSpace(value) == "" || isNonSecretScalar(value) {
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

// isNonSecretScalar reports whether value is a number or a boolean word, which
// config files hold as strings (Claude Code requires env values to be strings)
// and which is never a credential.
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

// envTableLeaksValue reports whether key names an env table that holds an entry
// envEntryIsSafe does not clear. A non-string entry is not a credential.
func envTableLeaksValue(key string, value any) bool {
	if !isEnvTableKey(key) {
		return false
	}
	for entryKey, entryValue := range stringKeyedEntries(value) {
		if text, ok := entryValue.(string); ok && !envEntryIsSafe(entryKey, text) {
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
