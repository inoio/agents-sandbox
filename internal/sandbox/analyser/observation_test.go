package analyser

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/channel"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
)

// narrowNone is a current policy that is already as narrow as a proposal can
// get: the terminal `none` profile with no extra allow entries. Tests use it to
// silence the network diff and exercise the secret findings alone.
func narrowNone() network.Policy {
	return network.Policy{Profiles: []network.Profile{network.ProfileNone}}
}

// egressAllowWrite returns the network.egress-allow write of a finding, failing
// when the finding proposes none.
func egressAllowWrite(t *testing.T, finding Finding) Write {
	t.Helper()
	for _, write := range finding.Writes {
		if write.ConfigKey == "network.egress-allow" {
			return write
		}
	}
	t.Fatalf("finding proposes no network.egress-allow write: %+v", finding)
	return Write{}
}

// TestObservationNetworkDiffNeverWidens: the proposed allow set is the current
// allow set plus only the destinations the session used, so a user-set entry is
// never removed and never dropped in favor of a broader profile.
func TestObservationNetworkDiffNeverWidens(t *testing.T) {
	s := SessionFacts{
		Current: network.Policy{
			Profiles:    []network.Profile{network.ProfileNone},
			EgressAllow: []string{"already.example"},
		},
		Observation: channel.Observation{Destinations: []channel.Destination{
			{Host: "api.example", Success: true, Count: 2},
		}},
	}

	findings := ObservationFindings(s)

	require.Len(t, findings, 1)
	require.Equal(t, KindObservation, findings[0].Kind)
	require.Equal(t, evidenceChannelProxy, findings[0].Evidence.Channel)
	require.Equal(t, []string{"api.example"}, findings[0].Evidence.Destinations)

	allow := egressAllowWrite(t, findings[0])
	require.Equal(t, ScopePerSlugUser, allow.Scope)
	require.False(t, allow.Remove, "the proposal never removes a user-set entry")
	require.Equal(t, []string{"already.example", "api.example"}, allow.ConfigValue)

	profile := configWrite(t, findings[0], "network.profile")
	require.Equal(t, ScopePerSlugUser, profile.Scope)
	require.Equal(t, network.ProfileNone.String(), profile.ConfigValue)
}

// TestObservationNetworkDiffIsSilentWhenAlreadyNarrow: a session whose used
// destinations the current `none` policy already allows yields no finding —
// there is nothing to change.
func TestObservationNetworkDiffIsSilentWhenAlreadyNarrow(t *testing.T) {
	s := SessionFacts{
		Current: network.Policy{
			Profiles:    []network.Profile{network.ProfileNone},
			EgressAllow: []string{"api.example"},
		},
		Observation: channel.Observation{Destinations: []channel.Destination{
			{Host: "api.example", Success: true, Count: 3},
		}},
	}

	require.Empty(t, ObservationFindings(s))
}

// TestObservationDeniedIsReportedNotAllowed: a denied destination is evidence,
// never an allow entry.
func TestObservationDeniedIsReportedNotAllowed(t *testing.T) {
	s := SessionFacts{
		Observation: channel.Observation{Destinations: []channel.Destination{
			{Host: "blocked.example", Denied: true},
		}},
	}

	findings := ObservationFindings(s)

	require.NotEmpty(t, findings)
	require.Contains(t, findings[0].Evidence.Denied, "blocked.example")
	for _, finding := range findings {
		for _, write := range finding.Writes {
			if write.ConfigKey != "network.egress-allow" {
				continue
			}
			allowed, ok := write.ConfigValue.([]string)
			require.True(t, ok, "an egress-allow value is a host list")
			require.NotContains(t, allowed, "blocked.example",
				"a denied destination must never be auto-allowed")
		}
	}
}

// TestObservationSecretUsesChannelAttributionThenConfigEntryHosts: the host set
// for a learned static secret comes from the destinations the channel observed
// the credential sent to, and falls back to the endpoint the read-back found in
// the credential's config entry.
func TestObservationSecretUsesChannelAttributionThenConfigEntryHosts(t *testing.T) {
	credential := readback.Credential{
		Kind:     readback.CredentialStatic,
		EnvVar:   "OPENAI_API_KEY",
		Provider: "openai",
		Field:    "authorization",
		Hosts:    []string{"config.example"},
	}

	attributed := SessionFacts{
		Current:  narrowNone(),
		ReadBack: readback.ReadBack{Static: []readback.Credential{credential}},
		Observation: channel.Observation{Attribution: []channel.Attribution{
			{Host: "attributed.example", Credential: "OPENAI_API_KEY", HeaderNames: []string{"authorization"}},
		}},
	}
	findings := ObservationFindings(attributed)
	require.Len(t, findings, 1)
	secret := findings[0].Writes[0].Secret
	require.NotNil(t, secret)
	require.Equal(t, []string{"attributed.example"}, secret.Hosts,
		"the channel attribution supersedes the config entry")

	configEntry := attributed
	configEntry.Observation = channel.Observation{}
	findings = ObservationFindings(configEntry)
	require.Len(t, findings, 1)
	require.Equal(t, []string{"config.example"}, findings[0].Writes[0].Secret.Hosts,
		"with no channel attribution the config-entry host is used")
}

