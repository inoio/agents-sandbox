package readback

import (
	"encoding/json"
	"os"
	"strings"
	"syscall"
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
			Remedy:   "then run `/logout` in claude-code, so it uses the key from its environment instead",
		},
	},
}

// maxExposedKeyStoreBytes bounds the read of a store: the sandboxed agent
// controls the file. A larger file is not inspected.
//
// gomutants:disable-next-line INTEGER_INCREMENT,INTEGER_DECREMENT reason="the bound's exact size is no contract; the tests pin its behaviour at and past whatever the constant is"
const maxExposedKeyStoreBytes = 4 << 20

// exposedKeys reports the agent's login stores that hold a plain-text API key.
// A store is read only to decide that: the key is never kept. A store holding
// a secret placeholder (the documented route stores the env secret's guest
// value) is not exposed.
func exposedKeys(home, agentName string) []ExposedKey {
	var exposed []ExposedKey
	for _, store := range exposedKeyStores {
		if store.agent != agentName {
			continue
		}
		if storeHoldsPlainTextKey(home, store.key.Source, store.field) {
			exposed = append(exposed, store.key)
		}
	}
	return exposed
}

// storeHoldsPlainTextKey reports whether the JSON object at source, relative
// to home, holds a non-empty literal string in field. The sandboxed agent
// controls the home, so the read stays inside it (a symlinked file or parent
// that leads out of it is refused), never blocks (a FIFO opens non-blocking),
// takes only a regular file, as checked on the opened file, and is bounded. A
// store that fails any of these reads as nothing, and nothing, like anything
// that is not a JSON object, decodes to no field.
func storeHoldsPlainTextKey(home, source, field string) bool {
	var document map[string]any
	// A failed decode leaves document nil, which holds no field.
	_ = json.Unmarshal(readStoreInHome(home, source), &document)
	value, _ := document[field].(string)
	return value != "" && !isSecretPlaceholder(value)
}

// readStoreInHome reads source under home within the bounds
// storeHoldsPlainTextKey describes, or returns nil.
func readStoreInHome(home, source string) []byte {
	root, err := os.OpenRoot(home)
	if err != nil {
		return nil
	}
	defer func() { _ = root.Close() }()
	// gomutants:disable-next-line INTEGER_INCREMENT reason="the permission is used only with O_CREATE, which this read-only open never passes"
	file, err := root.OpenFile(source, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	// gomutants:disable-next-line BRANCH_IF reason="a failed open leaves file nil, whose Stat fails with ErrInvalid, so the next check refuses it too"
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	// gomutants:disable-next-line BRANCH_IF,EXPRESSION_REMOVE,INVERT_LOGICAL reason="defence in depth: a non-regular file in a temp home reads as nothing anyway (a directory fails with EISDIR, a FIFO without a writer reads EOF); the check refuses a FIFO a writer feeds, which a test cannot stage without racing"
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	content, _ := readCapped(file, maxExposedKeyStoreBytes)
	return content
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
