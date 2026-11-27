package readback

import (
	"encoding/json"
	"strings"
)

// ExposedKey is an API key a harness's own login stored in plain text in the
// sandbox home, where it bypasses the secret mechanism. It names the store, the
// env secret that replaces the key, and how the store is provisioned instead.
// Value is the key, set in memory only so the proposal can move it into the
// secret; it never reaches a manifest or an artefact.
//
//exhaustruct:ignore
type ExposedKey struct {
	Source   string // sandbox-home-relative path of the store
	Provider string
	EnvVar   string   // the env secret that replaces the key
	Hosts    []string // the API hosts that secret is scoped to
	Value    string   // the key itself; in memory only
	// ProvisionedStore is the store the launcher provisions from the host in
	// its place: a format with one %q for the secret's guest placeholder. It is
	// empty for a harness that reads the key from its environment, which needs
	// no file.
	ProvisionedStore string
	// FollowUp is a step the user takes once, inside the VM, for what the
	// launcher cannot change in an existing home volume.
	FollowUp string
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
			Source:           codexAuthPath,
			Provider:         "openai",
			EnvVar:           "OPENAI_API_KEY",
			Hosts:            []string{"api.openai.com"},
			ProvisionedStore: `{"auth_mode":"apikey","OPENAI_API_KEY":%q}` + "\n",
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
			FollowUp: "Run /logout once in claude-code, so it drops the stored key and uses the secret.",
		},
	},
}

// maxExposedKeyStoreBytes bounds the read of a store: the sandboxed agent
// controls the file. A larger file is not inspected.
//
// gomutants:disable-next-line INTEGER_INCREMENT,INTEGER_DECREMENT reason="the bound's exact size is no contract; the tests pin its behaviour at and past whatever the constant is"
const maxExposedKeyStoreBytes = 4 << 20

// exposedKeys reports the agent's login stores that hold a plain-text API key,
// with the key in memory. A store holding a secret placeholder (the documented
// route and the launcher's provisioned store hold the env secret's guest
// value) is not exposed.
func exposedKeys(home, agentName string) []ExposedKey {
	var exposed []ExposedKey
	for _, store := range exposedKeyStores {
		if store.agent != agentName {
			continue
		}
		if value, ok := plainTextKey(home, store.key.Source, store.field); ok {
			key := store.key
			key.Value = value
			exposed = append(exposed, key)
		}
	}
	return exposed
}

// plainTextKey returns the non-empty literal string in field of the JSON object
// at source, relative to home, and whether it holds one. The sandboxed agent
// controls the home, so the read stays inside it (a symlinked file or parent
// that leads out of it is refused), never blocks (a FIFO opens non-blocking),
// takes only a regular file, as checked on the opened file, and is bounded. A
// store that fails any of these reads as nothing, and nothing, like anything
// that is not a JSON object, decodes to no field. A secret's placeholder is no
// plain-text key.
func plainTextKey(home, source, field string) (string, bool) {
	content, _ := ReadHomeFile(home, source, maxExposedKeyStoreBytes)
	var document map[string]any
	// A failed decode leaves document nil, which holds no field.
	_ = json.Unmarshal(content, &document)
	value, _ := document[field].(string)
	if value == "" || isSecretPlaceholder(value) {
		return "", false
	}
	return value, true
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
