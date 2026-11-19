package analyser

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/projectguard"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
	"github.com/inoio/agents-sandbox/internal/testutil"
	"github.com/inoio/agents-sandbox/internal/viperconfig"
)

// configFacts builds the harden facts over a fresh mock config tree, with the
// ordered scopes and a test logger.
func configFacts(t *testing.T, cfg *viperconfig.Config) ConfigFacts {
	t.Helper()
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	if cfg == nil {
		cfg = &viperconfig.Config{}
	}
	return ConfigFacts{
		Paths:  paths,
		Slug:   testSlug,
		Config: cfg,
		Scopes: configpaths.ConfigScopes(paths, testSlug),
		Logger: &termiotest.Mock{},
	}
}

// writeSecretFile writes a scope's env.secret.yaml.
func writeSecretFile(t *testing.T, dir, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o750))
	testutil.WriteFile(t, dir, configpaths.EnvSecretFileName+".yaml", content)
}

// tightFile restricts a scope's env.secret.yaml to 0600, so a finding test that
// only cares about secrets or the network posture is not also reporting C5.
func tightFile(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.Chmod(filepath.Join(dir, configpaths.EnvSecretFileName+".yaml"), 0o600))
}

// findingWithSecretFor returns the finding carrying a secret write for envVar.
func findingWithSecretFor(t *testing.T, findings []Finding, envVar string) Finding {
	t.Helper()
	for _, finding := range findings {
		for _, write := range finding.Writes {
			if write.Secret != nil && write.Secret.EnvVar == envVar {
				return finding
			}
		}
	}
	t.Fatalf("no finding carries a secret write for %s: %+v", envVar, findings)
	return Finding{}
}

// secretWriteFor returns the finding's secret write with the given removal state.
func secretWriteFor(t *testing.T, finding Finding, remove bool) Write {
	t.Helper()
	for _, write := range finding.Writes {
		if write.Secret != nil && write.Secret.Remove == remove {
			return write
		}
	}
	t.Fatalf("finding has no secret write with remove=%v: %+v", remove, finding)
	return Write{}
}

// referenceWrite returns the finding's declarative reference write.
func referenceWrite(t *testing.T, finding Finding) Write {
	t.Helper()
	for _, write := range finding.Writes {
		if write.Secret == nil && write.FileMode == nil && write.ConfigKey == "" {
			return write
		}
	}
	t.Fatalf("finding has no reference write: %+v", finding)
	return Write{}
}

// configWriteFor returns the first write proposing key.
func configWriteFor(t *testing.T, findings []Finding, key string) Write {
	t.Helper()
	for _, finding := range findings {
		for _, write := range finding.Writes {
			if write.ConfigKey == key {
				return write
			}
		}
	}
	t.Fatalf("no finding proposes a %s write: %+v", key, findings)
	return Write{}
}

// findingWithSummaryContaining returns the finding whose evidence summary
// contains substring.
func findingWithSummaryContaining(t *testing.T, findings []Finding, substring string) Finding {
	t.Helper()
	for _, finding := range findings {
		if strings.Contains(finding.Evidence.Summary, substring) {
			return finding
		}
	}
	t.Fatalf("no finding's summary contains %q: %+v", substring, findings)
	return Finding{}
}

// fileModeFinding returns the finding proposing a file-mode write.
func fileModeFinding(t *testing.T, findings []Finding) Finding {
	t.Helper()
	for _, finding := range findings {
		for _, write := range finding.Writes {
			if write.FileMode != nil {
				return finding
			}
		}
	}
	t.Fatalf("no finding proposes a file-mode write: %+v", findings)
	return Finding{}
}

// writeNarrowsOrRelocates reports whether a write only narrows the effective
// configuration or relocates a secret to a lower-precedence scope.
func writeNarrowsOrRelocates(write Write, currentAllow []string) bool {
	switch {
	case write.Remove, write.FileMode != nil, writeOnlyRendersText(write):
		return true
	case write.Secret != nil:
		return !slices.Contains(write.Secret.Hosts, "*")
	case write.ConfigKey == "network.profile":
		return write.ConfigValue == network.ProfileNone.String()
	case write.ConfigKey == "network.egress-allow":
		proposed, ok := write.ConfigValue.([]string)
		return ok && isSubset(proposed, currentAllow)
	case write.ConfigKey == "provision-host-config":
		return write.ConfigValue == false
	default:
		return false
	}
}

// isSubset reports whether every entry of subset is in superset.
func isSubset(subset, superset []string) bool {
	for _, entry := range subset {
		if !slices.Contains(superset, entry) {
			return false
		}
	}
	return true
}

// TestConfigFindingsMovesAProjectSecret pins C1: a secret committed in the
// project scope yields one finding whose two writes remove it from the project
// file and add it at the per-slug user scope, with the rotation warning.
func TestConfigFindingsMovesAProjectSecret(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{})
	writeSecretFile(t, f.Paths.ProjectConfigDir(), "OPENAI_API_KEY:\n  value: sk-project\n  host: api.openai.com\n")

	findings := ConfigFindings(f)

	finding := findingWithSecretFor(t, findings, "OPENAI_API_KEY")
	require.Len(t, finding.Writes, 2)

	remove := secretWriteFor(t, finding, true)
	require.Equal(t, ScopeProject, remove.Scope)
	require.Equal(t, []string{"api.openai.com"}, remove.Secret.Hosts)
	require.Contains(t, remove.Text, "git history")
	require.Contains(t, remove.Text, "rotate")

	add := secretWriteFor(t, finding, false)
	require.Equal(t, ScopePerSlugUser, add.Scope)
	require.Equal(t, []string{"api.openai.com"}, add.Secret.Hosts)
}

