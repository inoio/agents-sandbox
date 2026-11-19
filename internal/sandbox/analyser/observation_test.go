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
	require.Contains(t, allow.Text, "creation-only",
		"the proposal states the network policy takes effect at the next VM creation")

	profile := configWrite(t, findings[0], "network.profile")
	require.Equal(t, ScopePerSlugUser, profile.Scope)
	require.Equal(t, network.ProfileNone.String(), profile.ConfigValue)
	require.Contains(t, profile.Text, "clone",
		"the proposal states the per-slug scope is shared by every clone of the remote")
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

// TestObservationNetworkFindingIsSilentWhenAlreadyNarrowEvenWithDenials: a
// session whose used destinations the current `none` policy already allows
// yields no network finding — the policy is already as narrow as the proposal
// can make it, so a finding whose summary promised to lock egress would be a
// misleading no-op. A denied destination is still not a reason to emit one.
func TestObservationNetworkFindingIsSilentWhenAlreadyNarrowEvenWithDenials(t *testing.T) {
	s := SessionFacts{
		Current: network.Policy{
			Profiles:    []network.Profile{network.ProfileNone},
			EgressAllow: []string{"api.example"},
		},
		Observation: channel.Observation{Destinations: []channel.Destination{
			{Host: "api.example", Success: true, Count: 3},
			{Host: "blocked.example", Denied: true},
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

// TestObservationDeniedHostConnectTunnelIsNotUse: the channel records an
// accepted CONNECT tunnel as its own successful destination, separately from the
// request that travelled through it. The tunnel's acceptance is not evidence the
// host is safe to allow, so a host whose only request was denied never enters the
// proposed allow set even though its connect entry succeeded.
func TestObservationDeniedHostConnectTunnelIsNotUse(t *testing.T) {
	deniedHostConnect := SessionFacts{
		Current: network.Policy{Profiles: []network.Profile{network.ProfilePublic}},
		Observation: channel.Observation{Destinations: []channel.Destination{
			{Host: "dead.example", Protocol: connectProtocol, Success: true},
			{Host: "dead.example", Protocol: "https", Path: "/", Success: false, Denied: true},
		}},
	}

	findings := ObservationFindings(deniedHostConnect)

	require.Len(t, findings, 1)
	require.Contains(t, findings[0].Evidence.Denied, "dead.example")
	allow := egressAllowWrite(t, findings[0])
	allowed, ok := allow.ConfigValue.([]string)
	require.True(t, ok, "an egress-allow value is a host list")
	require.NotContains(t, allowed, "dead.example",
		"a host whose only request was denied must never be auto-allowed via its connect tunnel")

	t.Run("a connect tunnel with no denial is real use", func(t *testing.T) {
		connectOnly := SessionFacts{
			Current: network.Policy{Profiles: []network.Profile{network.ProfilePublic}},
			Observation: channel.Observation{Destinations: []channel.Destination{
				{Host: "live.example", Protocol: connectProtocol, Success: true},
			}},
		}

		findings := ObservationFindings(connectOnly)

		require.Len(t, findings, 1)
		allowed, ok := egressAllowWrite(t, findings[0]).ConfigValue.([]string)
		require.True(t, ok, "an egress-allow value is a host list")
		require.Contains(t, allowed, "live.example",
			"a successful non-intercepted tunnel is real use")
	})

	t.Run("a successful request still allows a host that also has a denial", func(t *testing.T) {
		mixed := SessionFacts{
			Current: network.Policy{Profiles: []network.Profile{network.ProfilePublic}},
			Observation: channel.Observation{Destinations: []channel.Destination{
				{Host: "mixed.example", Protocol: "https", Path: "/", Success: true},
				{Host: "mixed.example", Protocol: "https", Path: "/admin", Success: false, Denied: true},
			}},
		}

		findings := ObservationFindings(mixed)

		require.Len(t, findings, 1)
		allowed, ok := egressAllowWrite(t, findings[0]).ConfigValue.([]string)
		require.True(t, ok, "an egress-allow value is a host list")
		require.Contains(t, allowed, "mixed.example",
			"a successful request is real use even when another request to the host was denied")
	})
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
			{Host: "attributed.example", Credential: "OPENAI_API_KEY", HeaderNames: []string{"x-goog-api-key"}},
		}},
	}
	findings := ObservationFindings(attributed)
	require.Len(t, findings, 1)
	secret := findings[0].Writes[0].Secret
	require.NotNil(t, secret)
	require.Equal(t, []string{"attributed.example"}, secret.Hosts,
		"the channel attribution supersedes the config entry")
	require.Equal(t, []string{"x-goog-api-key"}, secret.HeaderFields,
		"the write scopes substitution to the attributed header name, not the credential's hint")
	require.True(t, findings[0].Evidence.Succeeded,
		"a channel attribution is a positive observation")

	configEntry := attributed
	configEntry.Observation = channel.Observation{}
	findings = ObservationFindings(configEntry)
	require.Len(t, findings, 1)
	require.Equal(t, []string{"config.example"}, findings[0].Writes[0].Secret.Hosts,
		"with no channel attribution the config-entry host is used")
	require.Equal(t, []string{"authorization"}, findings[0].Writes[0].Secret.HeaderFields,
		"with no channel attribution the credential's header-field hint is used")
	require.False(t, findings[0].Evidence.Succeeded,
		"the config-entry fallback is not an observed use")
}

// TestObservationAttributionSkipsUnmatchedEntries: an attribution entry for
// another credential, or one with no host, is not a host source; only a matching
// entry with a host contributes.
func TestObservationAttributionSkipsUnmatchedEntries(t *testing.T) {
	s := SessionFacts{
		Current: narrowNone(),
		ReadBack: readback.ReadBack{Static: []readback.Credential{{
			Kind:     readback.CredentialStatic,
			EnvVar:   "OPENAI_API_KEY",
			Provider: "openai",
			Hosts:    []string{"config.example"},
		}}},
		Observation: channel.Observation{Attribution: []channel.Attribution{
			{Credential: "OTHER_TOKEN", Host: "other.example"},
			{Credential: "OPENAI_API_KEY", Host: ""},
			{Credential: "OPENAI_API_KEY", Host: "attributed.example"},
		}},
	}

	findings := ObservationFindings(s)

	require.Len(t, findings, 1)
	require.Equal(t, []string{"attributed.example"}, findings[0].Writes[0].Secret.Hosts)
}

// TestObservationSecretWithoutAHeaderHintHasNoHeaderFields pins that a
// credential with no channel attribution and no header-field hint scopes the
// secret to no specific header.
func TestObservationSecretWithoutAHeaderHintHasNoHeaderFields(t *testing.T) {
	s := SessionFacts{
		Current: narrowNone(),
		ReadBack: readback.ReadBack{Static: []readback.Credential{{
			Kind:     readback.CredentialStatic,
			EnvVar:   "CUSTOM_TOKEN",
			Provider: "custom",
			Hosts:    []string{"api.example"},
		}}},
	}

	findings := ObservationFindings(s)

	require.Len(t, findings, 1)
	require.Empty(t, findings[0].Writes[0].Secret.HeaderFields)
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
	require.Contains(t, write.Text, "clone",
		"the proposal states the per-slug scope is shared by every clone of the remote")
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

// TestObservationSecretFindingsMergeSameEnvVar: two static credentials that
// share an env-var name yield exactly one finding. Two writes for the same
// literal key would make the second replace the first's entry and silently drop
// its host set, so the finding merges every source's hosts and header names.
func TestObservationSecretFindingsMergeSameEnvVar(t *testing.T) {
	s := SessionFacts{
		Current: narrowNone(),
		ReadBack: readback.ReadBack{Static: []readback.Credential{
			{
				Kind:     readback.CredentialStatic,
				EnvVar:   "OPENAI_API_KEY",
				Provider: "openai",
				Field:    "authorization",
				Hosts:    []string{"config.example"},
				Value:    "sk-dotenv",
			},
			{
				Kind:     readback.CredentialStatic,
				EnvVar:   "OPENAI_API_KEY",
				Provider: "openai",
				Field:    "x-api-key",
				Hosts:    []string{"inline.example"},
				Value:    "sk-inline",
			},
		}},
	}

	findings := ObservationFindings(s)

	require.Len(t, findings, 1, "one finding per env-var name")
	require.Equal(t, []string{"config.example", "inline.example"},
		findings[0].Writes[0].Secret.Hosts, "the host sets are merged")
	require.Equal(t, []string{"authorization", "x-api-key"},
		findings[0].Writes[0].Secret.HeaderFields, "the header-field sets are merged")
	require.Equal(t, []string{"config.example", "inline.example"},
		findings[0].Evidence.Destinations, "the evidence reflects the merged host set")
	require.Equal(t, []string{"authorization", "x-api-key"},
		findings[0].Evidence.HeaderFields, "the evidence reflects the merged header-field set")
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

// TestObservationEmptySessionYieldsNoFinding pins that a session with no
// observed destination and no credential is silent: the network finding needs
// something to act on, so nothing is proposed.
func TestObservationEmptySessionYieldsNoFinding(t *testing.T) {
	require.Empty(t, ObservationFindings(SessionFacts{}))
}

// TestObservationEvidenceSucceededReflectsUsedHosts pins that the evidence's
// Succeeded is true exactly when the session used at least one host, so a
// finding built only from a denial reports that it did not succeed.
func TestObservationEvidenceSucceededReflectsUsedHosts(t *testing.T) {
	used := SessionFacts{
		Current: network.Policy{Profiles: []network.Profile{network.ProfilePublic}},
		Observation: channel.Observation{Destinations: []channel.Destination{
			{Host: "api.example", Success: true, Count: 1},
		}},
	}
	findings := ObservationFindings(used)
	require.Len(t, findings, 1)
	require.True(t, findings[0].Evidence.Succeeded, "a used host means the session succeeded")

	deniedOnly := SessionFacts{
		Current: network.Policy{Profiles: []network.Profile{network.ProfilePublic}},
		Observation: channel.Observation{Destinations: []channel.Destination{
			{Host: "blocked.example", Denied: true},
		}},
	}
	findings = ObservationFindings(deniedOnly)
	require.Len(t, findings, 1)
	require.False(t, findings[0].Evidence.Succeeded, "no used host means it did not succeed")
}

// TestObservedUsedHostsFiltersNonUseAndKeepsLaterDestinations pins which
// destinations count as use: a hostless one, a failed one, and a denied one are
// excluded, a later successful destination is still reached, and a denied host
// is never allowed by its connect tunnel.
func TestObservedUsedHostsFiltersNonUseAndKeepsLaterDestinations(t *testing.T) {
	hosts := observedUsedHosts([]channel.Destination{
		{Host: "", Success: true},                             // hostless: not a host
		{Host: "failed.example", Success: false},              // a failed request
		{Host: "denied.example", Denied: true, Success: true}, // a denial outranks a success
		{Host: "used.example", Protocol: "https", Success: true, Count: 2},
	})
	require.Equal(t, []string{"used.example"}, hosts)

	connectThenUse := observedUsedHosts([]channel.Destination{
		{Host: "dead.example", Protocol: connectProtocol, Success: true},
		{Host: "dead.example", Protocol: "https", Success: false, Denied: true},
		{Host: "live.example", Success: true},
	})
	require.Equal(t, []string{"live.example"}, connectThenUse,
		"a denied host's connect tunnel is not use and must not stop the loop")
}

// TestDeniedHostsExcludeHostlessAndKeepLaterDenials pins that a hostless denied
// destination contributes no host, and a later denied host is still recorded.
func TestDeniedHostsExcludeHostlessAndKeepLaterDenials(t *testing.T) {
	hosts := deniedHosts([]channel.Destination{
		{Host: "", Denied: true},
		{Host: "b.example", Denied: true},
		{Host: "a.example", Denied: true},
		{Host: "used.example", Success: true},
	})
	require.Equal(t, []string{"a.example", "b.example"}, hosts)
}

// TestUsedRequestCountSumsOnlyUsedDestinations pins that the count adds up the
// request counts of exactly the used destinations: a failed one, a denied one,
// and a denied-but-successful one contribute nothing, and skipping one does not
// stop the scan.
func TestUsedRequestCountSumsOnlyUsedDestinations(t *testing.T) {
	total := usedRequestCount([]channel.Destination{
		{Host: "failed.example", Success: false, Count: 9},
		{Host: "denied.example", Denied: true, Success: true, Count: 7},
		{Host: "used.example", Success: true, Count: 2},
		{Host: "used2.example", Success: true, Count: 3},
	})
	require.Equal(t, 5, total)
}

// TestSecretFindingsSkipUnscopableCredentialsWithoutStopping pins that a
// credential the read-back could not name and one whose host set could not be
// derived are each skipped without stopping the loop, so a later scoped
// credential still yields a finding.
func TestSecretFindingsSkipUnscopableCredentialsWithoutStopping(t *testing.T) {
	s := SessionFacts{
		Current: narrowNone(),
		ReadBack: readback.ReadBack{Static: []readback.Credential{
			{Provider: "openai", Hosts: []string{"skip.example"}},                      // no EnvVar
			{Kind: readback.CredentialStatic, EnvVar: "HOSTLESS_TOKEN", Provider: "x"}, // no host
			{
				Kind:     readback.CredentialStatic,
				EnvVar:   "OPENAI_API_KEY",
				Provider: "openai",
				Hosts:    []string{"api.example"},
			},
		}},
	}

	findings := ObservationFindings(s)

	require.Len(t, findings, 1)
	require.Equal(t, "OPENAI_API_KEY", findings[0].Writes[0].Secret.EnvVar)
}

// TestSortedUniqueSortsDedupesAndReportsEmptyAsNil pins that sortedUnique returns
// a sorted, de-duplicated copy, and reports an empty input as nil.
func TestSortedUniqueSortsDedupesAndReportsEmptyAsNil(t *testing.T) {
	require.Nil(t, sortedUnique(nil))
	require.Nil(t, sortedUnique([]string{}))
	require.Equal(
		t,
		[]string{"a.example", "b.example"},
		sortedUnique([]string{"b.example", "a.example", "b.example"}),
	)
}
