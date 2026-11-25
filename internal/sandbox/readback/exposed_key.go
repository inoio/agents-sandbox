package readback

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ExposedKey is an API key a harness's own login stored in plain text in the
// sandbox home, where it bypasses the secret mechanism. It names the store and
// the env secret that replaces the key, never the key itself.
type ExposedKey struct {
	Source   string // sandbox-home-relative path of the store
	Provider string
	EnvVar   string   // the env secret that replaces the key
	Hosts    []string // the API hosts that secret is scoped to
	// Remedy is the step, after declaring the env secret, that takes the key
	// out of the store.
	Remedy string
}

// exposedKeyStore declares where one agent's login keeps an API key: a
// top-level string field of the JSON file at key.Source in the sandbox home.
type exposedKeyStore struct {
	agent string
	field string
	key   ExposedKey
}

// codexAuthPath is codex's login store, home-relative.
const codexAuthPath = ".codex/auth.json"

// exposedKeyStores lists the login stores that can hold a plain-text API key.
// codex's API-key login writes OPENAI_API_KEY into auth.json; its ChatGPT login
// leaves that field null. claude-code's /login with a Console key writes
// primaryApiKey into ~/.claude.json, its global config, and /logout clears it;
// its subscription login lives in .credentials.json instead.
var exposedKeyStores = []exposedKeyStore{
	{
		agent: "codex",
		field: "OPENAI_API_KEY",
		key: ExposedKey{
			Source:   codexAuthPath,
			Provider: "openai",
			EnvVar:   "OPENAI_API_KEY",
			Hosts:    []string{"api.openai.com"},
			Remedy: "then run `printenv OPENAI_API_KEY | codex login --with-api-key` in " +
				"`agents-sandbox shell --agent codex`, so auth.json stores the secret's placeholder instead",
		},
	},
	{
		agent: "claude-code",
		field: "primaryApiKey",
		key: ExposedKey{
			Source:   ".claude.json",
			Provider: "anthropic",
			EnvVar:   "ANTHROPIC_API_KEY",
			Hosts:    []string{"api.anthropic.com"},
			Remedy: "then run `/logout` in claude-code, so it uses the key from its environment instead " +
				"(it asks once to approve the key)",
		},
	},
}

// maxExposedKeyStoreBytes bounds the read of a store: the sandboxed agent
// controls the file. A larger file is not inspected.
const maxExposedKeyStoreBytes = 4 << 20

// exposedKeys reports the agent's login stores that hold a plain-text API key.
// A store is read only to decide that: the key is never kept. A store holding
// a secret placeholder (the documented route stores the env secret's guest
// value) is not exposed. A store that is missing, not a regular file (a symlink the agent
// planted could point at a host file), too large, or not a JSON object reports
// nothing.
func exposedKeys(home, agentName string) []ExposedKey {
	var exposed []ExposedKey
	for _, store := range exposedKeyStores {
		if store.agent != agentName {
			continue
		}
		if storeHoldsPlainTextKey(filepath.Join(home, store.key.Source), store.field) {
			exposed = append(exposed, store.key)
		}
	}
	return exposed
}

// storeHoldsPlainTextKey reports whether the JSON object at path holds a
// non-empty literal string in field.
func storeHoldsPlainTextKey(path, field string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	content, ok := ReadCappedFile(path, maxExposedKeyStoreBytes)
	if !ok {
		return false
	}
	var document map[string]any
	if json.Unmarshal(content, &document) != nil {
		return false
	}
	value, isString := document[field].(string)
	return isString && value != "" && !isSecretPlaceholder(value)
}

// msbDefaultPlaceholderPrefix starts the guest value microsandbox gives a secret
// that configures no placeholder of its own: $MSB_<SECRET_NAME>.
const msbDefaultPlaceholderPrefix = "$MSB_"

// isSecretPlaceholder reports whether a stored key is a secret's guest value
// rather than the key: microsandbox's default placeholder, or an explicit
// reference such as the ${ENVVAR} placeholder a managed config carries.
func isSecretPlaceholder(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), msbDefaultPlaceholderPrefix) || isExplicitReference(value)
}