// TestConfigFindingsDropsDangerousFlagWithHosts pins C2: a secret that allows
// any host and declares a host is rewritten without the flag.
func TestConfigFindingsDropsDangerousFlagWithHosts(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{})
	writeSecretFile(t, f.Paths.UserSlugConfigDir(testSlug),
		"CUSTOM_TOKEN:\n  value: sk-dangerous\n  host: api.example\n  allow_any_host_dangerous: true\n")

	findings := ConfigFindings(f)

	finding := findingWithSecretFor(t, findings, "CUSTOM_TOKEN")
	write := secretWriteFor(t, finding, false)
	require.Equal(t, ScopePerSlugUser, write.Scope)
	require.Equal(t, []string{"api.example"}, write.Secret.Hosts)
	require.Contains(t, write.Text, "allow_any_host_dangerous")
}

// TestConfigFindingsReportsDangerousWithoutHosts pins C2's foot-gun: a
// dangerous secret with no host has nothing to narrow to, so it is reported and
// proposes no write.
func TestConfigFindingsReportsDangerousWithoutHosts(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{})
	writeSecretFile(t, f.Paths.UserSlugConfigDir(testSlug),
		"CUSTOM_TOKEN:\n  value: sk-dangerous\n  allow_any_host_dangerous: true\n")

	findings := ConfigFindings(f)

	finding := findingWithSummaryContaining(t, findings, "CUSTOM_TOKEN")
	require.Empty(t, finding.Writes, "a dangerous secret with no host is reported, not written")
}

// TestConfigFindingsNarrowsBroadProfile pins C3: a broad profile with a
// non-empty allow list is narrowed to none, kept at the per-slug scope, and the
// allow list is left untouched.
func TestConfigFindingsNarrowsBroadProfile(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{Network: network.Policy{
		Profiles:    []network.Profile{network.ProfilePublic},
		EgressAllow: []string{"api.example"},
	}})

	findings := ConfigFindings(f)

	profile := configWriteFor(t, findings, "network.profile")
	require.Equal(t, ScopePerSlugUser, profile.Scope)
	require.Equal(t, network.ProfileNone.String(), profile.ConfigValue)
	for _, finding := range findings {
		for _, write := range finding.Writes {
			require.NotEqual(t, "network.egress-allow", write.ConfigKey,
				"narrowing the profile keeps the allow list; it does not rewrite it")
		}
	}

	t.Run("the project layer owns the key", func(t *testing.T) {
		pf := configFacts(t, &viperconfig.Config{Network: network.Policy{
			Profiles:    []network.Profile{network.ProfilePublic},
			EgressAllow: []string{"api.example"},
		}})
		testutil.WriteFile(t, pf.Paths.ProjectConfigDir(), "config.yaml", "network:\n  profile: public\n")

		findings := ConfigFindings(pf)

		profile := configWriteFor(t, findings, "network.profile")
		require.Equal(t, ScopeProject, profile.Scope,
			"a key the project layer owns is narrowed in the project scope")
	})
}

// TestConfigFindingsEmptyAllowIsReportedNotWritten pins C3's foot-gun: a broad
// profile with an empty allow list is reported, since narrowing to none would
// deny all egress.
func TestConfigFindingsEmptyAllowIsReportedNotWritten(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{Network: network.Policy{
		Profiles: []network.Profile{network.ProfilePublic},
	}})

	findings := ConfigFindings(f)

	require.Len(t, findings, 1, "a broad profile with no allow list is reported once")
	require.Equal(t, KindConfig, findings[0].Kind)
	require.Contains(t, findings[0].Evidence.Summary, "no egress-allow entries",
		"the reported-only finding names why narrowing would deny all egress")
	require.Empty(t, findings[0].Writes)
}

// TestConfigFindingsRemovesWildcardEntry pins C4: a wildcard egress entry is
// dropped and the remaining entries are kept.
func TestConfigFindingsRemovesWildcardEntry(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{Network: network.Policy{
		Profiles:    []network.Profile{network.ProfileNone},
		EgressAllow: []string{"*", "api.example"},
	}})

	findings := ConfigFindings(f)

	write := configWriteFor(t, findings, "network.egress-allow")
	require.Equal(t, ScopePerSlugUser, write.Scope)
	require.Equal(t, []string{"api.example"}, write.ConfigValue)
}

// TestConfigFindingsAllWildcardEgressIsReportedNotWritten pins the C3/C4
// interaction: an egress-allow list holding only wildcard entries has nothing to
// keep, so neither C3's profile narrowing nor C4's wildcard drop writes — the
// pair would otherwise leave none + an empty allow list, denying all egress.
func TestConfigFindingsAllWildcardEgressIsReportedNotWritten(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{Network: network.Policy{
		Profiles:    []network.Profile{network.ProfilePublic},
		EgressAllow: []string{"*", "0.0.0.0/0"},
	}})

	findings := ConfigFindings(f)

	require.Len(t, findings, 2, "the profile and the egress finding are both reported")
	for _, finding := range findings {
		require.Equal(t, KindConfig, finding.Kind)
		require.Contains(t, finding.Evidence.Summary, "only wildcard entries",
			"the reported-only finding names why dropping the last wildcards would deny all egress")
		require.Empty(t, finding.Writes)
	}
}

