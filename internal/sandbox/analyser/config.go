package analyser

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configread"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/viperconfig"
)

// evidenceChannelConfig marks a finding derived from the effective
// configuration alone, with no session observation and no home read-back.
const evidenceChannelConfig = "config"

// secretFileMode is the mode a secret file must have: read and written by its
// owner only.
const secretFileMode = 0o600

// wildcardEgressEntries are the egress-allow entries that admit every host.
var wildcardEgressEntries = []string{"*", "0.0.0.0/0", "::/0"}

// ConfigFacts is the effective configuration harden audits.
type ConfigFacts struct {
	Paths  configpaths.ConfigPaths
	Slug   string                    // the per-slug user scope + decline key
	Config *viperconfig.Config       // effective launcher config (network, provisioning)
	Scopes []configpaths.ConfigScope // to locate each scope's env.secret.yaml
	Logger termio.Logger
}

// ConfigFindings returns the config-only (KindConfig) findings the effective
// configuration supports (Design decision 2). It never widens and never removes
// a user-set allow entry: every write narrows a posture, drops a wildcard or a
// dangerous flag, tightens a mode, or moves a secret to a lower-precedence
// scope.
func ConfigFindings(f ConfigFacts) []Finding {
	var findings []Finding
	for _, scope := range f.Scopes {
		findings = append(findings, f.secretFindingsForScope(scope)...)
	}
	if f.Config != nil {
		findings = append(findings, f.policyFindings()...)
		findings = append(findings, f.inlineConfigFindings()...)
	}
	for i := range findings {
		findings[i] = findings[i].withEvidenceHash()
	}
	sortFindings(findings)
	return findings
}

// secretFindingsForScope returns the findings a scope's env.secret.yaml
// supports: a project-scope secret is moved to the trusted per-slug scope (C1),
// a dangerous secret is narrowed in place (C2), and a broadly readable secret
// file is tightened (C5).
func (f ConfigFacts) secretFindingsForScope(scope configpaths.ConfigScope) []Finding {
	var findings []Finding
	specs := envsecret.ParseSecretSpecYAML(scope.EnvSecretYAMLFile(), f.Logger)
	for _, envVar := range sortedSpecKeys(specs) {
		spec := specs[envVar]
		if scope.Kind == configpaths.ScopeProject {
			// A project secret is moved whole by C1; narrowing it in place as
			// well would let the C2 rewrite re-create the entry C1 removed.
			findings = append(findings, projectSecretMoveFinding(envVar, spec))
			continue
		}
		if spec.AllowAnyHostDangerous {
			findings = append(findings, dangerousSecretFinding(scope, envVar, spec))
		}
	}
	if finding, ok := secretFileModeFinding(scope); ok {
		findings = append(findings, finding)
	}
	return findings
}

// projectSecretMoveFinding is C1: a static secret committed in the project scope
// is removed from the committed file and added at the per-slug user scope. Its
// value is resolved in memory by the applier and never carried in the finding.
func projectSecretMoveFinding(envVar string, spec envsecret.SecretSpec) Finding {
	hosts := specHosts(spec)
	headerFields := sortedUnique(spec.HeaderFields)
	//exhaustruct:ignore // a removal write names no value and keeps the entry's hosts
	remove := Write{
		Scope: ScopeProject,
		//exhaustruct:ignore // the removal carries no value, only the key to drop
		Secret: &SecretWrite{
			EnvVar:       envVar,
			Hosts:        hosts,
			HeaderFields: headerFields,
			Remove:       true,
		},
		Text: "Removing the secret from the file does not remove it from git history; rotate the credential.",
	}
	//exhaustruct:ignore // an addition names no value and never removes
	add := Write{
		Scope: ScopePerSlugUser,
		//exhaustruct:ignore // a proposal never removes
		Secret: &SecretWrite{
			EnvVar:       envVar,
			Hosts:        hosts,
			HeaderFields: headerFields,
		},
		Text: "The per-slug user config scopes this secret, keyed by the repo slug, so it applies to every clone of the remote.",
	}
	//exhaustruct:ignore // a config finding reports no denial and no count
	evidence := Evidence{
		Summary:      "static secret " + envVar + " is committed in the project scope",
		Destinations: hosts,
		HeaderFields: headerFields,
		Channel:      evidenceChannelConfig,
	}
	//exhaustruct:ignore // EvidenceHash is stamped by ConfigFindings
	return Finding{Evidence: evidence, Writes: []Write{remove, add}, Kind: KindConfig}
}

