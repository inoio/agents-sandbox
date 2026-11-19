package analyser

import (
	"slices"
	"strings"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/channel"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
)

// evidenceChannelProxy and evidenceChannelReadback name where a finding's
// evidence came from: the terminating proxy's observation, or the home
// read-back.
const (
	evidenceChannelProxy    = "proxy"
	evidenceChannelReadback = "readback"
)

// connectProtocol is the channel's protocol marker for an accepted CONNECT
// tunnel. The tunnel is recorded as its own successful destination, separately
// from the request that travelled through it.
const connectProtocol = "connect"

// SessionFacts is everything the after-run analysis needs from one finished
// session: the home read-back, the observation channel's scrubbed capture, and
// the network policy in effect while the session ran, for the never-widen diff.
type SessionFacts struct {
	// Paths is the config-path resolver. It is carried for callers that need to
	// resolve a scope's directory; the observation finder writes per-slug only
	// and consults no layer ownership.
	Paths       configpaths.ConfigPaths
	Slug        string
	ReadBack    readback.ReadBack
	Observation channel.Observation
	Current     network.Policy // the effective policy, for the never-widen diff
}

// ObservationFindings derives the network and secret findings a session's
// observation supports. Every write is per-slug and non-removing: the proposal
// never removes a user-set entry and never widens a policy, because the
// proposed allow set is the current allow set plus only the destinations the
// session actually used. A denied destination is evidence, never an allow entry.
//
//deadcode:ignore reason="observation findings engine; pending S7 run --learn caller wires the analyser"
func ObservationFindings(s SessionFacts) []Finding {
	findings := s.secretFindings()
	if networkFinding, ok := s.networkFinding(); ok {
		findings = append([]Finding{networkFinding}, findings...)
	}
	for i := range findings {
		findings[i] = findings[i].withEvidenceHash()
	}
	return findings
}

// networkFinding proposes the narrowest policy that still admits what the
// session used: the terminal `none` profile plus the current allow set unioned
// with the observed-used hosts. It emits nothing when there is no observation
// to act on, or when the current `none` policy already admits everything the
// session used — a finding whose summary promised to lock egress would be a
// misleading no-op, and the spec's denial reporting is S7's, not a finding's.
//
//deadcode:ignore reason="observation findings engine; pending S7 run --learn caller wires the analyser"
func (s SessionFacts) networkFinding() (Finding, bool) {
	destinations := s.Observation.Destinations
	used := observedUsedHosts(destinations)
	denied := deniedHosts(destinations)
	if len(used) == 0 && len(denied) == 0 {
		return Finding{}, false
	}

	current := s.Current.Effective()
	allow := sortedUnique(append(slices.Clone(current.EgressAllow), used...))
	if policyIsNarrowNone(current, allow) {
		return Finding{}, false
	}

	//exhaustruct:ignore // a network finding carries no credential header names
	evidence := Evidence{
		Summary:      "lock egress to the destinations this session used",
		Destinations: used,
		Denied:       denied,
		Count:        usedRequestCount(destinations),
		Succeeded:    len(used) > 0,
		Channel:      evidenceChannelProxy,
	}
	writes := []Write{
		//exhaustruct:ignore // a config write names no secret and never removes
		{
			Scope:       ScopePerSlugUser,
			ConfigKey:   "network.profile",
			ConfigValue: network.ProfileNone.String(),
			Text:        "This scopes the policy to the per-slug user config, keyed by the repo slug, so it applies to every clone of the remote.",
		},
		//exhaustruct:ignore // a config write names no secret and never removes
		{
			Scope:       ScopePerSlugUser,
			ConfigKey:   "network.egress-allow",
			ConfigValue: allow,
			Text:        "The network policy is creation-only: it takes effect at the next VM creation.",
		},
	}
	//exhaustruct:ignore // EvidenceHash is stamped by ObservationFindings
	return Finding{Evidence: evidence, Writes: writes, Kind: KindObservation}, true
}

// policyIsNarrowNone reports whether policy is already the terminal `none`
// profile with exactly allow as its allow set.
//
//deadcode:ignore reason="observation findings engine; pending S7 run --learn caller wires the analyser"
func policyIsNarrowNone(policy network.Policy, allow []string) bool {
	if !slices.Equal(policy.Profiles, []network.Profile{network.ProfileNone}) {
		return false
	}
	return slices.Equal(sortedUnique(policy.EgressAllow), allow)
}

// observedUsedHosts returns the sorted, de-duplicated hosts the session used. A
// successful non-connect destination is use. An accepted connect tunnel is use
// only when its host has no denied destination: the channel records the tunnel's
// acceptance separately from a request denied through it, so a host whose only
// request was denied still carries a successful connect entry, and that tunnel
// is not evidence the host is safe to allow. A denied destination is never a used
// one.
//
//deadcode:ignore reason="observation findings engine; pending S7 run --learn caller wires the analyser"
func observedUsedHosts(destinations []channel.Destination) []string {
	denied := deniedHosts(destinations)
	var hosts []string
	for _, destination := range destinations {
		if destination.Denied || !destination.Success || destination.Host == "" {
			continue
		}
		if destination.Protocol == connectProtocol && slices.Contains(denied, destination.Host) {
			continue
		}
		hosts = append(hosts, destination.Host)
	}
	return sortedUnique(hosts)
}