// TestConfigFindingsEmptyEgressEntriesDoNotCountAsReachable pins that an empty
// egress entry, which the network spec drops, is not a reachable host: a list
// holding only empty entries is no allow list, so C3 reports rather than
// narrowing to none with nothing reachable.
func TestConfigFindingsEmptyEgressEntriesDoNotCountAsReachable(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{Network: network.Policy{
		Profiles:    []network.Profile{network.ProfilePublic},
		EgressAllow: []string{""},
	}})

	findings := ConfigFindings(f)

	require.Len(t, findings, 1, "an empty allow list is reported once")
	require.Contains(t, findings[0].Evidence.Summary, "no egress-allow entries")
	require.Empty(t, findings[0].Writes)
}

// TestConfigFindingsWildcardPlusEmptyEgressIsReported pins that an empty entry
// beside a wildcard does not keep the list reachable: dropping the wildcard
// leaves nothing, so C3 and C4 report rather than leave none with an empty allow
// list, which would deny all egress.
func TestConfigFindingsWildcardPlusEmptyEgressIsReported(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{Network: network.Policy{
		Profiles:    []network.Profile{network.ProfilePublic},
		EgressAllow: []string{"*", ""},
	}})

	findings := ConfigFindings(f)

	require.Len(t, findings, 2, "the profile and the egress finding are both reported")
	for _, finding := range findings {
		require.Contains(t, finding.Evidence.Summary, "only wildcard entries")
		require.Empty(t, finding.Writes)
	}
}

// TestConfigFindingsSecretFileMode pins C5: a broadly readable secret file
// yields a finding that tightens it to 0600.
func TestConfigFindingsSecretFileMode(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{})
	dir := f.Paths.UserSlugConfigDir(testSlug)
	writeSecretFile(t, dir, "OPENAI_API_KEY:\n  value: sk\n  host: api.example\n")
	secretPath := filepath.Join(dir, configpaths.EnvSecretFileName+".yaml")
	require.NoError(t, os.Chmod(secretPath, 0o644))

	findings := ConfigFindings(f)

	finding := fileModeFinding(t, findings)
	write := finding.Writes[0]
	require.Equal(t, ScopePerSlugUser, write.Scope)
	require.NotNil(t, write.FileMode)
	require.Equal(t, os.FileMode(0o600), write.FileMode.Mode)
}

// TestConfigFindingsProvisionHostConfig pins C6: provisioning on yields a
// finding proposing it off, with its Text naming the learning run.
func TestConfigFindingsProvisionHostConfig(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{ProvisionHostConfig: true})

	findings := ConfigFindings(f)

	write := configWriteFor(t, findings, "provision-host-config")
	require.Equal(t, false, write.ConfigValue)
	require.Contains(t, write.Text, "run --learn")
}

// TestConfigFindingsMovesAnInlineConfigSecret pins C7: an inline provider key in
// a managed agent config yields a finding that moves the value to
// env.secret.yaml and leaves a reference to the env var in the config.
func TestConfigFindingsMovesAnInlineConfigSecret(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{Agent: "opencode"})
	dir := filepath.Join(f.Paths.UserSlugConfigDir(testSlug), "opencode")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	testutil.WriteFile(t, dir, "opencode.jsonc",
		`{"provider":{"openai":{"options":{"apiKey":"sk-inline","baseURL":"https://api.openai.com/v1"}}}}`)

	findings := ConfigFindings(f)

	finding := findingWithSecretFor(t, findings, "OPENAI_API_KEY")
	require.Len(t, finding.Writes, 2)

	secret := secretWriteFor(t, finding, false)
	require.Equal(t, ScopePerSlugUser, secret.Scope)
	require.Equal(t, "openai", secret.Secret.Provider)
	require.Equal(t, []string{"api.openai.com"}, secret.Secret.Hosts)

	reference := referenceWrite(t, finding)
	require.Contains(t, reference.Text, "${OPENAI_API_KEY}")
	require.Empty(t, reference.ConfigKey)
	require.Nil(t, reference.Secret)
	require.Nil(t, reference.FileMode)
}

// TestConfigFindingsReportsAnInlineCredentialThePerSlugScopeAlreadyDeclares pins
// that C7 does not move an inline credential whose env var the per-slug secret
// file already declares: C7's only secret target is the per-slug scope, so the
// write would overwrite the value and host scope the user set there.
func TestConfigFindingsReportsAnInlineCredentialThePerSlugScopeAlreadyDeclares(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{Agent: "opencode"})
	dir := filepath.Join(f.Paths.UserConfigDir(), "opencode")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	testutil.WriteFile(t, dir, "opencode.jsonc",
		`{"provider":{"openai":{"options":{"apiKey":"sk-inline","baseURL":"https://api.openai.com/v1"}}}}`)
	writeSecretFile(t, f.Paths.UserSlugConfigDir(testSlug),
		"OPENAI_API_KEY:\n  value: sk-repo\n  host: api.corp.example\n")
	tightFile(t, f.Paths.UserSlugConfigDir(testSlug))

	findings := ConfigFindings(f)

	var inline []Finding
	for _, finding := range findings {
		if strings.Contains(finding.Evidence.Summary, "inline in the managed agent config") {
			inline = append(inline, finding)
		}
	}
	require.Len(t, inline, 1)
	require.Contains(t, inline[0].Evidence.Summary, "the per-slug scope already declares it")
	require.Empty(t, inline[0].Writes, "the per-slug entry must not be overwritten")
}

