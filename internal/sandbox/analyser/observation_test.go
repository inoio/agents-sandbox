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

// TestObservationNetworkLockTurnsInterceptionOn: under the runtime's strict
// hostname mode a hostname allow rule admits only an intercepted connection, so
// a `none` lock with hostname entries and interception left at auto (off
// without a secret) would block every destination it allows. The lock proposes
// interception on with it.
func TestObservationNetworkLockTurnsInterceptionOn(t *testing.T) {
	s := SessionFacts{
		Observation: channel.Observation{Destinations: []channel.Destination{
			{Host: "api.example", Success: true, Count: 2},
		}},
	}

	findings := ObservationFindings(s)

	require.Len(t, findings, 1)
	intercept := configWrite(t, findings[0], "network.intercept-tls")
	require.Equal(t, ScopePerSlugUser, intercept.Scope)
	require.Equal(t, string(network.InterceptOn), intercept.ConfigValue)
	require.Contains(t, intercept.Text, "interception")
}

// TestObservationNetworkLockLeavesOutAddressOnlyTunnels: the channel names
// every tunnel that carried a TLS handshake by its SNI hostname, so a tunnel
// still recorded under an IP never carried a request (an abandoned
// happy-eyeballs attempt, a pre-warmed connection). An address is no evidence of
// use, and an allow entry for it would break when the address rotates.
func TestObservationNetworkLockLeavesOutAddressOnlyTunnels(t *testing.T) {
	s := SessionFacts{
		Observation: channel.Observation{Destinations: []channel.Destination{
			{Host: "160.79.104.10", IP: "160.79.104.10", Protocol: "connect", Success: true, Count: 1},
			{Host: "2607:6bc0::10", IP: "2607:6bc0::10", Protocol: "connect", Success: true, Count: 1},
			{Host: "api.example", IP: "160.79.104.10", Protocol: "connect", Success: true, Count: 2},
			{Host: "api.example", Protocol: "https", Success: true, Count: 2},
		}},
	}

	findings := ObservationFindings(s)

	require.Len(t, findings, 1)
	require.Equal(t, []string{"api.example"}, findings[0].Evidence.Destinations)
	require.Equal(t, []string{"api.example"}, egressAllowWrite(t, findings[0]).ConfigValue)
}

// TestObservationNetworkLockKeepsInterceptionWhenItAlreadyWorks: interception
// already on, or strict mode off, lets hostname rules work, so the lock writes
// no interception setting.
func TestObservationNetworkLockKeepsInterceptionWhenItAlreadyWorks(t *testing.T) {
	for name, current := range map[string]network.Policy{
		"interception on": {InterceptTLS: network.InterceptOn},
		"strict mode off": {DisableStrict: true},
	} {
		t.Run(name, func(t *testing.T) {
			s := SessionFacts{
				Current: current,
				Observation: channel.Observation{Destinations: []channel.Destination{
					{Host: "api.example", Success: true, Count: 2},
				}},
			}

			findings := ObservationFindings(s)

			require.Len(t, findings, 1)
			for _, write := range findings[0].Writes {
				require.NotEqual(t, "network.intercept-tls", write.ConfigKey)
			}
		})
	}
}

// TestObservationNarrowPolicyWithoutInterceptionGetsAnInterceptionFinding: a
// hand-written none policy with hostname entries and interception at auto (off
// without a secret) blocks every host it allows outside learn mode, where the
// overlay no longer forces interception. The session used exactly the allowed
// hosts, so no lock is proposed, but an interception-only finding is. Its
// evidence carries no per-session count, so a decline (a user whose secret
// already turns interception on) sticks.
func TestObservationNarrowPolicyWithoutInterceptionGetsAnInterceptionFinding(t *testing.T) {
	facts := func(count int) SessionFacts {
		return SessionFacts{
			Current: network.Policy{
				Profiles:    []network.Profile{network.ProfileNone},
				EgressAllow: []string{"api.example"},
			},
			Observation: channel.Observation{Destinations: []channel.Destination{
				{Host: "api.example", Success: true, Count: count},
			}},
		}
	}

	findings := ObservationFindings(facts(3))

	require.Len(t, findings, 1)
	require.Len(t, findings[0].Writes, 1)
	require.Equal(t, "network.intercept-tls", findings[0].Writes[0].ConfigKey)
	require.Equal(t, findings[0].EvidenceHash, ObservationFindings(facts(7))[0].EvidenceHash,
		"the interception finding's evidence must not change with the session's request count")
}