// TestObservationSecretWriteShape: the secret write names the env var, the
// provider, the scoped hosts, and the header-field hint, and never the value.
func TestObservationSecretWriteShape(t *testing.T) {
	s := SessionFacts{
		Current: narrowNone(),
		ReadBack: readback.ReadBack{Static: []readback.Credential{{
			Kind:     readback.CredentialStatic,
			EnvVar:   "ANTHROPIC_API_KEY",
			Provider: "anthropic",
			Field:    "x-api-key",
			Hosts:    []string{"api.anthropic.example"},
			Value:    "sk-never-persisted",
		}}},
	}

	findings := ObservationFindings(s)

	require.Len(t, findings, 1)
	finding := findings[0]
	require.Equal(t, evidenceChannelReadback, finding.Evidence.Channel)
	require.Equal(t, []string{"x-api-key"}, finding.Evidence.HeaderFields,
		"the evidence carries header NAMES only")
	write := finding.Writes[0]
	require.Equal(t, ScopePerSlugUser, write.Scope)
	require.False(t, write.Remove)
	require.Equal(t, "ANTHROPIC_API_KEY", write.Secret.EnvVar)
	require.Equal(t, "anthropic", write.Secret.Provider)
	require.Equal(t, []string{"api.anthropic.example"}, write.Secret.Hosts)
	require.Equal(t, []string{"x-api-key"}, write.Secret.HeaderFields)
}

// TestObservationSecretWithoutHostsYieldsNoFinding: a static secret with no
// derivable host is not migrated — it stays in the sandbox home.
func TestObservationSecretWithoutHostsYieldsNoFinding(t *testing.T) {
	s := SessionFacts{
		Current: narrowNone(),
		ReadBack: readback.ReadBack{Static: []readback.Credential{{
			Kind:     readback.CredentialStatic,
			EnvVar:   "OPENAI_API_KEY",
			Provider: "openai",
			Field:    "authorization",
		}}},
	}

	require.Empty(t, ObservationFindings(s))
}

// TestObservationSecretWithoutEnvVarYieldsNoFinding: a static credential whose
// env-var name the read-back did not recover cannot be scoped, so no finding is
// emitted for it.
func TestObservationSecretWithoutEnvVarYieldsNoFinding(t *testing.T) {
	s := SessionFacts{
		Current: narrowNone(),
		ReadBack: readback.ReadBack{Static: []readback.Credential{{
			Kind:     readback.CredentialStatic,
			Provider: "openai",
			Hosts:    []string{"api.example"},
		}}},
	}

	require.Empty(t, ObservationFindings(s))
}

// TestObservationFindingsStampEvidenceHash: every finding carries the digest of
// its own evidence, so the decline memory keys on real evidence.
func TestObservationFindingsStampEvidenceHash(t *testing.T) {
	s := SessionFacts{
		Current: network.Policy{Profiles: []network.Profile{network.ProfileNone}},
		ReadBack: readback.ReadBack{Static: []readback.Credential{{
			Kind:     readback.CredentialStatic,
			EnvVar:   "OPENAI_API_KEY",
			Provider: "openai",
			Field:    "authorization",
			Hosts:    []string{"api.example"},
		}}},
		Observation: channel.Observation{Destinations: []channel.Destination{
			{Host: "api.example", Success: true, Count: 1},
		}},
	}

	findings := ObservationFindings(s)

	require.NotEmpty(t, findings)
	for _, finding := range findings {
		require.NotEmpty(t, finding.EvidenceHash)
		require.Equal(t, finding.Evidence.hash(), finding.EvidenceHash)
	}
}

// TestObservationProjectOwnedKeyIsStillEmitted: the observation finder emits a
// per-slug write even when the project config already sets the key. The applier
// is the single place that reports-and-skips a shadowed key, so the engine here
// never suppresses or retargets the finding.
func TestObservationProjectOwnedKeyIsStillEmitted(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	projectConfig := filepath.Join(paths.ProjectConfigDir(), "config.yaml")
	require.NoError(t, os.WriteFile(projectConfig,
		[]byte("network:\n  egress-allow:\n    - project.example\n"), 0o600))

	s := SessionFacts{
		Paths:   paths,
		Slug:    "widget-abc",
		Current: narrowNone(),
		Observation: channel.Observation{Destinations: []channel.Destination{
			{Host: "api.example", Success: true, Count: 1},
		}},
	}

	findings := ObservationFindings(s)

	require.Len(t, findings, 1)
	allow := egressAllowWrite(t, findings[0])
	require.Equal(t, ScopePerSlugUser, allow.Scope,
		"the engine emits the per-slug write; the applier skips a project-shadowed key")
	require.Equal(t, []string{"api.example"}, allow.ConfigValue)
	for _, write := range findings[0].Writes {
		require.NotEqual(t, ScopeProject, write.Scope)
	}
}

// configWrite returns the write proposing key, failing when the finding has none.
func configWrite(t *testing.T, finding Finding, key string) Write {
	t.Helper()
	for _, write := range finding.Writes {
		if write.ConfigKey == key {
			return write
		}
	}
	t.Fatalf("finding proposes no %s write: %+v", key, finding)
	return Write{}
}