// TestConfigFindingsInlineWithoutAPerSlugScopeMovesTheCredential pins that with
// no per-slug scope in the projection, C7 still moves an inline credential: it
// has no per-slug declaration to protect.
func TestConfigFindingsInlineWithoutAPerSlugScopeMovesTheCredential(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{Agent: "opencode"})
	f.Scopes = []configpaths.ConfigScope{{Kind: configpaths.ScopeGenericUser, Dir: f.Paths.UserConfigDir()}}
	dir := filepath.Join(f.Paths.UserConfigDir(), "opencode")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	testutil.WriteFile(t, dir, "opencode.jsonc",
		`{"provider":{"openai":{"options":{"apiKey":"sk-inline","baseURL":"https://api.openai.com/v1"}}}}`)

	findings := ConfigFindings(f)

	finding := findingWithSecretFor(t, findings, "OPENAI_API_KEY")
	require.Len(t, finding.Writes, 2)
}

// TestConfigFindingsStampEvidenceHash pins that every finding carries the
// digest of its own evidence, so the decline memory keys on real evidence.
func TestConfigFindingsStampEvidenceHash(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{ProvisionHostConfig: true})

	findings := ConfigFindings(f)

	require.NotEmpty(t, findings)
	for _, finding := range findings {
		require.NotEmpty(t, finding.EvidenceHash)
		require.Equal(t, finding.Evidence.hash(), finding.EvidenceHash)
	}
}

// TestConfigFindingsAreDeterministic pins that two analyses of the same
// configuration emit the same findings in the same order, independent of map
// iteration order.
func TestConfigFindingsAreDeterministic(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{
		Agent:               "opencode",
		ProvisionHostConfig: true,
		Network: network.Policy{
			Profiles:    []network.Profile{network.ProfilePublic},
			EgressAllow: []string{"*", "api.example"},
		},
	})
	writeSecretFile(t, f.Paths.ProjectConfigDir(), "PROJECT_KEY:\n  value: sk-project\n  host: api.example\n")
	writeSecretFile(t, f.Paths.UserSlugConfigDir(testSlug),
		"CUSTOM_TOKEN:\n  value: sk-dangerous\n  host: api.example\n  allow_any_host_dangerous: true\n")
	dir := filepath.Join(f.Paths.UserSlugConfigDir(testSlug), "opencode")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	testutil.WriteFile(t, dir, "opencode.jsonc",
		`{"provider":{"openai":{"options":{"apiKey":"sk-inline","baseURL":"https://api.openai.com/v1"}}}}`)

	hashes := func(findings []Finding) []string {
		result := make([]string, len(findings))
		for i, finding := range findings {
			result[i] = finding.EvidenceHash
		}
		return result
	}

	first := hashes(ConfigFindings(f))
	require.NotEmpty(t, first)
	require.Equal(t, first, hashes(ConfigFindings(f)))
}

// TestConfigFindingsNeverWidens pins invariant 4 over a configuration that
// triggers every finding: each proposed write only narrows or relocates.
func TestConfigFindingsNeverWidens(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{
		Agent:               "opencode",
		ProvisionHostConfig: true,
		Network: network.Policy{
			Profiles:    []network.Profile{network.ProfilePublic},
			EgressAllow: []string{"*", "api.example"},
		},
	})
	writeSecretFile(t, f.Paths.ProjectConfigDir(), "PROJECT_KEY:\n  value: sk-project\n  host: api.example\n")
	writeSecretFile(t, f.Paths.UserSlugConfigDir(testSlug),
		"CUSTOM_TOKEN:\n  value: sk-dangerous\n  host: api.example\n  allow_any_host_dangerous: true\n")
	dir := filepath.Join(f.Paths.UserSlugConfigDir(testSlug), "opencode")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	testutil.WriteFile(t, dir, "opencode.jsonc",
		`{"provider":{"openai":{"options":{"apiKey":"sk-inline","baseURL":"https://api.openai.com/v1"}}}}`)

	findings := ConfigFindings(f)
	require.NotEmpty(t, findings)

	current := f.Config.Network.Effective().EgressAllow
	for _, finding := range findings {
		for _, write := range finding.Writes {
			require.Truef(t, writeNarrowsOrRelocates(write, current),
				"finding %q proposes a widening write: %+v", finding.Evidence.Summary, write)
		}
	}
}

// TestConfigFindingsSkipsALoginStoreInAScope pins invariant 2: a login store
// provisioned from a scope's agent config dir is never classified as a static
// secret, even when it is shaped like a provider entry the inline walk would
// otherwise classify (codex's auth.json carries a "token").
func TestConfigFindingsSkipsALoginStoreInAScope(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{
		Agent:   "codex",
		Network: network.Policy{Profiles: []network.Profile{network.ProfileNone}},
	})
	dir := filepath.Join(f.Paths.UserSlugConfigDir(testSlug), "codex")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	testutil.WriteFile(t, dir, "auth.json", `{"provider":"codex","token":"super-secret"}`)

	require.Empty(t, ConfigFindings(f), "a login store is not a static secret")
}

