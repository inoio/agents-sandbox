package readback

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// CredentialKind classifies a credential the read-back found: a static secret
// whose value is extracted into memory, or a login whose value is never read.
type CredentialKind int

const (
	// CredentialStatic is a secret written as a literal (a dotenv entry, an
	// inline API key). Its value is read into Credential.Value, in memory only.
	CredentialStatic CredentialKind = iota
	// CredentialLogin is an OAuth or session credential. Its presence, source,
	// and provider are recorded; its value is never read out of the store.
	CredentialLogin
)

// Credential is one credential the read-back found in the home volume. Value is
// set for a static credential only, stays in memory, and never reaches a
// manifest or a persisted artefact.
//
//exhaustruct:ignore
type Credential struct {
	Kind     CredentialKind
	Source   string // sandbox-home-relative path, or "<agent>:<probe>"
	Provider string
	Field    string   // header-field hint, optional
	Hosts    []string // endpoints named for the entry, optional
	Value    string   // static only; never in a Manifest
}

// ReadBack is the result of reading one agent's home back: the declared config
// paths that exist, the credentials found, the keys changed since the baseline
// manifest, and the credential probes whose store could not be read this run.
type ReadBack struct {
	ConfigPaths []string
	Static      []Credential
	Login       []Credential
	Changed     []string
	// Unreadable names the probe sources (e.g. "opencode2:opencode2-sqlite")
	// whose read failed this run. A non-empty entry means the absence of that
	// source's credentials is undetermined, not a confirmed "no credentials".
	Unreadable []string
}

// Read inspects the agent's declared config and credential paths under home and
// reports what it found, comparing the current manifest against baseline. An
// agent without a ReadBackDescriber (unknown, or not yet verified) yields an
// empty ReadBack, never an error.
func Read(home, agentName string, baseline Manifest) (ReadBack, error) {
	a, ok := agent.Lookup(agentName)
	if !ok {
		return ReadBack{}, nil
	}
	if _, ok := agent.AsReadBackDescriber(a); !ok {
		return ReadBack{}, nil
	}

	collection, err := collectReadBack(home, agentName)
	if err != nil {
		return ReadBack{}, err
	}

	readBack := ReadBack{
		ConfigPaths: nil,
		Static:      nil,
		Login:       nil,
		Changed:     collection.manifest.Changed(baseline),
		Unreadable:  nil,
	}
	for _, entry := range collection.manifest.Entries {
		if isProbe(entry) {
			// A credential probe's entry carries no path: its outcome supplies
			// the credentials below.
			continue
		}
		loginStore, isLogin := loginCredentialStores[entry.Key]
		if isLogin && loginStore.appliesTo(agentName) {
			readBack.Login = append(readBack.Login, Credential{
				Kind:     CredentialLogin,
				Source:   entry.Key,
				Provider: loginStore.provider,
			})
		}
		if filepath.Base(entry.Key) == dotenvFileName {
			content, ok := readCappedFile(filepath.Join(home, entry.Key), maxHashedBytes)
			if !ok {
				continue
			}
			readBack.Static = append(readBack.Static, staticCredentials(entry.Key, content)...)
			continue
		}
		// A login store is also a config fact: it appears in ConfigPaths too.
		readBack.ConfigPaths = append(readBack.ConfigPaths, entry.Key)
	}
	readBack.appendProbeCredentials(collection.probes)
	sort.Strings(readBack.ConfigPaths)
	sort.Strings(readBack.Unreadable)
	return readBack, nil
}

// appendProbeCredentials reports the credentials each credential probe found and
// names the probe sources whose store was unreadable. The outcomes come from the
// same pass that produced the manifest digest, so the two cannot disagree.
func (r *ReadBack) appendProbeCredentials(probes []probeOutcome) {
	for _, probe := range probes {
		if probe.unreadable {
			r.Unreadable = append(r.Unreadable, probe.source)
		}
		for _, credential := range probe.credentials {
			if credential.Kind == CredentialLogin {
				r.Login = append(r.Login, credential)
				continue
			}
			r.Static = append(r.Static, credential)
		}
	}
}