// TestObservationNetworkLockIsSilentOnANarrowInterceptedPolicy: a policy
// already narrowed to the used hosts with interception on needs nothing.
func TestObservationNetworkLockIsSilentOnANarrowInterceptedPolicy(t *testing.T) {
	s := SessionFacts{
		Current: network.Policy{
			Profiles:     []network.Profile{network.ProfileNone},
			EgressAllow:  []string{"api.example"},
			InterceptTLS: network.InterceptOn,
		},
		Observation: channel.Observation{Destinations: []channel.Destination{
			{Host: "api.example", Success: true, Count: 3},
		}},
	}

	require.Empty(t, ObservationFindings(s))
}

// TestObservationNetworkLockNeedsAUsedDestination: a session that used nothing
// (only denials, or only address-only tunnels) would lock egress to the current
// allow set alone, possibly nothing at all, so it yields no lock. The denials
// are reported by the after-run summary.
func TestObservationNetworkLockNeedsAUsedDestination(t *testing.T) {
	s := SessionFacts{
		Observation: channel.Observation{Destinations: []channel.Destination{
			{Host: "203.0.113.7", IP: "203.0.113.7", Protocol: channel.ConnectProtocol, Success: true, Count: 1},
			{Host: "blocked.example", Denied: true},
		}},
	}

	require.Empty(t, ObservationFindings(s))
}

func TestBypassedHostsRequiresAWildcardLabelAndIgnoresCase(t *testing.T) {
	got := bypassedHosts(
		[]string{"example.com", "badexample.com", "a.example.com", "API.Pinned.Example", "exact.example"},
		[]string{"*.example.com", "*example.com", "*.pinned.example", "EXACT.example"},
	)
	require.Equal(t, []string{"a.example.com", "API.Pinned.Example", "exact.example"}, got)
}

// TestObservationNetworkLockNamesBypassedHosts: under the strict-hostname mode a
// host that is both allowlisted and in tls-bypass stays denied, so the lock says
// which used hosts that applies to instead of promising they work.
func TestObservationNetworkLockNamesBypassedHosts(t *testing.T) {
	s := SessionFacts{
		Current: network.Policy{TLSBypass: []string{"*.pinned.example"}},
		Observation: channel.Observation{Destinations: []channel.Destination{
			{Host: "api.pinned.example", Success: true, Count: 1},
			{Host: "api.example", Success: true, Count: 1},
		}},
	}

	findings := ObservationFindings(s)

	require.Len(t, findings, 1)
	text := egressAllowWrite(t, findings[0]).Text
	require.Contains(t, text, "api.pinned.example")
	require.NotContains(t, text, "api.example,")
	require.Contains(t, text, "tls-bypass")
}

