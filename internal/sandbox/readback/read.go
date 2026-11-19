package readback

import (
	"io"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"

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
	// EnvVar is the configured secret's env-var name — a dotenv key or the
	// derived <PROVIDER>_API_KEY of an inline provider key — set for a static
	// credential only. It scopes a proposed secret write; empty means the
	// credential cannot be scoped.
	EnvVar string
	Field  string   // header-field hint, optional
	Hosts  []string // endpoints named for the entry, optional
	Value  string   // static only; never in a Manifest
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
		loginStore, isLogin := loginStoreFor(entry.Key)
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
	readBack.Static = append(readBack.Static, configInlineCredentials(home, collection.manifest)...)
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
// observation channel did not see. A credential that already carries hosts (an
// inline credential takes its own entry's) keeps them; a credential whose
// provider no entry names keeps empty Hosts.
func (r *ReadBack) fillStaticCredentialHosts(home string, manifest Manifest) {
	hostsByProvider := configEndpointHosts(home, manifest)
	for i := range r.Static {
		if len(r.Static[i].Hosts) > 0 {
			continue
		}
		r.Static[i].Hosts = hostsByProvider[normalizeToken(r.Static[i].Provider)]
	}
}

// configEndpointHosts reads the agent's declared config files and maps each
// provider they name to the endpoint host its entry declares. A probe entry and
// the dotenv carry no endpoint; an opaque (over-cap) or unparsable file is
// skipped, never guessed at.
func configEndpointHosts(home string, manifest Manifest) map[string][]string {
	hostsByProvider := map[string][]string{}
	for _, entry := range manifest.Entries {
		if isProbe(entry) || entry.Hash == "" {
			continue
		}
		format, ok := configdecode.FormatFor(entry.Key)
		if !ok {
			continue
		}
		// A failed read yields nil content, which decodes to no hosts, so it
		// needs no separate skip.
		content, _ := readCappedFile(filepath.Join(home, entry.Key), maxHashedBytes)
		document, err := configdecode.Decode(format, content)
		if err != nil {
			continue
		}
		collectEndpointHosts(document, hostsByProvider)
	}
	for provider, hosts := range hostsByProvider {
		hostsByProvider[provider] = slices.Compact(slices.Sorted(slices.Values(hosts)))
	}
	return hostsByProvider
}

// walkProviderEntries calls visit for every node the document positively
// presents as a provider entry, with the provider token that entry declares. A
// node is a provider entry when it sits inside a provider container (keyed by
// the provider id, opencode's "provider.<id>" / "models.providers.<id>") or
// names its provider through an explicit "provider" field (hermes'
// "model.provider"). A node that merely carries an endpoint under some other key
// (an MCP server's "url", a tool's "endpoint") is not a provider entry, so an
// unrecognised shape is never a guess. It factors the traversal the endpoint
// walk uses, so both walks agree on what a provider entry is.
func walkProviderEntries(node any, visit func(entry map[string]any, provider string)) {
	walkProviderNode(node, "", "", false, visit)
}

// walkProviderNode is walkProviderEntries' recursion. key is the node's key in
// its parent; entryKey is the provider id the node's enclosing provider
// container gives it, or "" outside a container. inModelCatalog is set once the
// walk enters a model catalog (see isModelCatalog): such a node is never a
// provider entry, even when it names a provider explicitly, and its keys are
// never a provider container. The child keys are visited in sorted order, so the
// whole traversal — and therefore the credentials it yields — does not depend on
// map iteration order.
func walkProviderNode(
	node any,
	key, entryKey string,
	inModelCatalog bool,
	visit func(entry map[string]any, provider string),
) {
	switch typed := node.(type) {
	case map[string]any:
		if !inModelCatalog {
			if provider, ok := providerNamedByEntry(typed, entryKey); ok {
				visit(typed, provider)
			}
		}
		container := !inModelCatalog && isProviderContainer(key)
		for _, childKey := range slices.Sorted(maps.Keys(typed)) {
			child := typed[childKey]
			childEntryKey := ""
			if container {
				childEntryKey = childKey
			}
			walkProviderNode(child, childKey, childEntryKey, inModelCatalog || isModelCatalog(childKey, child), visit)
		}
	case []any:
		for _, child := range typed {
			walkProviderNode(child, "", "", inModelCatalog, visit)
		}
	}
}

// collectEndpointHosts records, per provider a decoded config document names,
// the endpoint host that provider's entry declares. An entry that names a
// provider but declares no endpoint adds nothing: the hosts map never carries an
// empty provider key.
func collectEndpointHosts(node any, hostsByProvider map[string][]string) {
	walkProviderEntries(node, func(entry map[string]any, provider string) {
		hosts := entryEndpointHosts(entry)
		if len(hosts) == 0 {
			return
		}
		normalized := normalizeToken(provider)
		hostsByProvider[normalized] = append(hostsByProvider[normalized], hosts...)
	})
}

// providerNamedByEntry returns the provider an entry names, verbatim: the id its
// enclosing provider container gave it, or an explicit "provider" field. It
// reports false for a node that names no provider either way, so a generic
// "name"/"id" field on a non-provider entry (an MCP server, a tool) never claims
// a provider. A name that normalizes to empty (e.g. "___") names no provider
// either.
func providerNamedByEntry(entry map[string]any, entryKey string) (string, bool) {
	if provider, _ := entry["provider"].(string); provider != "" {
		if normalizeToken(provider) == "" {
			return "", false
		}
		return provider, true
	}
	if entryKey != "" && normalizeToken(entryKey) != "" {
		return entryKey, true
	}
	return "", false
}

// providerContainers are the config keys whose value is keyed by provider id, so
// a child entry names its provider through its container-relative key
// (opencode's "provider.<id>" shape, and the "models.providers.<id>" wrapper).
// A bare "models" map is a model catalog, not a container: only the
// "models.providers.<id>" wrapper is one (see isModelCatalog).
var providerContainers = []string{"provider", "providers", "models"}

// isProviderContainer reports whether a config key holds provider-keyed entries.
func isProviderContainer(key string) bool {
	return slices.Contains(providerContainers, normalizeToken(key))
}

// isModelCatalog reports whether the child under key is a model catalog rather
// than provider-keyed entries. A "models" key holds models — opencode's
// provider.<id>.models.<model> — except the documented models.providers.<id>
// wrapper, whose value holds a "providers" key. A catalog's descendants are
// never provider entries.
func isModelCatalog(key string, child any) bool {
	if normalizeToken(key) != "models" {
		return false
	}
	entry, ok := child.(map[string]any)
	if !ok {
		return true
	}
	_, isWrapper := entry["providers"]
	return !isWrapper
}

// entryEndpointHosts returns the hosts an entry's endpoint fields name, looking
// directly at the entry and at its nested "options" object, which is where
// opencode keeps a provider's baseURL.
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
func endpointHosts(node map[string]any) []string {
	var hosts []string
	for key, value := range node {
		if !isEndpointKey(key) {
			continue
		}
		raw, _ := value.(string)
		if host := endpointHost(raw); host != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

// isEndpointKey reports whether a config key names an endpoint URL. It matches
// case-insensitively and treats '_' and '-' as separators, so baseURL,
// base_url, and BASE-URL all name the endpoint.
func isEndpointKey(key string) bool {
	normalized := normalizeToken(key)
	return slices.Contains(endpointKeys, normalized)
}

// endpointKeys are the normalized config keys that name a provider endpoint
// (specs/onboarding/onboarding.md §curl: baseURL / endpoint / url).
var endpointKeys = []string{"baseurl", "endpoint", "url"}

// endpointHost normalizes an endpoint value to its host, dropping the scheme,
// port, and path. A value that is not an absolute URL with a host yields "".
func endpointHost(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

// normalizeToken folds a provider name or a config key to its comparison form:
// lowercase, with '_' and '-' dropped, so OPENAI_API_KEY matches openai and
// baseURL matches base_url.
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
	// pattern is an exact sandbox-home-relative path or a glob matched with
	// filepath.Match.
	pattern  string
	provider string
	agents   []string
}

// appliesTo reports whether the store classifies as a login for agentName.
func (s loginCredentialStore) appliesTo(agentName string) bool {
	return s.agents == nil || slices.Contains(s.agents, agentName)
}

// loginCredentialStores gives each declared login store's pattern its rule. An
// exact path is a pattern without metacharacters; a directory store is a glob
// whose files each classify individually. The shared opencode auth.json is
// scoped to opencode (v1): opencode2 keeps credentials in its database and a
// fresh database never imports the file, so reporting it as an opencode2 login
// would be a false positive.
var loginCredentialStores = []loginCredentialStore{
	{pattern: ".claude/settings.json", provider: "claude-code"},
	{pattern: ".codex/auth.json", provider: "codex"},
	{pattern: ".local/share/opencode/auth.json", provider: "opencode", agents: []string{"opencode"}},
	{pattern: ".pi/agent/settings.json", provider: "pi"},
	{pattern: ".kimi-code/credentials/*.json", provider: "kimi-code"},
}

// loginStoreFor returns the login-store rule matching a manifest entry key, if
// a pattern matches it. A pattern with no metacharacter matches its exact path.
func loginStoreFor(key string) (loginCredentialStore, bool) {
	for _, store := range loginCredentialStores {
		// A malformed pattern cannot match any key, so its error needs no
		// separate branch: it is a non-match.
		if matched, _ := filepath.Match(store.pattern, key); matched {
			return store, true
		}
	}
	return loginCredentialStore{}, false
}

// secretKeyMarkers are the whole key segments that mark a dotenv key as a
// secret. A key without one is ordinary configuration, not a credential.
var secretKeyMarkers = []string{"KEY", "TOKEN", "SECRET", "PASSWORD"}

// looksLikeSecretKey reports whether a dotenv key names a secret, matching the
// markers against the key's segments rather than its substrings: MAX_TOKENS is
// a token count, while OPENAI_API_KEY carries a KEY segment. Obvious
// non-secrets (DEBUG, LOG_LEVEL) stay out of the static credentials.
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
func isSecretKeySeparator(r rune) bool {
	return r == '_' || r == '-'
}

// inlineCredentialKeys are the normalized keys whose value inside a provider
// entry is that provider's own static credential.
var inlineCredentialKeys = []string{"apikey", "key", "token"}

// InlineCredentialsIn returns each inline static credential the config document
// in data holds: a credential-bearing key inside a provider entry, with the
// provider, host, and env-var name taken from that entry. source names the
// document so its format is chosen and the credential's Source records it. A
// format the decoder does not know, or a document it cannot decode, yields
// nothing, never a guess. The value stays in memory on Credential.Value; it is
// never persisted.
func InlineCredentialsIn(source string, data []byte) []Credential {
	format, ok := configdecode.FormatFor(source)
	if !ok {
		return nil
	}
	document, err := configdecode.Decode(format, data)
	if err != nil {
		return nil
	}
	var credentials []Credential
	walkProviderEntries(document, func(providerEntry map[string]any, provider string) {
		credentials = append(credentials, entryCredentials(providerEntry, provider, source)...)
	})
	return credentials
}

// configInlineCredentials returns each inline static credential a declared
// config file holds, via InlineCredentialsIn. A probe, the dotenv, and a login
// store are skipped (the login store's value is never read); a file the reader
// cannot decode yields nothing, never a guess.
func configInlineCredentials(home string, manifest Manifest) []Credential {
	var credentials []Credential
	for _, entry := range manifest.Entries {
		if isProbe(entry) || entry.Hash == "" || filepath.Base(entry.Key) == dotenvFileName {
			continue
		}
		if _, isLogin := loginStoreFor(entry.Key); isLogin {
			continue
		}
		if _, ok := configdecode.FormatFor(entry.Key); !ok {
			continue
		}
		// A failed read yields nil content, which decodes to an empty
		// document, so it needs no separate skip.
		content, _ := readCappedFile(filepath.Join(home, entry.Key), maxHashedBytes)
		credentials = append(credentials, InlineCredentialsIn(entry.Key, content)...)
	}
	return credentials
}

// entryCredentials returns the static credentials one provider entry holds: its
// own credential key (api_key/apiKey/key/token), the credential keys of its
// nested "options" object (opencode keeps a provider's apiKey beside its
// baseURL there), and the secret-looking keys of its "env" sub-table, each
// scoped by an env-var name. A non-string value (a token count) and a value that
// is a reference are not credentials; the header field is left to the harness.
// The keys are visited in sorted order, so the output does not depend on map
// iteration order. provider is the entry's raw provider token: Credential.Provider
// takes the normalized form (the endpoint-map key), while a derived env-var name
// keeps the token's word boundaries.
func entryCredentials(entry map[string]any, provider, source string) []Credential {
	hosts := entryEndpointHosts(entry)
	normalizedProvider := normalizeToken(provider)
	credentials := inlineKeyCredentials(entry, provider, normalizedProvider, source, hosts)
	if options, ok := entry["options"].(map[string]any); ok {
		credentials = append(
			credentials,
			inlineKeyCredentials(options, provider, normalizedProvider, source, hosts)...,
		)
	}
	env, _ := entry["env"].(map[string]any)
	for _, key := range slices.Sorted(maps.Keys(env)) {
		if !looksLikeSecretKey(key) {
			continue
		}
		if value, ok := inlineCredentialValue(env[key]); ok {
			credentials = append(credentials, Credential{
				Kind:     CredentialStatic,
				Source:   source,
				Provider: normalizedProvider,
				// A key the entry declares is the env-var name it scopes by,
				// and it wins over the derived one.
				EnvVar: key,
				Hosts:  hosts,
				Value:  value,
			})
		}
	}
	return credentials
}

// inlineKeyCredentials returns the credentials an entry's (or its "options"
// object's) own credential keys hold. The keys are visited in sorted order, so
// the output does not depend on map iteration order.
func inlineKeyCredentials(
	node map[string]any,
	provider, normalizedProvider, source string,
	hosts []string,
) []Credential {
	var credentials []Credential
	for _, key := range slices.Sorted(maps.Keys(node)) {
		if !slices.Contains(inlineCredentialKeys, normalizeToken(key)) {
			continue
		}
		if value, ok := inlineCredentialValue(node[key]); ok {
			credentials = append(credentials, Credential{
				Kind:     CredentialStatic,
				Source:   source,
				Provider: normalizedProvider,
				EnvVar:   derivedEnvVarName(provider),
				Hosts:    hosts,
				Value:    value,
			})
		}
	}
	return credentials
}

// inlineCredentialValue returns a config value as a static secret's value, and
// whether it is one: a non-string value (a token count) and a value that is a
// reference rather than a literal are not. The value stays in memory; this
// predicate returns only the classification.
func inlineCredentialValue(raw any) (string, bool) {
	value, ok := raw.(string)
	if !ok || isReferenceValue(value) {
		return "", false
	}
	return value, true
}

// derivedEnvVarName returns the env-var name an inline credential is scoped by
// when its entry names none: <PROVIDER>_API_KEY, uppercased with
// non-alphanumerics folded to '_'.
func derivedEnvVarName(provider string) string {
	var name strings.Builder
	for _, r := range strings.ToUpper(provider) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			name.WriteRune(r)
			continue
		}
		name.WriteRune('_')
	}
	name.WriteString("_API_KEY")
	return name.String()
}

// isReferenceValue reports whether a config value is a reference rather than a
// literal secret to copy: a ${...} placeholder, a leading-! command reference
// (oh-my-pi's "!op read ..."), or a bare environment-variable identifier (a
// value the read-back cannot tell apart from a bound variable name). Only the
// classification leaves this predicate; the value stays in memory.
func isReferenceValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	return strings.HasPrefix(trimmed, "${") ||
		strings.HasPrefix(trimmed, "!") ||
		isEnvVarIdentifier(trimmed)
}

// isEnvVarIdentifier reports whether value is a bare environment-variable name:
// at least four characters of A-Z, 0-9, and '_', starting with a letter. A value
// in that form is a variable name the read-back cannot confidently classify as a
// literal secret.
func isEnvVarIdentifier(value string) bool {
	if len(value) < 4 {
		return false
	}
	if c := value[0]; c < 'A' || c > 'Z' {
		return false
	}
	for i := 1; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_':
		default:
			return false
		}
	}
	return true
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
			EnvVar:   key,
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
