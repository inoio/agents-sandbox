package analyser

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configdecode"
	"github.com/inoio/agents-sandbox/internal/configmerge"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// ManagedConfigFacts is a finished session's home and its read-back.
type ManagedConfigFacts struct {
	// Paths is the config-path resolver. It is carried for callers that resolve
	// a scope's directory; the finder itself only names the per-slug scope.
	Paths configpaths.ConfigPaths
	Slug  string
	// Home is the home-volume host path the read-back was taken against.
	Home string
	// Agent is the canonical agent name, which selects the harness's
	// env-indirection form.
	Agent    string
	ReadBack readback.ReadBack
	// SecretEnvVars names the static secrets the analysis will propose as
	// env.secret.yaml entries (the env-var names with a value and a host). The
	// write-back substitutes a credential only when its name is here, so a
	// managed config never carries a ${ENVVAR} placeholder without a matching
	// secret entry; a file holding a credential that will not become a secret is
	// refused whole rather than copied with a raw sibling value.
	SecretEnvVars []string
	// Logger reports a candidate the write-back could not sanitize or read. It
	// may be nil.
	Logger termio.Logger
}

// ManagedConfigFindings returns one finding per changed, non-credential agent
// config file, whose single write is the sanitized managed config. The static
// credentials' values are replaced by PlaceholderFor(EnvVar) in the content and
// never appear in the finding. Substitution is per-file (only the file's own
// credentials are replaced, so no ${ENVVAR} placeholder is introduced without a
// matching secret), but the value-remains check is global: a candidate can be
// any file the read-back's glob declared, so a value classified from a
// different file must still refuse it. A file that is a credential store, cannot
// be read under readback.MaxHashedBytes, or still holds any static value after
// substitution is reported through Logger and yields no finding.
func ManagedConfigFindings(f ManagedConfigFacts) []Finding {
	credentialsBySource := staticCredentialsBySource(f.ReadBack.Static)
	indirectionKey := nativeEnvIndirectionKey(f.Agent)
	secretEnvVars := stringSet(f.SecretEnvVars)

	var findings []Finding
	for _, configPath := range f.ReadBack.ConfigPaths {
		// The credential-store check is agent-independent and subsumes
		// ReadBack.Login[].Source for the candidate set: every login source is a
		// store path, while a store path can reach ConfigPaths without a Login
		// entry when the store's agents filter excludes the current agent
		// (opencode2 inherits opencode's auth.json and provisions it). A probe
		// login's "<agent>:<probe>" source never reaches ConfigPaths, so it needs
		// no separate filter here.
		if !f.changed(configPath) || readback.IsCredentialStorePath(configPath) {
			continue
		}
		// Only a file the agent's snippet pattern matches is provisioned; a
		// candidate the merge never reads would be a managed write that takes no
		// effect.
		if !snippetNameMatches(f.Agent, configPath) {
			f.warnf(
				"skipping managed agent config %s: the agent merges no snippet by that name",
				configPath,
			)
			continue
		}
		sanitized, ok := f.sanitize(
			configPath,
			credentialsBySource[configPath],
			f.ReadBack.Static,
			indirectionKey,
			secretEnvVars,
		)
		if !ok {
			continue
		}
		findings = append(findings, newManagedConfigFinding(configPath, sanitized))
	}
	return findings
}

// stringSet indexes values for membership, or an empty set when none are given.
func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

// snippetNameMatches reports whether the agent merges a snippet with configPath's
// base name. An unknown agent, or one without a snippet pattern, is not
// second-guessed here: the write's target is resolved and refused at apply time.
func snippetNameMatches(agentName, configPath string) bool {
	profile, ok := agent.Lookup(agentName)
	if !ok {
		return true
	}
	merger, ok := agent.AsConfigMerger(profile)
	if !ok {
		return true
	}
	return configmerge.SnippetNameMatches(merger.SnippetPattern(), filepath.Base(configPath))
}

// changed reports whether the read-back saw configPath change since baseline.
func (f ManagedConfigFacts) changed(configPath string) bool {
	return slices.Contains(f.ReadBack.Changed, configPath)
}

// sanitize reads configPath's content under the read-back's cap and replaces
// every static secret value the file's own credentials hold. It reports false,
// after naming the file through the logger, when the file cannot be read, when
// a credential has no env-var name to substitute, or when a value survives the
// substitution — so no value ever reaches the finding. The substitution is
// per-file, but the value-remains check runs against the whole static set: a
// value classified from another file can appear verbatim in this candidate, and
// only the whole set can prove the file is value-free.
func (f ManagedConfigFacts) sanitize(
	configPath string,
	credentials []readback.Credential,
	allStatic []readback.Credential,
	indirectionKey string,
	secretEnvVars map[string]bool,
) (string, bool) {
	content, ok := readback.ReadCappedFile(filepath.Join(f.Home, configPath), readback.MaxHashedBytes)
	if !ok {
		f.warnf(
			"skipping managed agent config %s: unreadable or over the %d-byte cap",
			configPath,
			readback.MaxHashedBytes,
		)
		return "", false
	}
	sanitized, ok := substituteStaticValues(string(content), credentials, indirectionKey, secretEnvVars)
	if !ok {
		f.warnf(
			"skipping managed agent config %s: a credential cannot be replaced by a placeholder",
			configPath,
		)
		return "", false
	}
	if contentRetainsStaticValue(configPath, sanitized, allStatic) {
		f.warnf(
			"skipping managed agent config %s: a static secret value was not fully replaced",
			configPath,
		)
		return "", false
	}
	return sanitized, true
}

