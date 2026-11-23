package analyser

import (
	"path/filepath"
	"slices"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
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
// base name. It delegates to the shared agent predicate, so the write-back and
// the first-run host seed agree on which config files the merge actually reads.
func snippetNameMatches(agentName, configPath string) bool {
	return agent.MergesSnippetName(agentName, configPath)
}

// changed reports whether the read-back saw configPath change since baseline.
func (f ManagedConfigFacts) changed(configPath string) bool {
	return slices.Contains(f.ReadBack.Changed, configPath)
}

// sanitize reads configPath's content under the read-back's cap and replaces
// every static secret value the file's own credentials hold, through the shared
// readback sanitizer. It reports false, after naming the file through the
// logger, when the file cannot be read or when the sanitizer refuses it — a
// credential with no substitutable value, or a value that survives — so no value
// ever reaches the finding. The substitution is per-file, but the value-remains
// check runs against the whole static set: a value classified from another file
// can appear verbatim in this candidate, and only the whole set can prove the
// file is value-free.
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
	sanitized, ok := readback.SanitizeConfig(content, readback.SanitizeParams{
		Path:                       configPath,
		Credentials:                credentials,
		AllCredentials:             allStatic,
		AllowedEnvVars:             secretEnvVars,
		IndirectionKey:             indirectionKey,
		RefuseUndeclaredSecretKeys: true,
	})
	if !ok {
		f.warnf(
			"skipping managed agent config %s: it holds a credential that cannot be replaced by a placeholder",
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
// so readback.SanitizeConfig refuses its file rather than emitting the value.
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
