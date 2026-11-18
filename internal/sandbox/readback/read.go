package readback

import (
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configdecode"
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
//
//deadcode:ignore reason="read-back API; the pending learn-mode analyser (S6) calls Read after a session"
func Read(home, agentName string, baseline Manifest) (ReadBack, error) {
	// An unknown agent yields the nil Agent, for which AsReadBackDescriber is
	// false, so no separate lookup guard is needed.
	a, _ := agent.Lookup(agentName)
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
			//
			// gomutants:disable-next-line INVERT_LOOP_CTRL reason="a probe entry is always the last (its key '<agent>:<probe>' sorts after every '.'-prefixed path), so continuing past it and breaking on it are the same"
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
			if content, ok := readCappedFile(filepath.Join(home, entry.Key), maxHashedBytes); ok {
				readBack.Static = append(readBack.Static, staticCredentials(entry.Key, content)...)
			}
			continue
		}
		// A login store is also a config fact: it appears in ConfigPaths too.
		readBack.ConfigPaths = append(readBack.ConfigPaths, entry.Key)
	}
	readBack.appendProbeCredentials(collection.probes)
	readBack.fillStaticCredentialHosts(home, collection.manifest)
	// The manifest entries are already sorted by key, so appending the
	// config paths in this loop is order-preserving.
	//
	// gomutants:disable-next-line STATEMENT_REMOVE reason="the manifest entries are already sorted by key, so appending ConfigPaths in this loop is order-preserving"
	sort.Strings(readBack.ConfigPaths)
	// gomutants:disable-next-line STATEMENT_REMOVE reason="the probe outcomes are appended in declared probe order, so Unreadable is already sorted"
	sort.Strings(readBack.Unreadable)
	return readBack, nil
}

// appendProbeCredentials reports the credentials each credential probe found and
// names the probe sources whose store was unreadable. The outcomes come from the
// same pass that produced the manifest digest, so the two cannot disagree.
//
//deadcode:ignore reason="folds one collection pass's probe outcomes into the ReadBack; reached through Read from the pending S6 analyser"
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

// fillStaticCredentialHosts sets each static credential's Hosts from the
// endpoint the agent's config entry for its provider declares: the spec's
// "endpoint in the same config entry" attribution fallback for a session the
// observation channel did not see. A credential whose provider no entry names
// keeps empty Hosts.
//
//deadcode:ignore reason="fills Credential.Hosts from the provider's config entry; reached through Read from the pending S6 analyser"
func (r *ReadBack) fillStaticCredentialHosts(home string, manifest Manifest) {
	if len(r.Static) == 0 {
		return
	}
	hostsByProvider := configEndpointHosts(home, manifest)
	for i := range r.Static {
		r.Static[i].Hosts = hostsByProvider[normalizeToken(r.Static[i].Provider)]
	}
}

// configEndpointHosts reads the agent's declared config files and maps each
// provider they name to the endpoint host its entry declares. A probe entry and
// the dotenv carry no endpoint; an opaque (over-cap) or unparsable file is
// skipped, never guessed at.
//
//deadcode:ignore reason="extracts provider endpoints from the agent config files; reached through Read from the pending S6 analyser"
func configEndpointHosts(home string, manifest Manifest) map[string][]string {
	hostsByProvider := map[string][]string{}
	for _, entry := range manifest.Entries {
		if isProbe(entry) || entry.Hash == "" || filepath.Base(entry.Key) == dotenvFileName {
			continue
		}
		format, ok := configdecode.FormatFor(entry.Key)
		if !ok {
			continue
		}
		content, ok := readCappedFile(filepath.Join(home, entry.Key), maxHashedBytes)
		if !ok {
			continue
		}
		document, err := configdecode.Decode(format, content)
		if err != nil {
			continue
		}
		collectEndpointHosts(document, "", hostsByProvider)
	}
	for provider, hosts := range hostsByProvider {
		hostsByProvider[provider] = slices.Compact(slices.Sorted(slices.Values(hosts)))
	}
	return hostsByProvider
}

// collectEndpointHosts walks a decoded config document and records, per
// provider it finds named, the endpoint host that provider's entry declares.
// A node names a provider through its own provider/name/id field or through the
// map key it sits under, and only when it actually declares an endpoint
// (opencode's "provider.<id>.options.baseURL" shape names the provider by key);
// an entry's endpoint is a direct field or a nested "options" field.
//
//deadcode:ignore reason="walks the agent config for provider endpoints; reached through configEndpointHosts and Read"
func collectEndpointHosts(node any, key string, hostsByProvider map[string][]string) {
	switch typed := node.(type) {
	case map[string]any:
		hosts := entryEndpointHosts(typed)
		if len(hosts) > 0 {
			if provider, ok := providerNamedByEntry(typed, key); ok {
				hostsByProvider[provider] = append(hostsByProvider[provider], hosts...)
			}
		}
		for childKey, child := range typed {
			collectEndpointHosts(child, childKey, hostsByProvider)
		}
	case []any:
		for _, child := range typed {
			collectEndpointHosts(child, "", hostsByProvider)
		}
	}
}