// TestConfigFindingsReadsAProjectAgentConfigThroughTheGuard pins that C7 reads a
// project-scope agent config through the project guard: an approved file yields
// a finding, and a file that is not approved is refused.
func TestConfigFindingsReadsAProjectAgentConfigThroughTheGuard(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{
		Agent:   "opencode",
		Network: network.Policy{Profiles: []network.Profile{network.ProfileNone}},
	})
	dir := filepath.Join(f.Paths.ProjectConfigDir(), "opencode")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	const content = `{"provider":{"openai":{"options":{"apiKey":"sk-project","baseURL":"https://api.openai.com/v1"}}}}`
	testutil.WriteFile(t, dir, "opencode.jsonc", content)
	path := filepath.Join(dir, "opencode.jsonc")

	t.Run("an approved project file yields a finding", func(t *testing.T) {
		restore := projectguard.Install(
			[]string{f.Paths.ProjectConfigDir()},
			map[string]projectguard.Approval{path: projectguard.ApprovalOf([]byte(content))},
		)
		t.Cleanup(restore)

		findings := ConfigFindings(f)

		require.Len(t, findings, 1)
		require.Contains(t, findings[0].Evidence.Summary, "OPENAI_API_KEY")
		require.Equal(t, ScopeProject, referenceWrite(t, findings[0]).Scope,
			"the reference write targets the scope the config lives in")
	})

	t.Run("an unapproved project file is refused", func(t *testing.T) {
		restore := projectguard.Install([]string{f.Paths.ProjectConfigDir()}, nil)
		t.Cleanup(restore)

		require.Empty(t, ConfigFindings(f))
	})
}

// TestConfigFindingsSecretFileModeSkipsTheGenericScope pins R8: the generic user
// scope's env.secret.yaml yields no C5 finding, since the applier has no
// generic-scope file-mode target and a file mode cannot be shadowed.
func TestConfigFindingsSecretFileModeSkipsTheGenericScope(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{Network: network.Policy{Profiles: []network.Profile{network.ProfileNone}}})
	dir := f.Paths.UserConfigDir()
	writeSecretFile(t, dir, "OPENAI_API_KEY:\n  value: sk\n  host: api.example\n")
	secretPath := filepath.Join(dir, configpaths.EnvSecretFileName+".yaml")
	require.NoError(t, os.Chmod(secretPath, 0o644))

	require.Empty(t, ConfigFindings(f), "the generic scope has no file-mode target")
}

// TestConfigFindingsInlineSourcesHashDistinctly pins that two inline copies of
// the same secret (opencode2's primary and alias dirs) yield two findings whose
// evidence hashes differ, so a decline for one does not skip the other.
func TestConfigFindingsInlineSourcesHashDistinctly(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{
		Agent:   "opencode2",
		Network: network.Policy{Profiles: []network.Profile{network.ProfileNone}},
	})
	const content = `{"provider":{"openai":{"options":{"apiKey":"sk-inline","baseURL":"https://api.openai.com/v1"}}}}`
	for _, name := range []string{"opencode2", "opencode"} {
		dir := filepath.Join(f.Paths.UserSlugConfigDir(testSlug), name)
		require.NoError(t, os.MkdirAll(dir, 0o750))
		testutil.WriteFile(t, dir, "opencode.jsonc", content)
	}

	findings := ConfigFindings(f)

	require.Len(t, findings, 2)
	require.NotEqual(t, findings[0].EvidenceHash, findings[1].EvidenceHash,
		"two inline sources of the same secret hash distinctly")
}

// TestConfigFindingsInlineSkipsAnUnknownAgent pins that C7 yields nothing when
// the configured agent is not a registered profile, rather than scanning a
// guessed config dir.
func TestConfigFindingsInlineSkipsAnUnknownAgent(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{
		Agent:   "no-such-agent",
		Network: network.Policy{Profiles: []network.Profile{network.ProfileNone}},
	})

	require.Empty(t, ConfigFindings(f))
}

// TestInlineConfigFindingsInScopeSkipsDirectories pins that a directory under an
// agent config dir is not read as a config file.
func TestInlineConfigFindingsInScopeSkipsDirectories(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{Network: network.Policy{Profiles: []network.Profile{network.ProfileNone}}})
	scope := f.Scopes[len(f.Scopes)-1]
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nested"), 0o750))

	require.Empty(t, inlineConfigFindingsInScope(scope, dir, nil))
}

// TestInlineConfigFindingRejectsAnIncompleteCredential pins that a credential
// without an env-var name, a host, or a literal value yields no finding, since
// there is nothing to move.
func TestInlineConfigFindingRejectsAnIncompleteCredential(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{Network: network.Policy{Profiles: []network.Profile{network.ProfileNone}}})
	scope := f.Scopes[len(f.Scopes)-1]

	_, ok := inlineConfigFinding(scope, readback.Credential{}, nil)

	require.False(t, ok, "an empty credential cannot be moved to env.secret.yaml")
}

// TestScopeLabelNamesEveryScope pins the scope label a finding's evidence uses
// for each scope kind, including the unknown-kind fallback.
func TestScopeLabelNamesEveryScope(t *testing.T) {
	labels := map[configpaths.ScopeKind]string{
		configpaths.ScopeProject:     "project scope",
		configpaths.ScopePerSlugUser: "per-slug user scope",
		configpaths.ScopeGenericUser: "generic user scope",
		configpaths.ScopeKind(99):    "config scope",
	}
	for kind, want := range labels {
		require.Equal(t, want, scopeLabel(kind))
	}
}

