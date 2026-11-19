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
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
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

	require.NotEmpty(t, findings, "a broad profile with no allow list is reported")
	for _, finding := range findings {
		for _, write := range finding.Writes {
			require.NotEqual(t, "network.profile", write.ConfigKey,
				"narrowing to none with no allow entries would deny all egress")
		}
	}
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

	require.NotEmpty(t, findings, "an all-wildcard allow list is reported")
	for _, finding := range findings {
		for _, write := range finding.Writes {
			require.NotEqual(t, "network.profile", write.ConfigKey,
				"narrowing to none while dropping the last wildcards would deny all egress")
			require.NotEqual(t, "network.egress-allow", write.ConfigKey,
				"dropping the last wildcards would deny all egress")
		}
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
