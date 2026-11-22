package analyser

import (
	"slices"
	"strings"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/channel"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
)

// egressAllowKey is the config key of the network allow list the lock writes.
const egressAllowKey = "network.egress-allow"

// evidenceChannelProxy and evidenceChannelReadback name where a finding's
// evidence came from: the terminating proxy's observation, or the home
// read-back.
const (
	evidenceChannelProxy    = "proxy"
	evidenceChannelReadback = "readback"
)

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
func (s SessionFacts) networkFinding() (Finding, bool) {
	destinations := s.Observation.Destinations
	used := observedUsedHosts(destinations)
	denied := deniedHosts(destinations)
	if len(used) == 0 {
		return Finding{}, false
	}

	current := s.Current.Effective()
	allow := sortedUnique(append(slices.Clone(current.EgressAllow), used...))
	if policyIsNarrowNone(current, allow) {
		if !hostnameRulesNeedInterception(current) {
			return Finding{}, false
		}
		return interceptionFinding(current, used), true
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
			ConfigKey:   egressAllowKey,
			ConfigValue: allow,
			Text:        egressAllowText(current, used),
		},
	}
	if hostnameRulesNeedInterception(current) {
		writes = append(writes, interceptionWrite())
	}
	//exhaustruct:ignore // EvidenceHash is stamped by ObservationFindings
	return Finding{Evidence: evidence, Writes: writes, Kind: KindObservation}, true
}

// interceptionWrite turns TLS interception on in the per-slug scope.
func interceptionWrite() Write {
	//exhaustruct:ignore // a config write names no secret and never removes
	return Write{
		Scope:       ScopePerSlugUser,
		ConfigKey:   "network.intercept-tls",
		ConfigValue: string(network.InterceptOn),
		Text: "Under the strict-hostname mode a hostname rule admits only an intercepted connection, " +
			"so this turns TLS interception on.",
	}
}

// interceptionFinding is the finding for a none policy already narrowed to the
// used hosts but without interception: outside learn mode, where the overlay
// no longer forces interception, it blocks every host it allows. Its evidence
// names the hosts but no per-session count, so a decline sticks (a user whose
// secret already turns interception on declines it once).
func interceptionFinding(current network.Policy, used []string) Finding {
	write := interceptionWrite()
	if bypassed := bypassedHosts(used, current.TLSBypass); len(bypassed) > 0 {
		write.Text += " These hosts are in tls-bypass, so the strict-hostname mode keeps them denied: " +
			strings.Join(bypassed, ", ") + "."
	}
	//exhaustruct:ignore // an interception finding carries no count, denials, or header names
	evidence := Evidence{
		Summary:      "turn on TLS interception so the none profile's hostname rules admit connections",
		Destinations: used,
		Channel:      evidenceChannelProxy,
	}
	//exhaustruct:ignore // EvidenceHash is stamped by ObservationFindings
	return Finding{Evidence: evidence, Writes: []Write{write}, Kind: KindObservation}
}

// egressAllowText states when the lock takes effect and names the used hosts it
// cannot open: under the strict-hostname mode a host that is both allowlisted
// and in tls-bypass stays denied, so the lock must not promise it works.
func egressAllowText(current network.Policy, used []string) string {
	text := "The network policy is creation-only: it takes effect at the next VM creation."
	if current.DisableStrict {
		return text
	}
	bypassed := bypassedHosts(used, current.TLSBypass)
	if len(bypassed) == 0 {
		return text
	}
	return text + " These hosts are in tls-bypass, so the strict-hostname mode keeps them denied: " +
		strings.Join(bypassed, ", ") + "."
}

// bypassedHosts returns the hosts a tls-bypass entry covers, compared without
// regard to case.
func bypassedHosts(hosts, bypass []string) []string {
	var covered []string
	for _, host := range hosts {
		for _, entry := range bypass {
			if bypassCovers(strings.ToLower(entry), strings.ToLower(host)) {
				covered = append(covered, host)
				break
			}
		}
	}
	return covered
}

// bypassCovers reports whether a tls-bypass entry covers host: an exact host,
// or a "*.suffix" entry covering the suffix's subdomains (not the suffix
// itself, and not a host that merely ends in the same letters).
func bypassCovers(entry, host string) bool {
	if suffix, wildcard := strings.CutPrefix(entry, "*."); wildcard {
		return strings.HasSuffix(host, "."+suffix)
	}
	return host == entry
}