// TestConfigFindingsOrderBySummaryNotGenerationOrder pins that ConfigFindings
// orders the emitted findings by their key rather than leaving them in the order
// the finder built them: the policy finding is appended after the project-secret
// finding, but its summary sorts first.
func TestConfigFindingsOrderBySummaryNotGenerationOrder(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{
		ProvisionHostConfig: true,
		Network:             network.Policy{Profiles: []network.Profile{network.ProfileNone}},
	})
	writeSecretFile(t, f.Paths.ProjectConfigDir(), "PROJECT_KEY:\n  value: sk\n  host: api.example\n")
	tightFile(t, f.Paths.ProjectConfigDir())

	findings := ConfigFindings(f)

	require.Len(t, findings, 2)
	require.Contains(t, findings[0].Evidence.Summary, "host-config provisioning")
	require.Contains(t, findings[1].Evidence.Summary, "PROJECT_KEY")
}

// TestConfigFindingsReportsAGenericDangerousSecretWithoutWriting pins that a
// dangerous secret in the generic user scope is reported, not written: the
// applier's only secret target is the per-slug scope, so writing it would move a
// lower-precedence value up and could overwrite a per-slug entry.
func TestConfigFindingsReportsAGenericDangerousSecretWithoutWriting(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{Network: network.Policy{Profiles: []network.Profile{network.ProfileNone}}})
	writeSecretFile(t, f.Paths.UserConfigDir(),
		"WIDGET_TOKEN:\n  value: sk-generic\n  host: a.example\n  allow_any_host_dangerous: true\n")
	writeSecretFile(t, f.Paths.UserSlugConfigDir(testSlug),
		"WIDGET_TOKEN:\n  value: sk-perslug\n  host: b.example\n")
	tightFile(t, f.Paths.UserSlugConfigDir(testSlug))

	findings := ConfigFindings(f)

	require.Len(t, findings, 1)
	require.Contains(t, findings[0].Evidence.Summary, "WIDGET_TOKEN")
	require.Contains(t, findings[0].Evidence.Summary, "generic user scope")
	require.Empty(t, findings[0].Writes,
		"a generic-scope dangerous secret has no in-place narrowing target")
}

// TestConfigFindingsMovesEveryProjectSecret pins that each secret in the project
// scope yields its own C1 move, not only the first.
func TestConfigFindingsMovesEveryProjectSecret(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{Network: network.Policy{Profiles: []network.Profile{network.ProfileNone}}})
	writeSecretFile(t, f.Paths.ProjectConfigDir(),
		"ALPHA_KEY:\n  value: sk-a\n  host: api.example\n"+
			"BETA_KEY:\n  value: sk-b\n  host: api.example\n")
	tightFile(t, f.Paths.ProjectConfigDir())

	findings := ConfigFindings(f)

	for _, envVar := range []string{"ALPHA_KEY", "BETA_KEY"} {
		finding := findingWithSecretFor(t, findings, envVar)
		require.Len(t, finding.Writes, 2, "%s is removed and re-added", envVar)
	}
}

// TestConfigFindingsSecretFileModeSkipsATightFile pins that a secret file
// already at 0600 yields no C5 finding.
func TestConfigFindingsSecretFileModeSkipsATightFile(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{Network: network.Policy{Profiles: []network.Profile{network.ProfileNone}}})
	dir := f.Paths.UserSlugConfigDir(testSlug)
	writeSecretFile(t, dir, "OPENAI_API_KEY:\n  value: sk\n  host: api.example\n")
	tightFile(t, dir)

	require.Empty(t, ConfigFindings(f), "a 0600 secret file needs no tightening")
}

// TestConfigFindingsSecretFileModeSkipsANonRegularFile pins that the secret
// file's mode is only reportable for a regular file.
func TestConfigFindingsSecretFileModeSkipsANonRegularFile(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{Network: network.Policy{Profiles: []network.Profile{network.ProfileNone}}})
	dir := f.Paths.UserSlugConfigDir(testSlug)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, configpaths.EnvSecretFileName+".yaml"), 0o750))

	require.Empty(t, ConfigFindings(f), "a directory named like the secret file has no mode to tighten")
}

// TestConfigFindingsAllWildcardEgressAloneIsReported pins C4's foot-gun on its
// own: with no broad profile to narrow, an all-wildcard allow list is still
// reported rather than written empty.
func TestConfigFindingsAllWildcardEgressAloneIsReported(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{Network: network.Policy{
		Profiles:    []network.Profile{network.ProfileNone},
		EgressAllow: []string{"*"},
	}})

	findings := ConfigFindings(f)

	require.Len(t, findings, 1)
	require.Equal(t, KindConfig, findings[0].Kind)
	require.Contains(t, findings[0].Evidence.Summary, "only wildcard entries")
	require.Empty(t, findings[0].Writes)
}

// TestInlineConfigFindingsInScopeReturnsNothingWhenTheDirCannotBeRead pins that
// an absent agent config dir yields no findings.
func TestInlineConfigFindingsInScopeReturnsNothingWhenTheDirCannotBeRead(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{Network: network.Policy{Profiles: []network.Profile{network.ProfileNone}}})
	scope := f.Scopes[len(f.Scopes)-1]

	require.Empty(t, inlineConfigFindingsInScope(scope, filepath.Join(t.TempDir(), "missing"), nil))
}