// dotenvFileName is the static-secret file name: a declared path with this base
// is parsed as KEY=VALUE lines whose values are static credentials.
const dotenvFileName = ".env"

// loginCredentialStore classifies a declared login store: the provider its
// credential authenticates, and the agents the store applies to (nil = every
// agent that declares the path). A login store's value is never read: the
// read-back records only that the store exists.
//
//exhaustruct:ignore
type loginCredentialStore struct {
	provider string
	agents   []string
}

// appliesTo reports whether the store classifies as a login for agentName.
func (s loginCredentialStore) appliesTo(agentName string) bool {
	return s.agents == nil || slices.Contains(s.agents, agentName)
}

// loginCredentialStores maps a declared login store (sandbox-home-relative path)
// to its rule. The shared opencode auth.json is scoped to opencode (v1):
// opencode2 keeps credentials in its database and a fresh database never imports
// the file, so reporting it as an opencode2 login would be a false positive.
var loginCredentialStores = map[string]loginCredentialStore{
	".claude/settings.json":           {provider: "claude-code"},
	".codex/auth.json":                {provider: "codex"},
	".local/share/opencode/auth.json": {provider: "opencode", agents: []string{"opencode"}},
	".pi/agent/settings.json":         {provider: "pi"},
}

// secretKeyMarkers are the case-insensitive substrings that mark a dotenv key as
// a secret. A key without one is ordinary configuration, not a credential.
var secretKeyMarkers = []string{"KEY", "TOKEN", "SECRET", "PASSWORD"}

// looksLikeSecretKey reports whether a dotenv key names a secret, so obvious
// non-secrets (DEBUG=true, LOG_LEVEL=info) stay out of the static credentials.
func looksLikeSecretKey(key string) bool {
	upper := strings.ToUpper(key)
	return slices.ContainsFunc(secretKeyMarkers, func(marker string) bool {
		return strings.Contains(upper, marker)
	})
}

// credentialProviders maps a provider token in a secret's key name to the
// request header field that carries it. An unrecognised provider contributes no
// field hint.
var credentialProviders = []struct {
	token    string
	provider string
	field    string
}{
	{token: "OPENAI", provider: "openai", field: "authorization"},
	{token: "ANTHROPIC", provider: "anthropic", field: "x-api-key"},
	{token: "GEMINI", provider: "gemini", field: "x-goog-api-key"},
}

// staticCredentials parses a dotenv file's KEY=VALUE lines into static
// credentials. Blank lines, # comments, and keys that do not look like secrets
// are skipped; the value comes from the right-hand side, and Provider/Field are
// derived from a recognisable provider name in the key.
func staticCredentials(source string, content []byte) []Credential {
	var credentials []Credential
	for line := range strings.SplitSeq(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" || !looksLikeSecretKey(key) {
			continue
		}
		provider, field := staticCredentialProviderField(key)
		credentials = append(credentials, Credential{
			Kind:     CredentialStatic,
			Source:   source,
			Provider: provider,
			Field:    field,
			Value:    strings.TrimSpace(value),
		})
	}
	return credentials
}

// staticCredentialProviderField derives a provider and header-field hint from a
// dotenv key when the key names a known provider (OPENAI_API_KEY → provider
// "openai", field "authorization"). Both are empty for an unrecognised key.
func staticCredentialProviderField(key string) (string, string) {
	upper := strings.ToUpper(key)
	for _, known := range credentialProviders {
		if strings.HasPrefix(upper, known.token) {
			return known.provider, known.field
		}
	}
	return "", ""
}

// readCappedFile reads path's contents, bounded by maxBytes. ok is false when
// the file cannot be read or exceeds the cap: a secret file larger than the cap
// is not parsed whole into host memory.
func readCappedFile(path string, maxBytes int64) ([]byte, bool) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, false
	}
	if int64(len(data)) > maxBytes {
		return nil, false
	}
	return data, true
}