// hostnameRulesNeedInterception reports whether the lock must turn TLS
// interception on for its hostname rules to admit anything: under the runtime's
// strict-hostname mode an allow rule authorizes only an intercepted connection,
// and interception left at auto is off when no secret is configured. With a
// secret configured, auto already intercepts and the write only makes it
// explicit (one VM recreate).
func hostnameRulesNeedInterception(policy network.Policy) bool {
	return policy.InterceptTLS != network.InterceptOn && !policy.DisableStrict
}

// policyIsNarrowNone reports whether policy is already the terminal `none`
// profile with exactly allow as its allow set.
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
// is not evidence the host is safe to allow. A tunnel still recorded under an
// address is not use either (see channel.Destination.AddressOnlyTunnel). A denied destination is
// never a used one.
func observedUsedHosts(destinations []channel.Destination) []string {
	denied := deniedHosts(destinations)
	var hosts []string
	for _, destination := range destinations {
		if destination.Denied || !destination.Success || destination.Host == "" {
			continue
		}
		if destination.Protocol == channel.ConnectProtocol && slices.Contains(denied, destination.Host) {
			continue
		}
		if destination.AddressOnlyTunnel() {
			continue
		}
		hosts = append(hosts, destination.Host)
	}
	return sortedUnique(hosts)
}

// deniedHosts returns the sorted, de-duplicated hosts of the destinations the
// proxy's own dial refused.
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
func usedRequestCount(destinations []channel.Destination) int {
	total := 0
	for _, destination := range destinations {
		if destination.Denied || !destination.Success || destination.Protocol == channel.ConnectProtocol {
			continue
		}
		total += destination.Count
	}
	return total
}

// secretFindings proposes one env.secret.yaml write per static env-var name. Two
// static sources can name the same env var — a dotenv entry and an inline
// provider key, or two inline entries — and two writes for the same literal key
// would make the later write replace the earlier entry and drop its host set, so
// the finding merges every source's hosts and header names. An empty value is not
// a secret, so a source with an empty value contributes no finding and no hosts.
// A credential with no env-var name cannot be scoped, and a name with no host
// after the merge is not migrated: neither yields a finding.
func (s SessionFacts) secretFindings() []Finding {
	var names []string
	merged := map[string]mergedSecret{}
	for _, credential := range s.ReadBack.Static {
		if credential.EnvVar == "" || credential.Value == "" {
			continue
		}
		hosts, headerNames, attributed := credentialHosts(credential, s.Observation.Attribution)
		secret, seen := merged[credential.EnvVar]
		if !seen {
			//exhaustruct:ignore // the accumulators start at their zero values
			secret = mergedSecret{provider: credential.Provider}
			names = append(names, credential.EnvVar)
		}
		secret.hosts = append(secret.hosts, hosts...)
		secret.headerNames = append(secret.headerNames, headerNames...)
		secret.attributed = secret.attributed || attributed
		merged[credential.EnvVar] = secret
	}

	var findings []Finding
	for _, name := range names {
		secret := merged[name]
		hosts := sortedUnique(secret.hosts)
		if len(hosts) == 0 {
			continue
		}
		findings = append(findings, secretFinding(
			name,
			secret.provider,
			hosts,
			sortedUnique(secret.headerNames),
			secret.attributed,
		))
	}
	return findings
}

// mergedSecret accumulates what every static credential sharing one env-var name
// contributes to that name's single finding.
type mergedSecret struct {
	provider    string
	hosts       []string
	headerNames []string
	attributed  bool
}

// credentialHosts returns the host set to scope a static credential to. The
// channel attribution — the destinations the credential was observed sent to —
// supersedes the endpoint the read-back found in the credential's config entry.
// The header names are the credential-bearing names the attribution saw; with no
// attribution they fall back to the credential's header-field hint. attributed
// reports which source supplied the hosts.
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
func secretFinding(envVar, provider string, hosts, headerNames []string, attributed bool) Finding {
	//exhaustruct:ignore // a secret finding reports no denials and no request count
	evidence := Evidence{
		Summary:      "static credential " + envVar + " used at " + strings.Join(hosts, ", "),
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
			EnvVar:       envVar,
			Provider:     provider,
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
func headerFieldNames(field string) []string {
	if field == "" {
		return nil
	}
	return []string{field}
}

// sortedUnique returns a sorted copy of values with duplicates removed, leaving
// the caller's slice untouched. An empty input yields nil.
func sortedUnique(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	copied := slices.Clone(values)
	slices.Sort(copied)
	return slices.Compact(copied)
}