// TestObservationNetworkDiffIsSilentWhenAlreadyNarrow: a session whose used
// destinations the current `none` policy already allows yields no finding —
// there is nothing to change.
func TestObservationNetworkDiffIsSilentWhenAlreadyNarrow(t *testing.T) {
	s := SessionFacts{
		Current: network.Policy{
			Profiles:     []network.Profile{network.ProfileNone},
			EgressAllow:  []string{"api.example"},
			InterceptTLS: network.InterceptOn,
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
			Profiles:     []network.Profile{network.ProfileNone},
			EgressAllow:  []string{"api.example"},
			InterceptTLS: network.InterceptOn,
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
			{Host: "used.example", Success: true, Count: 1},
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
			{Host: "dead.example", Protocol: channel.ConnectProtocol, Success: true},
			{Host: "dead.example", Protocol: "https", Path: "/", Success: false, Denied: true},
			{Host: "used.example", Protocol: "https", Success: true, Count: 1},
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
				{Host: "live.example", Protocol: channel.ConnectProtocol, Success: true},
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
		Value:    "sk-dotenv",
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
			Value:    "sk-secret",
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
			Value:    "sk-custom",
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
			Value:    "sk-secret",
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

// TestObservationSecretEmptyValueYieldsNoFinding: an empty static value is not
// a secret, so a source with an empty value yields no finding alone and
// contributes nothing to a merge — the non-empty source's hosts alone scope the
// merged finding.
func TestObservationSecretEmptyValueYieldsNoFinding(t *testing.T) {
	emptyOnly := SessionFacts{
		Current: narrowNone(),
		ReadBack: readback.ReadBack{Static: []readback.Credential{{
			Kind:     readback.CredentialStatic,
			EnvVar:   "OPENAI_API_KEY",
			Provider: "openai",
			Field:    "authorization",
			Hosts:    []string{"api.example"},
			Value:    "",
		}}},
	}
	require.Empty(t, ObservationFindings(emptyOnly), "an empty value is not a secret")

	merged := SessionFacts{
		Current: narrowNone(),
		ReadBack: readback.ReadBack{Static: []readback.Credential{
			{
				Kind:     readback.CredentialStatic,
				EnvVar:   "OPENAI_API_KEY",
				Provider: "openai",
				Field:    "authorization",
				Hosts:    []string{"empty.example"},
				Value:    "",
			},
			{
				Kind:     readback.CredentialStatic,
				EnvVar:   "OPENAI_API_KEY",
				Provider: "openai",
				Field:    "x-api-key",
				Hosts:    []string{"real.example"},
				Value:    "sk-real",
			},
		}},
	}
	findings := ObservationFindings(merged)
	require.Len(t, findings, 1, "one finding for the non-empty credential")
	require.Equal(t, []string{"real.example"}, findings[0].Writes[0].Secret.Hosts,
		"the empty source contributes no hosts")
	require.Equal(t, []string{"x-api-key"}, findings[0].Writes[0].Secret.HeaderFields,
		"the empty source contributes no header names")
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
			Value:    "sk-secret",
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
			Value:    "sk-secret",
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
// Succeeded is true when the session used a host, and that a session built only
// from a denial proposes no lock at all.
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
	require.Empty(t, ObservationFindings(deniedOnly), "no used host means no lock to propose")
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
		{Host: "dead.example", Protocol: channel.ConnectProtocol, Success: true},
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

// TestUsedRequestCountLeavesOutTunnels pins that an accepted CONNECT tunnel is
// not a request: the requests inside it are counted on their own entries, so
// counting the tunnel too would double the evidence.
func TestUsedRequestCountLeavesOutTunnels(t *testing.T) {
	total := usedRequestCount([]channel.Destination{
		{Host: "api.example", Protocol: "connect", Success: true, Count: 4},
		{Host: "api.example", Protocol: "https", Success: true, Count: 6},
	})
	require.Equal(t, 6, total)
}

// TestSecretFindingsSkipUnscopableCredentialsWithoutStopping pins that a
// credential the read-back could not name and one whose host set could not be
// derived are each skipped without stopping the loop, so a later scoped
// credential still yields a finding.
func TestSecretFindingsSkipUnscopableCredentialsWithoutStopping(t *testing.T) {
	s := SessionFacts{
		Current: narrowNone(),
		ReadBack: readback.ReadBack{Static: []readback.Credential{
			{
				Provider: "openai",
				Hosts:    []string{"skip.example"},
				Value:    "sk-unscoped",
			}, // no EnvVar
			{Kind: readback.CredentialStatic, EnvVar: "HOSTLESS_TOKEN", Provider: "x", Value: "sk-hostless"}, // no host
			{
				Kind:     readback.CredentialStatic,
				EnvVar:   "OPENAI_API_KEY",
				Provider: "openai",
				Hosts:    []string{"api.example"},
				Value:    "sk-scoped",
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