// TestInlineConfigFindingsInScopeSkipsALoginStore pins that a login store is
// never read as a managed config, even when its content would classify, so a
// credential store's value is not moved.
func TestInlineConfigFindingsInScopeSkipsALoginStore(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{Network: network.Policy{Profiles: []network.Profile{network.ProfileNone}}})
	scope := f.Scopes[len(f.Scopes)-1]
	dir := filepath.Join(t.TempDir(), ".codex")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	testutil.WriteFile(t, dir, "auth.json",
		`{"provider":{"openai":{"options":{"apiKey":"sk-inline","baseURL":"https://api.openai.com/v1"}}}}`)

	findings := inlineConfigFindingsInScope(scope, dir, nil)

	require.Empty(t, findings, "a login store is never read as a managed config")
}

// TestInlineConfigFindingsInScopeProcessesEveryEntry pins that a directory, a
// skipped login store, and an unreadable file each pass by without stopping the
// scan, so a later valid file still yields its finding.
func TestInlineConfigFindingsInScopeProcessesEveryEntry(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{Network: network.Policy{Profiles: []network.Profile{network.ProfileNone}}})
	scope := f.Scopes[len(f.Scopes)-1]
	dir := t.TempDir()
	testutil.WriteFile(t, dir, ".env", "OPENAI_API_KEY=sk-dotenv\n")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "a-nested"), 0o750))
	require.NoError(t, os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, "b-unreadable")))
	testutil.WriteFile(t, dir, "c-opencode.jsonc",
		`{"provider":{"openai":{"options":{"apiKey":"sk-inline","baseURL":"https://api.openai.com/v1"}}}}`)

	findings := inlineConfigFindingsInScope(scope, dir, nil)

	require.Len(t, findings, 1)
	require.Contains(t, findings[0].Evidence.Summary, "OPENAI_API_KEY")
}

// TestInlineConfigFindingRequiresEachField pins that a credential missing any
// one of its env-var name, a host, or a literal value yields no finding.
func TestInlineConfigFindingRequiresEachField(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{Network: network.Policy{Profiles: []network.Profile{network.ProfileNone}}})
	scope := f.Scopes[len(f.Scopes)-1]
	complete := readback.Credential{
		EnvVar:   "OPENAI_API_KEY",
		Provider: "openai",
		Hosts:    []string{"api.openai.com"},
		Value:    "sk",
	}
	without := func(edit func(*readback.Credential)) readback.Credential {
		credential := complete
		edit(&credential)
		return credential
	}

	for _, tc := range []struct {
		name       string
		credential readback.Credential
		wantOK     bool
	}{
		{name: "complete", credential: complete, wantOK: true},
		{name: "no env var", credential: without(func(c *readback.Credential) { c.EnvVar = "" })},
		{name: "no host", credential: without(func(c *readback.Credential) { c.Hosts = nil })},
		{name: "no value", credential: without(func(c *readback.Credential) { c.Value = "" })},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := inlineConfigFinding(scope, tc.credential, nil)
			require.Equal(t, tc.wantOK, ok)
		})
	}
}

// TestOwnerScopePicksTheOwningLayer pins that a key the project layer owns maps
// to the project scope and every other key to the per-slug scope.
func TestOwnerScopePicksTheOwningLayer(t *testing.T) {
	f := configFacts(t, &viperconfig.Config{Network: network.Policy{Profiles: []network.Profile{network.ProfileNone}}})
	testutil.WriteFile(t, f.Paths.ProjectConfigDir(), "config.yaml", "memory: 8G\n")
	testutil.WriteFile(t, f.Paths.UserSlugConfigDir(testSlug), "config.yaml", "disk-size: 10G\n")

	require.Equal(t, ScopeProject, f.ownerScope("memory"), "the project layer owns memory")
	require.Equal(t, ScopePerSlugUser, f.ownerScope("disk-size"), "a lower layer owns disk-size")
	require.Equal(t, ScopePerSlugUser, f.ownerScope("no-such-key"))
}

// noProjectConfigPaths is a ConfigPaths whose project config dir does not
// resolve, as it does not for a bare command outside a checkout.
type noProjectConfigPaths struct{}

func (noProjectConfigPaths) UserConfigDir() string            { return "" }
func (noProjectConfigPaths) UserSlugConfigDir(string) string  { return "" }
func (noProjectConfigPaths) UserCacheDir() string             { return "" }
func (noProjectConfigPaths) UserStateDir() string             { return "" }
func (noProjectConfigPaths) UserDataDir() string              { return "" }
func (noProjectConfigPaths) UserHomeDir() (string, error)     { return "", nil }
func (noProjectConfigPaths) ProjectConfigDir() string         { return "" }
func (noProjectConfigPaths) ProjectConfigDirAbs() string      { return "" }
func (noProjectConfigPaths) ProjectDockerfile() string        { return "" }
func (noProjectConfigPaths) IgnoredWorktreeConfigDir() string { return "" }

// TestOwnerScopeWithoutAProjectConfigDirIsPerSlug pins that an unowned key maps
// to the per-slug scope even when the project config dir does not resolve, so
// the ownership check is what decides, not a degenerate empty-path comparison.
func TestOwnerScopeWithoutAProjectConfigDirIsPerSlug(t *testing.T) {
	f := ConfigFacts{Paths: noProjectConfigPaths{}, Slug: testSlug}

	require.Equal(t, ScopePerSlugUser, f.ownerScope("no-such-key"))
}

// TestScopeWriteScopeMapsProjectAndOthers pins the write-scope mapping: only the
// project layer maps to the project write scope.
func TestScopeWriteScopeMapsProjectAndOthers(t *testing.T) {
	require.Equal(t, ScopeProject, scopeWriteScope(configpaths.ScopeProject))
	require.Equal(t, ScopePerSlugUser, scopeWriteScope(configpaths.ScopePerSlugUser))
	require.Equal(t, ScopePerSlugUser, scopeWriteScope(configpaths.ScopeGenericUser))
	require.Equal(t, ScopePerSlugUser, scopeWriteScope(configpaths.ScopeKind(99)))
}