// warnf logs a skipped candidate when a logger is present.
func (f ManagedConfigFacts) warnf(format string, args ...any) {
	if f.Logger != nil {
		f.Logger.Warnf(format, args...)
	}
}

// newManagedConfigFinding builds the per-slug managed-config finding for one
// sanitized file. Its evidence and write text name the file and never a value.
func newManagedConfigFinding(configPath, content string) Finding {
	//exhaustruct:ignore // a managed-config finding carries no destinations or header names
	evidence := Evidence{
		Summary: "the session authored " + configPath,
		Channel: evidenceChannelReadback,
	}
	//exhaustruct:ignore // a managed-config write names no config key and never removes
	write := Write{
		Scope: ScopePerSlugUser,
		//exhaustruct:ignore // the sanitized content already holds placeholders, never a value
		ManagedConfig: &ManagedConfigWrite{
			Name:    filepath.Base(configPath),
			Content: []byte(content),
		},
		Text: "The per-slug user config scopes this agent config file, keyed by the repo slug, so it applies to every clone of the remote.",
	}
	//exhaustruct:ignore // EvidenceHash is stamped here
	return Finding{Evidence: evidence, Writes: []Write{write}, Kind: KindObservation}.withEvidenceHash()
}

// staticCredentialsBySource indexes the static credentials by the file they came
// from. A credential with no value is not a secret and contributes nothing; a
// credential with no env-var name cannot be substituted and is kept in the map
// so substituteStaticValues refuses its file rather than emitting the value.
func staticCredentialsBySource(static []readback.Credential) map[string][]readback.Credential {
	bySource := make(map[string][]readback.Credential)
	for _, credential := range static {
		if credential.Value == "" {
			continue
		}
		bySource[credential.Source] = append(bySource[credential.Source], credential)
	}
	return bySource
}

// nativeEnvIndirectionKey returns the config key the agent's harness reads an
// environment-variable name from, or "" when it takes a ${VAR} placeholder
// verbatim. An unknown agent yields "".
func nativeEnvIndirectionKey(agentName string) string {
	profile, ok := agent.Lookup(agentName)
	if !ok {
		return ""
	}
	describer, ok := agent.AsEnvIndirectionDescriber(profile)
	if !ok {
		return ""
	}
	return describer.EnvIndirectionKey()
}

// substituteStaticValues replaces each static credential's value in content. The
// default form replaces every occurrence of the value with its ${ENVVAR}
// placeholder; a harness with a native indirection key rewrites the whole
// assignment instead (its key becomes the indirection key and its value becomes
// the env-var name). ok is false when a credential cannot be substituted — it
// has no env-var name, or its name is not one the analysis will propose as a
// secret (so its placeholder would have no env.secret.yaml entry); the caller
// then emits no finding. The value-remains check is the caller's, because it
// decodes the content. Credentials are substituted longest value first, so a
// value that is a prefix of another does not corrupt the longer one.
func substituteStaticValues(
	content string,
	credentials []readback.Credential,
	indirectionKey string,
	secretEnvVars map[string]bool,
) (string, bool) {
	sanitized := content
	for _, credential := range longestValueFirst(credentials) {
		if credential.EnvVar == "" || !secretEnvVars[credential.EnvVar] {
			return "", false
		}
		if indirectionKey != "" {
			sanitized = rewriteNativeAssignments(sanitized, credential, indirectionKey)
			continue
		}
		sanitized = strings.ReplaceAll(sanitized, credential.Value, envsecret.PlaceholderFor(credential.EnvVar))
	}
	return sanitized, true
}

// longestValueFirst orders credentials so a longer value is substituted before a
// shorter one that is its prefix: replacing "sk-abc" first would corrupt a
// sibling "sk-abcdef" into the placeholder for "sk-abc" followed by "def".
func longestValueFirst(credentials []readback.Credential) []readback.Credential {
	ordered := slices.Clone(credentials)
	slices.SortStableFunc(ordered, func(a, b readback.Credential) int {
		return len(b.Value) - len(a.Value)
	})
	return ordered
}