// dangerousSecretFinding is C2: a secret that allows any host is narrowed to its
// declared hosts, dropping the dangerous flag. The applier's secret writer
// always writes AllowAnyHostDangerous false, so the rewrite is the drop. A
// dangerous secret with no host has nothing to narrow to and is reported, not
// written.
func dangerousSecretFinding(scope configpaths.ConfigScope, envVar string, spec envsecret.SecretSpec) Finding {
	hosts := specHosts(spec)
	headerFields := sortedUnique(spec.HeaderFields)
	//exhaustruct:ignore // a config finding reports no denial and no count
	evidence := Evidence{
		Summary:      "static secret " + envVar + " allows any host",
		Destinations: hosts,
		HeaderFields: headerFields,
		Channel:      evidenceChannelConfig,
	}
	if len(hosts) == 0 {
		//exhaustruct:ignore // a reported-only finding proposes no write
		return Finding{Evidence: evidence, Kind: KindConfig}
	}
	//exhaustruct:ignore // a secret write names no config key and no mode
	write := Write{
		Scope: scopeWriteScope(scope.Kind),
		//exhaustruct:ignore // a proposal never removes
		Secret: &SecretWrite{
			EnvVar:       envVar,
			Hosts:        hosts,
			HeaderFields: headerFields,
		},
		Text: "This drops allow_any_host_dangerous and keeps the declared host list.",
	}
	//exhaustruct:ignore // EvidenceHash is stamped by ConfigFindings
	return Finding{Evidence: evidence, Writes: []Write{write}, Kind: KindConfig}
}

// secretFileModeFinding is C5: a scope's env.secret.yaml that is readable beyond
// its owner is tightened to secretFileMode. A scope with no secret file has
// nothing to tighten, so it yields no finding.
func secretFileModeFinding(scope configpaths.ConfigScope) (Finding, bool) {
	path := scope.EnvSecretYAMLFile()
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() == secretFileMode {
		return Finding{}, false
	}
	//exhaustruct:ignore // a file-mode finding reports no destinations and no count
	evidence := Evidence{
		Summary: "the secret file in the " + scopeLabel(scope.Kind) + " is readable beyond its owner",
		Channel: evidenceChannelConfig,
	}
	//exhaustruct:ignore // a file-mode write carries no key, value, or secret
	write := Write{
		Scope:    scopeWriteScope(scope.Kind),
		FileMode: &FileModeWrite{Mode: secretFileMode},
		Text:     "This tightens env.secret.yaml to 0600.",
	}
	//exhaustruct:ignore // EvidenceHash is stamped by ConfigFindings
	return Finding{Evidence: evidence, Writes: []Write{write}, Kind: KindConfig}, true
}

// policyFindings returns the network-posture (C3/C4) and provisioning (C6)
// findings the effective config supports.
func (f ConfigFacts) policyFindings() []Finding {
	policy := f.Config.Network.Effective()
	var findings []Finding
	if finding, ok := f.broadProfileFinding(policy); ok {
		findings = append(findings, finding)
	}
	if finding, ok := f.wildcardEgressFinding(policy); ok {
		findings = append(findings, finding)
	}
	if finding, ok := f.provisionHostConfigFinding(); ok {
		findings = append(findings, finding)
	}
	return findings
}

// broadProfileFinding is C3: a policy whose profile admits broad egress is
// narrowed to the terminal none profile, keeping the current allow list. An
// empty allow list has nothing to keep, so narrowing would deny all egress: it
// is reported, not written.
func (f ConfigFacts) broadProfileFinding(policy network.Policy) (Finding, bool) {
	if slices.Equal(policy.Profiles, []network.Profile{network.ProfileNone}) {
		return Finding{}, false
	}
	profiles := profileList(policy.Profiles)
	if len(policy.EgressAllow) == 0 {
		//exhaustruct:ignore // a config finding reports no denial and no count
		evidence := Evidence{
			Summary: "the network profile " + profiles + " admits broad egress with no egress-allow entries; " +
				"narrowing to none would deny all egress",
			Channel: evidenceChannelConfig,
		}
		//exhaustruct:ignore // a reported-only finding proposes no write
		return Finding{Evidence: evidence, Kind: KindConfig}, true
	}
	//exhaustruct:ignore // a config finding reports no denial and no count
	evidence := Evidence{
		Summary:      "the network profile " + profiles + " admits broad egress",
		Destinations: sortedUnique(policy.EgressAllow),
		Channel:      evidenceChannelConfig,
	}
	//exhaustruct:ignore // a config write names no secret, mode, or removal
	write := Write{
		Scope:       f.ownerScope("network.profile"),
		ConfigKey:   "network.profile",
		ConfigValue: network.ProfileNone.String(),
		Text:        "This sets the terminal none profile and keeps the current egress-allow list, so only the listed hosts remain reachable.",
	}
	//exhaustruct:ignore // EvidenceHash is stamped by ConfigFindings
	return Finding{Evidence: evidence, Writes: []Write{write}, Kind: KindConfig}, true
}

