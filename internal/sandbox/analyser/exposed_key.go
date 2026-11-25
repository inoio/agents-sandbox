package analyser

import (
	"strings"

	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
)

// exposedKeyFindings reports each login store that holds a plain-text API key.
// Every finding is report-only: learn mode never writes into an agent's home
// volume, so it guides the user to the env secret that replaces the key.
func exposedKeyFindings(exposed []readback.ExposedKey) []Finding {
	findings := make([]Finding, 0, len(exposed))
	for _, key := range exposed {
		findings = append(findings, exposedKeyFinding(key))
	}
	return findings
}

// exposedKeyFinding builds one store's report: the evidence names the store
// and the API hosts, and the write carries the guidance as text alone.
func exposedKeyFinding(key readback.ExposedKey) Finding {
	//exhaustruct:ignore // a report names no denials, count, or header fields
	evidence := Evidence{
		Summary:      "An API key is stored in plain text in ~/" + key.Source + " on the home volume.",
		Destinations: key.Hosts,
		Channel:      evidenceChannelReadback,
	}
	//exhaustruct:ignore // a report-only write names no key, secret, or mode
	guidance := Write{
		Scope: ScopePerSlugUser,
		Text: "Keep it on the host: declare " + key.EnvVar + " as an env secret for " +
			strings.Join(key.Hosts, ", ") + ", " + key.Remedy + ".",
	}
	//exhaustruct:ignore // EvidenceHash is stamped by ObservationFindings
	return Finding{Evidence: evidence, Writes: []Write{guidance}, Kind: KindObservation}
}
