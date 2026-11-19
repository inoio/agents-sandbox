package analyser

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configdecode"
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
	// Logger reports a candidate the write-back could not sanitize or read. It
	// may be nil.
	Logger termio.Logger
}

// ManagedConfigFindings returns one finding per changed, non-credential agent
// config file, whose single write is the sanitized managed config. The static
// credentials' values are replaced by PlaceholderFor(EnvVar) in the content and
// never appear in the finding. A file that is a credential store, cannot be read
// under readback.MaxHashedBytes, or still holds a value after substitution is
// reported through Logger and yields no finding.
//
//deadcode:ignore reason="managed-config write-back finder; the after-run wiring supplies its caller"
func ManagedConfigFindings(f ManagedConfigFacts) []Finding {
	credentialsBySource := staticCredentialsBySource(f.ReadBack.Static)
	indirectionKey := nativeEnvIndirectionKey(f.Agent)

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
		sanitized, ok := f.sanitize(configPath, credentialsBySource[configPath], indirectionKey)
		if !ok {
			continue
		}
		findings = append(findings, newManagedConfigFinding(configPath, sanitized))
	}
	return findings
}

// changed reports whether the read-back saw configPath change since baseline.
//
//deadcode:ignore reason="managed-config write-back finder; the after-run wiring supplies its caller"
func (f ManagedConfigFacts) changed(configPath string) bool {
	return slices.Contains(f.ReadBack.Changed, configPath)
}

// sanitize reads configPath's content under the read-back's cap and replaces
// every static secret value it holds. It reports false, after naming the file
// through the logger, when the file cannot be read, when a credential has no
// env-var name to substitute, or when a value survives the substitution — so no
// value ever reaches the finding.
//
//deadcode:ignore reason="managed-config write-back finder; the after-run wiring supplies its caller"
func (f ManagedConfigFacts) sanitize(
	configPath string,
	credentials []readback.Credential,
	indirectionKey string,
) (string, bool) {
	content, ok := readBoundedFile(filepath.Join(f.Home, configPath))
	if !ok {
		f.warnf(
			"skipping managed agent config %s: unreadable or over the %d-byte cap",
			configPath,
			readback.MaxHashedBytes,
		)
		return "", false
	}
	sanitized, ok := substituteStaticValues(string(content), credentials, indirectionKey)
	if !ok {
		f.warnf(
			"skipping managed agent config %s: a credential has no env-var name to substitute",
			configPath,
		)
		return "", false
	}
	if contentRetainsStaticValue(configPath, sanitized, credentials) {
		f.warnf(
			"skipping managed agent config %s: a static secret value was not fully replaced",
			configPath,
		)
		return "", false
	}
	return sanitized, true
}

// warnf logs a skipped candidate when a logger is present.
//
//deadcode:ignore reason="managed-config write-back finder; the after-run wiring supplies its caller"
func (f ManagedConfigFacts) warnf(format string, args ...any) {
	if f.Logger != nil {
		f.Logger.Warnf(format, args...)
	}
}

// newManagedConfigFinding builds the per-slug managed-config finding for one
// sanitized file. Its evidence and write text name the file and never a value.
//
//deadcode:ignore reason="managed-config write-back finder; the after-run wiring supplies its caller"
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
// so the value-remains check refuses its file rather than emitting the value.
//
//deadcode:ignore reason="managed-config write-back finder; the after-run wiring supplies its caller"
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
//
//deadcode:ignore reason="managed-config write-back finder; the after-run wiring supplies its caller"
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
// the env-var name). ok is false when a credential has no env-var name to
// substitute; the caller then emits no finding. The value-remains check is the
// caller's, because it decodes the content.
//
//deadcode:ignore reason="managed-config write-back finder; the after-run wiring supplies its caller"
func substituteStaticValues(
	content string,
	credentials []readback.Credential,
	indirectionKey string,
) (string, bool) {
	sanitized := content
	for _, credential := range credentials {
		if credential.EnvVar == "" {
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

// contentRetainsStaticValue reports whether any of the credentials' values
// survives in content after substitution. It tests both the literal content and,
// when the path's format decodes, every string the decoded document holds: a
// config file may store a value in its escaped form (JSON's \" and \\), which a
// literal search misses even though the harness reads the value. A literal hit
// is deliberately kept as well, so a value left in a comment or a file whose
// format cannot be decoded is still caught.
//
//deadcode:ignore reason="managed-config write-back finder; the after-run wiring supplies its caller"
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
		return false
	}
	return decodedContainsStaticValue(decoded, credentials)
}

// decodedContainsStaticValue reports whether any string a decoded document holds
// — a key or a leaf value — contains one of the credentials' values.
//
//deadcode:ignore reason="managed-config write-back finder; the after-run wiring supplies its caller"
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
//
//deadcode:ignore reason="managed-config write-back finder; the after-run wiring supplies its caller"
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
// env-var name. An occurrence with no recognizable assignment key before it is
// left untouched, so the caller's value-remains check refuses the file rather
// than emitting the value.
//
//deadcode:ignore reason="managed-config write-back finder; the after-run wiring supplies its caller"
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
		builder.WriteString(remaining[:keyStart])
		builder.WriteString(indirectionKey)
		builder.WriteString(remaining[keyEnd:index])
		builder.WriteString(credential.EnvVar)
		remaining = remaining[index+len(value):]
	}
}

// assignmentKeyRange returns the half-open range of the key token that
// introduces the value starting at valueStart, when that value sits on the
// right-hand side of a '=' or ':' assignment. The range includes the key's
// quotes, if any. It reports false when no key token precedes the value.
//
//deadcode:ignore reason="managed-config write-back finder; the after-run wiring supplies its caller"
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
	}
	return start, end, true
}

// skipSpaceBackward returns index moved left past any ASCII whitespace.
//
//deadcode:ignore reason="managed-config write-back finder; the after-run wiring supplies its caller"
func skipSpaceBackward(content string, index int) int {
	for index > 0 && isSpaceByte(content[index-1]) {
		index--
	}
	return index
}

// isSpaceByte reports whether b is ASCII whitespace.
//
//deadcode:ignore reason="managed-config write-back finder; the after-run wiring supplies its caller"
func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

// isQuote reports whether b opens or closes a quoted config value or key.
//
//deadcode:ignore reason="managed-config write-back finder; the after-run wiring supplies its caller"
func isQuote(b byte) bool { return b == '"' || b == '\'' }

// isKeyChar reports whether b is part of a config key token. A '.' is excluded,
// so a dotted key's final segment is the token that is rewritten.
//
//deadcode:ignore reason="managed-config write-back finder; the after-run wiring supplies its caller"
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

// readBoundedFile reads path's contents, bounded by readback.MaxHashedBytes,
// mirroring the read-back's own capped read. ok is false when the file cannot be
// read or exceeds the cap.
//
//deadcode:ignore reason="managed-config write-back finder; the after-run wiring supplies its caller"
func readBoundedFile(path string) ([]byte, bool) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, readback.MaxHashedBytes+1))
	if err != nil {
		return nil, false
	}
	if int64(len(data)) > readback.MaxHashedBytes {
		return nil, false
	}
	return data, true
}