// wildcardEgressFinding is C4: an egress-allow entry that admits every host is
// dropped, keeping the remaining entries.
func (f ConfigFacts) wildcardEgressFinding(policy network.Policy) (Finding, bool) {
	var kept, dropped []string
	for _, entry := range policy.EgressAllow {
		if slices.Contains(wildcardEgressEntries, entry) {
			dropped = append(dropped, entry)
			continue
		}
		kept = append(kept, entry)
	}
	if len(dropped) == 0 {
		return Finding{}, false
	}
	remaining := sortedUnique(kept)
	//exhaustruct:ignore // a config finding reports no denial and no count
	evidence := Evidence{
		Summary:      "the egress-allow list contains a wildcard entry: " + strings.Join(sortedUnique(dropped), ", "),
		Destinations: remaining,
		Channel:      evidenceChannelConfig,
	}
	//exhaustruct:ignore // a config write names no secret, mode, or removal
	write := Write{
		Scope:       f.ownerScope("network.egress-allow"),
		ConfigKey:   "network.egress-allow",
		ConfigValue: remaining,
		Text:        "This drops the wildcard entry and keeps the remaining allow entries.",
	}
	//exhaustruct:ignore // EvidenceHash is stamped by ConfigFindings
	return Finding{Evidence: evidence, Writes: []Write{write}, Kind: KindConfig}, true
}

// provisionHostConfigFinding is C6: host-config provisioning is turned off. Its
// Text points at a learning run, which derives a managed config before the
// host-config copy is disabled.
func (f ConfigFacts) provisionHostConfigFinding() (Finding, bool) {
	if !f.Config.ProvisionHostConfig {
		return Finding{}, false
	}
	//exhaustruct:ignore // a config finding reports no destinations and no count
	evidence := Evidence{
		Summary: "host-config provisioning copies the agent's host config into the VM",
		Channel: evidenceChannelConfig,
	}
	//exhaustruct:ignore // a config write names no secret, mode, or removal
	write := Write{
		Scope:       f.ownerScope("provision-host-config"),
		ConfigKey:   "provision-host-config",
		ConfigValue: false,
		Text:        "This turns host-config provisioning off. Run `agents-sandbox run --learn` first to derive a managed config from a real session.",
	}
	//exhaustruct:ignore // EvidenceHash is stamped by ConfigFindings
	return Finding{Evidence: evidence, Writes: []Write{write}, Kind: KindConfig}, true
}

// inlineConfigFindings is C7: a static secret left inline in a managed agent
// config is moved to env.secret.yaml, and the config keeps a reference to the
// named env var. The value stays in memory; the finding carries the env-var name
// and hosts the entry declares, never a guessed host.
func (f ConfigFacts) inlineConfigFindings() []Finding {
	profile, ok := agent.Lookup(f.Config.Agent)
	if !ok {
		return nil
	}
	names, err := agent.ConfigDirNames(profile)
	if err != nil {
		return nil
	}
	var findings []Finding
	for _, scope := range f.Scopes {
		for _, name := range names {
			findings = append(findings, inlineConfigFindingsInScope(scope, scope.AgentConfigDir(name))...)
		}
	}
	return findings
}

// inlineConfigFindingsInScope classifies every regular file under the scope's
// agent config dir and returns a finding per inline static credential. A file
// the reader cannot read, or that names no credential, contributes nothing.
func inlineConfigFindingsInScope(scope configpaths.ConfigScope, dir string) []Finding {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var findings []Finding
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := configread.ReadFile(path)
		if err != nil {
			continue
		}
		for _, credential := range readback.InlineCredentialsIn(path, data) {
			if finding, ok := inlineConfigFinding(scope, credential); ok {
				findings = append(findings, finding)
			}
		}
	}
	return findings
}