// providerNamedByEntry returns the provider an entry names: its provider, name,
// or id field, or the map key it sits under. It reports false for a node that
// names no provider or names one only as a non-string.
//
//deadcode:ignore reason="identifies the provider a config entry describes; reached through collectEndpointHosts and Read"
func providerNamedByEntry(entry map[string]any, key string) (string, bool) {
	for _, field := range providerNameFields {
		if provider, ok := entry[field].(string); ok && provider != "" {
			return normalizeToken(provider), true
		}
	}
	if key != "" {
		return normalizeToken(key), true
	}
	return "", false
}

// providerNameFields are the config keys an entry uses to name the provider it
// configures (hermes' "model.provider", an OpenAI-compatible "name").
var providerNameFields = []string{"provider", "name", "id"}

// entryEndpointHosts returns the hosts an entry's endpoint fields name, looking
// directly at the entry and at its nested "options" object, which is where
// opencode keeps a provider's baseURL.
//
//deadcode:ignore reason="collects an entry's endpoint hosts; reached through collectEndpointHosts and Read"
func entryEndpointHosts(entry map[string]any) []string {
	hosts := endpointHosts(entry)
	if options, ok := entry["options"].(map[string]any); ok {
		hosts = append(hosts, endpointHosts(options)...)
	}
	return hosts
}

// endpointHosts returns the host each endpoint field of node names. A value that
// is not a string, or is not an absolute URL with a host, contributes nothing:
// an unknown shape yields no host rather than a guess.
//
//deadcode:ignore reason="extracts a host from an entry's endpoint fields; reached through entryEndpointHosts and Read"
func endpointHosts(node map[string]any) []string {
	var hosts []string
	for key, value := range node {
		if !isEndpointKey(key) {
			continue
		}
		raw, ok := value.(string)
		if !ok {
			continue
		}
		if host := endpointHost(raw); host != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

// isEndpointKey reports whether a config key names an endpoint URL. It matches
// case-insensitively and treats '_' and '-' as separators, so baseURL,
// base_url, and BASE-URL all name the endpoint.
//
//deadcode:ignore reason="recognizes an endpoint field in a config entry; reached through endpointHosts and Read"
func isEndpointKey(key string) bool {
	normalized := normalizeToken(key)
	return slices.Contains(endpointKeys, normalized)
}

// endpointKeys are the normalized config keys that name a provider endpoint
// (specs/onboarding/onboarding.md §curl: baseURL / endpoint / url).
var endpointKeys = []string{"baseurl", "endpoint", "url"}

// endpointHost normalizes an endpoint value to its host, dropping the scheme,
// port, and path. A value that is not an absolute URL with a host yields "".
//
//deadcode:ignore reason="normalizes an endpoint URL to its host; reached through endpointHosts and Read"
func endpointHost(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	return parsed.Hostname()
}

// normalizeToken folds a provider name or a config key to its comparison form:
// lowercase, with '_' and '-' dropped, so OPENAI_API_KEY matches openai and
// baseURL matches base_url.
//
//deadcode:ignore reason="folds provider and endpoint-key tokens for comparison; reached through Read"
func normalizeToken(token string) string {
	return strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(strings.TrimSpace(token)))
}

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
//
//deadcode:ignore reason="scopes a login store to its agents; reached through Read from the pending S6 analyser"
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

// secretKeyMarkers are the whole key segments that mark a dotenv key as a
// secret. A key without one is ordinary configuration, not a credential.
var secretKeyMarkers = []string{"KEY", "TOKEN", "SECRET", "PASSWORD"}

// looksLikeSecretKey reports whether a dotenv key names a secret, matching the
// markers against the key's segments rather than its substrings: MAX_TOKENS is
// a token count, while OPENAI_API_KEY carries a KEY segment. Obvious
// non-secrets (DEBUG, LOG_LEVEL) stay out of the static credentials.
//
//deadcode:ignore reason="filters a dotenv key before it becomes a credential; reached through Read from the pending S6 analyser"
func looksLikeSecretKey(key string) bool {
	for segment := range strings.FieldsFuncSeq(strings.ToUpper(key), isSecretKeySeparator) {
		if slices.Contains(secretKeyMarkers, segment) {
			return true
		}
	}
	return false
}

// isSecretKeySeparator reports whether r separates two segments of a dotenv key,
// so a marker is matched as a whole segment (MAX_TOKENS) rather than a substring
// (TOKENS).
//
//deadcode:ignore reason="splits a dotenv key into segments for looksLikeSecretKey; reached through Read from the pending S6 analyser"
func isSecretKeySeparator(r rune) bool {
	return r == '_' || r == '-'
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
//
//deadcode:ignore reason="parses a dotenv's secret lines; reached through Read from the pending S6 analyser"
func staticCredentials(source string, content []byte) []Credential {
	var credentials []Credential
	for line := range strings.SplitSeq(string(content), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if !looksLikeSecretKey(key) {
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
//
//deadcode:ignore reason="derives a provider and header-field hint for a dotenv secret; reached through staticCredentials"
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
//
//deadcode:ignore reason="bounds a secret-file read; reached through Read from the pending S6 analyser"
func readCappedFile(path string, maxBytes int64) ([]byte, bool) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer func() { _ = file.Close() }()
	// gomutants:disable-next-line INTEGER_INCREMENT reason="io.LimitReader with maxBytes+2 returns the same bytes as maxBytes+1 for any file at or below maxBytes+1, and a longer file is rejected by the cap check below either way"
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, false
	}
	if int64(len(data)) > maxBytes {
		return nil, false
	}
	return data, true
}