// contentRetainsStaticValue reports whether content may still hold any of the
// credentials' values after substitution. It tests the literal content and, when
// the path's format decodes, every string the decoded document holds: a config
// file may store a value in its escaped form (JSON's \" and \\), which a literal
// search misses even though the harness reads the value. A literal hit is
// deliberately kept as well, so a value left in a comment is still caught. A
// known format that does not parse is reported as retaining a value: the decoded
// walk cannot run, so an escaped form cannot be ruled out.
func contentRetainsStaticValue(configPath, content string, credentials []readback.Credential) bool {
	if containsStaticValue(content, credentials) {
		return true
	}
	format, ok := configdecode.FormatFor(configPath)
	if !ok {
		return false
	}
	decoded, err := configdecode.Decode(format, []byte(content))
	if err != nil {
		return true
	}
	return decodedContainsStaticValue(decoded, credentials)
}

// decodedContainsStaticValue reports whether any string a decoded document holds
// — a key or a leaf value — contains one of the credentials' values.
func decodedContainsStaticValue(node any, credentials []readback.Credential) bool {
	switch typed := node.(type) {
	case map[string]any:
		for key, child := range typed {
			if containsStaticValue(key, credentials) || decodedContainsStaticValue(child, credentials) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if decodedContainsStaticValue(child, credentials) {
				return true
			}
		}
	case string:
		return containsStaticValue(typed, credentials)
	}
	return false
}

// containsStaticValue reports whether any of the credentials' values appears in
// text.
func containsStaticValue(text string, credentials []readback.Credential) bool {
	for _, credential := range credentials {
		if credential.Value != "" && strings.Contains(text, credential.Value) {
			return true
		}
	}
	return false
}

// rewriteNativeAssignments rewrites each assignment holding credential's value
// so the harness reads credential.EnvVar through indirectionKey: the key that
// introduces the value becomes indirectionKey, and the value becomes the
// env-var name. An occurrence whose key is already the env-var name — an
// env-table entry such as `KIMI_API_KEY = "…"` — keeps its key and takes the
// ${ENVVAR} placeholder instead, because that name is what the harness reads.
// An occurrence with no recognizable assignment key before it is left untouched,
// so the caller's value-remains check refuses the file rather than emitting the
// value.
func rewriteNativeAssignments(content string, credential readback.Credential, indirectionKey string) string {
	value := credential.Value
	if value == "" {
		return content
	}
	var builder strings.Builder
	remaining := content
	for {
		index := strings.Index(remaining, value)
		if index < 0 {
			builder.WriteString(remaining)
			return builder.String()
		}
		keyStart, keyEnd, ok := assignmentKeyRange(remaining, index)
		if !ok {
			builder.WriteString(remaining[:index+len(value)])
			remaining = remaining[index+len(value):]
			continue
		}
		if unquotedKey(remaining[keyStart:keyEnd]) == credential.EnvVar {
			builder.WriteString(remaining[:index])
			builder.WriteString(envsecret.PlaceholderFor(credential.EnvVar))
			remaining = remaining[index+len(value):]
			continue
		}
		builder.WriteString(remaining[:keyStart])
		builder.WriteString(indirectionKey)
		builder.WriteString(remaining[keyEnd:index])
		builder.WriteString(credential.EnvVar)
		remaining = remaining[index+len(value):]
	}
}

// unquotedKey returns a key token without its surrounding quotes, if any.
func unquotedKey(key string) string {
	if len(key) >= 2 && isQuote(key[0]) && isQuote(key[len(key)-1]) {
		return key[1 : len(key)-1]
	}
	return key
}

// assignmentKeyRange returns the half-open range of the key token that
// introduces the value starting at valueStart, when that value sits on the
// right-hand side of a '=' or ':' assignment. The range includes the key's
// quotes, if any. It reports false when no key token precedes the value.
func assignmentKeyRange(content string, valueStart int) (int, int, bool) {
	separator := skipSpaceBackward(content, valueStart)
	if separator > 0 && isQuote(content[separator-1]) {
		separator--
	}
	separator = skipSpaceBackward(content, separator)
	if separator == 0 || (content[separator-1] != '=' && content[separator-1] != ':') {
		return 0, 0, false
	}
	end := skipSpaceBackward(content, separator-1)
	if end > 0 && isQuote(content[end-1]) {
		end--
	}
	start := end
	for start > 0 && isKeyChar(content[start-1]) {
		start--
	}
	if start == end {
		return 0, 0, false
	}
	if start > 0 && isQuote(content[start-1]) {
		start--
		// The key token is quoted; consume its closing quote so replacing the
		// whole token does not leave a dangling quote behind.
		if end < len(content) && isQuote(content[end]) {
			end++
		}
	}
	return start, end, true
}

// skipSpaceBackward returns index moved left past any ASCII whitespace.
func skipSpaceBackward(content string, index int) int {
	for index > 0 && isSpaceByte(content[index-1]) {
		index--
	}
	return index
}

// isSpaceByte reports whether b is ASCII whitespace.
func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

// isQuote reports whether b opens or closes a quoted config value or key.
func isQuote(b byte) bool { return b == '"' || b == '\'' }

// isKeyChar reports whether b is part of a config key token. A '.' is excluded,
// so a dotted key's final segment is the token that is rewritten.
func isKeyChar(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b == '_' || b == '-':
		return true
	default:
		return false
	}
}