// TestSortedSpecKeysReturnsAStableSortedOrder pins that a secret-spec map's keys
// are returned sorted, so findings do not depend on map iteration order.
func TestSortedSpecKeysReturnsAStableSortedOrder(t *testing.T) {
	specs := make(map[string]envsecret.SecretSpec)
	for _, key := range []string{"ECHO", "DELTA", "CHARLIE", "BRAVO", "ALPHA"} {
		specs[key] = envsecret.SecretSpec{Value: "sk"}
	}

	require.Equal(t, []string{"ALPHA", "BRAVO", "CHARLIE", "DELTA", "ECHO"}, sortedSpecKeys(specs))
}

// TestProfileListJoinsEveryProfile pins that every profile in a policy is
// rendered, not just the first.
func TestProfileListJoinsEveryProfile(t *testing.T) {
	require.Equal(t, "public, host", profileList([]network.Profile{network.ProfilePublic, network.ProfileHost}))
}

// TestSortFindingsOrdersByItsKey pins that sortFindings reorders by the key, not
// by input order, and leaves an already-ordered input unchanged.
func TestSortFindingsOrdersByItsKey(t *testing.T) {
	early := Finding{Kind: KindConfig, Evidence: Evidence{Summary: "a"}}
	middle := Finding{Kind: KindConfig, Evidence: Evidence{Summary: "b"}}
	late := Finding{Kind: KindConfig, Evidence: Evidence{Summary: "c"}}

	got := []Finding{late, middle, early}
	sortFindings(got)
	require.Equal(t, []Finding{early, middle, late}, got)

	already := []Finding{early, middle, late}
	sortFindings(already)
	require.Equal(t, []Finding{early, middle, late}, already)
}

// TestSortFindingsDiscriminatesEveryKeyComponent pins that each component of the
// ordering key takes part in the order: two findings that differ only in that
// component are still ordered by it rather than left in input order.
func TestSortFindingsDiscriminatesEveryKeyComponent(t *testing.T) {
	base := Finding{Kind: KindConfig, Evidence: Evidence{Summary: "summary"}}
	withKind := func(k Kind) Finding { finding := base; finding.Kind = k; return finding }
	withSummary := func(s string) Finding { finding := base; finding.Evidence.Summary = s; return finding }
	withWrite := func(w Write) Finding { finding := base; finding.Writes = []Write{w}; return finding }
	withWrites := func(writes []Write) Finding { finding := base; finding.Writes = writes; return finding }

	perSlug := Write{Scope: ScopePerSlugUser}
	secret := func(envVar string) Write {
		return Write{Scope: ScopePerSlugUser, Secret: &SecretWrite{EnvVar: envVar}}
	}

	for _, tc := range []struct {
		name    string
		low     Finding
		high    Finding
		lowKey  string
		highKey string
		probe   func(Finding) string
	}{
		{
			name: "kind", low: withKind(KindConfig), high: withKind(KindObservation),
			lowKey: "config", highKey: "observation",
			probe: func(f Finding) string { return string(f.Kind) },
		},
		{
			name: "summary", low: withSummary("a"), high: withSummary("b"),
			lowKey: "a", highKey: "b",
			probe: func(f Finding) string { return f.Evidence.Summary },
		},
		{
			name: "write scope", low: withWrite(perSlug), high: withWrite(Write{Scope: ScopeProject}),
			lowKey: string(ScopePerSlugUser), highKey: string(ScopeProject),
			probe: func(f Finding) string { return string(f.Writes[0].Scope) },
		},
		{
			name:   "config key",
			low:    withWrite(Write{Scope: ScopePerSlugUser, ConfigKey: "a"}),
			high:   withWrite(Write{Scope: ScopePerSlugUser, ConfigKey: "b"}),
			lowKey: "a", highKey: "b",
			probe: func(f Finding) string { return f.Writes[0].ConfigKey },
		},
		{
			name: "secret env var", low: withWrite(secret("A")), high: withWrite(secret("B")),
			lowKey: "A", highKey: "B",
			probe: func(f Finding) string { return f.Writes[0].Secret.EnvVar },
		},
		{
			name:   "file mode",
			low:    withWrite(perSlug),
			high:   withWrite(Write{Scope: ScopePerSlugUser, FileMode: &FileModeWrite{Mode: 0o600}}),
			lowKey: "plain", highKey: "file-mode",
			probe: func(f Finding) string {
				if f.Writes[0].FileMode != nil {
					return "file-mode"
				}
				return "plain"
			},
		},
		{
			name:   "second write",
			low:    withWrites([]Write{perSlug, {Scope: ScopePerSlugUser, ConfigKey: "a"}}),
			high:   withWrites([]Write{perSlug, {Scope: ScopePerSlugUser, ConfigKey: "b"}}),
			lowKey: "a", highKey: "b",
			probe: func(f Finding) string { return f.Writes[1].ConfigKey },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := []Finding{tc.high, tc.low}
			sortFindings(got)
			require.Equal(t, tc.lowKey, tc.probe(got[0]), "the lower-keyed finding sorts first")
			require.Equal(t, tc.highKey, tc.probe(got[1]))
		})
	}
}