// deniedHosts returns the sorted, de-duplicated hosts of the destinations the
// proxy's own dial refused.
//
//deadcode:ignore reason="observation findings engine; pending S7 run --learn caller wires the analyser"
func deniedHosts(destinations []channel.Destination) []string {
	var hosts []string
	for _, destination := range destinations {
		if !destination.Denied || destination.Host == "" {
			continue
		}
		hosts = append(hosts, destination.Host)
	}
	return sortedUnique(hosts)
}

// usedRequestCount sums the request counts of the destinations the session used,
// so the evidence names how often the allow set was exercised.
//
//deadcode:ignore reason="observation findings engine; pending S7 run --learn caller wires the analyser"
func usedRequestCount(destinations []channel.Destination) int {
	total := 0
	for _, destination := range destinations {
		if destination.Denied || !destination.Success {
			continue
		}
		total += destination.Count
	}
	return total
}

// secretFindings proposes one env.secret.yaml write per static credential whose
// host set and env-var name are both derivable. A credential with no env-var
// name cannot be scoped, and one with no host is not migrated: neither yields a
// finding.
//
//deadcode:ignore reason="observation findings engine; pending S7 run --learn caller wires the analyser"
func (s SessionFacts) secretFindings() []Finding {
	var findings []Finding
	for _, credential := range s.ReadBack.Static {
		if credential.EnvVar == "" {
			continue
		}
		hosts, headerNames, attributed := credentialHosts(credential, s.Observation.Attribution)
		if len(hosts) == 0 {
			continue
		}
		findings = append(findings, secretFinding(credential, hosts, headerNames, attributed))
	}
	return findings
}

// credentialHosts returns the host set to scope a static credential to. The
// channel attribution — the destinations the credential was observed sent to —
// supersedes the endpoint the read-back found in the credential's config entry.
// The header names are the credential-bearing names the attribution saw; with no
// attribution they fall back to the credential's header-field hint. attributed
// reports which source supplied the hosts.
//
//deadcode:ignore reason="observation findings engine; pending S7 run --learn caller wires the analyser"
func credentialHosts(
	credential readback.Credential,
	attribution []channel.Attribution,
) ([]string, []string, bool) {
	var hosts, headerNames []string
	for _, entry := range attribution {
		if entry.Credential != credential.EnvVar || entry.Host == "" {
			continue
		}
		hosts = append(hosts, entry.Host)
		headerNames = append(headerNames, entry.HeaderNames...)
	}
	if len(hosts) > 0 {
		return sortedUnique(hosts), sortedUnique(headerNames), true
	}
	return sortedUnique(credential.Hosts), headerFieldNames(credential.Field), false
}

// secretFinding builds the per-slug secret write and its evidence. The evidence
// carries the header NAMES only, never a value.
//
//deadcode:ignore reason="observation findings engine; pending S7 run --learn caller wires the analyser"
func secretFinding(credential readback.Credential, hosts, headerNames []string, attributed bool) Finding {
	//exhaustruct:ignore // a secret finding reports no denials and no request count
	evidence := Evidence{
		Summary:      "static credential " + credential.EnvVar + " used at " + strings.Join(hosts, ", "),
		Destinations: hosts,
		Succeeded:    attributed,
		Channel:      evidenceChannelReadback,
		HeaderFields: headerNames,
	}
	//exhaustruct:ignore // a secret write names no config key and no removal
	write := Write{
		Scope: ScopePerSlugUser,
		//exhaustruct:ignore // a proposal never removes
		Secret: &SecretWrite{
			EnvVar:       credential.EnvVar,
			Provider:     credential.Provider,
			Hosts:        hosts,
			HeaderFields: headerNames,
		},
		Text: "The per-slug user config scopes this secret, keyed by the repo slug, so it applies to every clone of the remote.",
	}
	//exhaustruct:ignore // EvidenceHash is stamped by ObservationFindings
	return Finding{Evidence: evidence, Writes: []Write{write}, Kind: KindObservation}
}

// headerFieldNames wraps a header-field hint as a one-element list, or nil when
// there is no hint.
//
//deadcode:ignore reason="observation findings engine; pending S7 run --learn caller wires the analyser"
func headerFieldNames(field string) []string {
	if field == "" {
		return nil
	}
	return []string{field}
}

// sortedUnique returns a sorted copy of values with duplicates removed, leaving
// the caller's slice untouched. An empty input yields nil.
//
//deadcode:ignore reason="observation findings engine; pending S7 run --learn caller wires the analyser"
func sortedUnique(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	copied := slices.Clone(values)
	slices.Sort(copied)
	return slices.Compact(copied)
}