// inlineConfigFinding builds one C7 finding from a classified inline credential:
// a secret write adding the value at the per-slug user scope, plus a
// declarative reference write that names the env-var placeholder the config
// keeps. A credential without an env-var name, without a host, or without a
// literal value cannot be moved, so it yields no finding.
func inlineConfigFinding(scope configpaths.ConfigScope, credential readback.Credential) (Finding, bool) {
	hosts := sortedUnique(credential.Hosts)
	if credential.EnvVar == "" || len(hosts) == 0 || credential.Value == "" {
		return Finding{}, false
	}
	//exhaustruct:ignore // a config finding reports no denial and no count
	evidence := Evidence{
		Summary:      "static secret " + credential.EnvVar + " is inline in the managed agent config",
		Destinations: hosts,
		Channel:      evidenceChannelConfig,
	}
	//exhaustruct:ignore // a secret write names no config key and no mode
	secretWrite := Write{
		Scope: ScopePerSlugUser,
		//exhaustruct:ignore // a proposal never removes
		Secret: &SecretWrite{
			EnvVar:   credential.EnvVar,
			Provider: credential.Provider,
			Hosts:    hosts,
		},
		Text: "This moves the value to env.secret.yaml at the per-slug user scope.",
	}
	//exhaustruct:ignore // the reference write carries only its scope and text
	referenceWrite := Write{
		Scope: scopeWriteScope(scope.Kind),
		Text: "In the agent config, replace the literal value with the reference ${" +
			credential.EnvVar + "}; the runtime substitutes it for the declared hosts.",
	}
	//exhaustruct:ignore // EvidenceHash is stamped by ConfigFindings
	return Finding{Evidence: evidence, Writes: []Write{secretWrite, referenceWrite}, Kind: KindConfig}, true
}

// ownerScope returns the scope harden writes a launcher-config key to: the
// project scope when the project layer owns the key (the write then goes through
// the trust gate), and the per-slug user scope otherwise (where the applier
// reports and skips a key the project shadows).
func (f ConfigFacts) ownerScope(key string) Scope {
	if owner, owned := viperconfig.LayerSettingOwner(
		f.Paths,
		f.Slug,
		key,
	); owned &&
		owner == f.Paths.ProjectConfigDir() {
		return ScopeProject
	}
	return ScopePerSlugUser
}

// scopeWriteScope maps a config scope to the analyser write scope harden can
// address: the project scope for the project layer, and the per-slug user scope
// otherwise — the applier's only non-project target, so a generic-user finding
// is written at the trusted per-slug scope.
func scopeWriteScope(kind configpaths.ScopeKind) Scope {
	if kind == configpaths.ScopeProject {
		return ScopeProject
	}
	return ScopePerSlugUser
}

// scopeLabel names a config scope in a finding's evidence.
func scopeLabel(kind configpaths.ScopeKind) string {
	switch kind {
	case configpaths.ScopeProject:
		return "project scope"
	case configpaths.ScopePerSlugUser:
		return "per-slug user scope"
	case configpaths.ScopeGenericUser:
		return "generic user scope"
	default:
		return "config scope"
	}
}

// specHosts returns a secret spec's declared hosts: its hosts list plus its
// single host, sorted and de-duplicated.
func specHosts(spec envsecret.SecretSpec) []string {
	hosts := slices.Clone(spec.Hosts)
	if spec.Host != "" {
		hosts = append(hosts, spec.Host)
	}
	return sortedUnique(hosts)
}

// sortedSpecKeys returns a secret-spec map's keys in sorted order, so findings
// do not depend on map iteration order.
func sortedSpecKeys(specs map[string]envsecret.SecretSpec) []string {
	keys := make([]string, 0, len(specs))
	for key := range specs {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// profileList renders a policy's profiles as a comma-separated list.
func profileList(profiles []network.Profile) string {
	names := make([]string, len(profiles))
	for i, profile := range profiles {
		names[i] = profile.String()
	}
	return strings.Join(names, ", ")
}

// sortFindings orders findings deterministically, so a re-run over the same
// configuration emits the same sequence.
func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		return findingSortKey(findings[i]) < findingSortKey(findings[j])
	})
}

// findingSortKey is the ordering key sortFindings compares: the finding's kind,
// its evidence summary, then each write's scope, key, and target.
func findingSortKey(f Finding) string {
	var key strings.Builder
	key.WriteString(string(f.Kind))
	key.WriteByte(0)
	key.WriteString(f.Evidence.Summary)
	for _, write := range f.Writes {
		key.WriteByte(0)
		key.WriteString(string(write.Scope))
		key.WriteByte(0)
		key.WriteString(write.ConfigKey)
		if write.Secret != nil {
			key.WriteString(write.Secret.EnvVar)
		}
		if write.FileMode != nil {
			key.WriteString("file-mode")
		}
	}
	return key.String()
}
