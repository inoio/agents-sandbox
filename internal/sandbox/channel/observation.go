package channel

import (
	"slices"
	"strings"
	"time"
)

// observedViaProxy names the channel that saw a destination: this package's
// terminating proxy. A destination another observer records carries its own
// channel name.
const observedViaProxy = "proxy-terminate"

// Destination is one place a session reached, scrubbed to values that cannot
// leak: no request body, no header value, no query. It is the frozen contract
// the after-run analysis reads.
type Destination struct {
	Host      string    `json:"host"`       // Host header / SNI
	IP        string    `json:"ip"`         // literal IP when the CONNECT target is an IP; empty for a hostname target (unknown to the analyser)
	Port      uint16    `json:"port"`       // port the request used
	Protocol  string    `json:"protocol"`   // "https" | "http" | "connect" | "socks5" | "tcp"; empty for a deny seen before any request
	Path      string    `json:"path"`       // request path, never a query
	Success   bool      `json:"success"`    // a response arrived
	Denied    bool      `json:"denied"`     // egress policy or transport refused it
	Count     int       `json:"count"`      // requests observed to this destination
	FirstSeen time.Time `json:"first_seen"` // first request observed
	LastSeen  time.Time `json:"last_seen"`  // most recent request observed
	Channel   string    `json:"channel"`    // which observer saw it, e.g. "proxy-terminate"
}

// Attribution records that a credential was sent to a destination, as the
// credential-bearing header NAMES and, when known, the configured secret's
// env-var name. It never records a header value (hub invariant 3).
type Attribution struct {
	Host           string   `json:"host"`            // destination this credential went to
	Path           string   `json:"path"`            // request path the credential was sent to
	Credential     string   `json:"credential"`      // configured secret's env-var name, when substituted in flight
	HeaderNames    []string `json:"header_names"`    // credential-bearing header names only; never values
	SawReal        bool     `json:"saw_real"`        // a real value crossed (never stored)
	SawPlaceholder bool     `json:"saw_placeholder"` // a placeholder crossed unsubstituted
}

// Observation is the scrubbed capture a session leaves behind: every
// destination it reached and every credential it attributed, with no field able
// to carry a value.
type Observation struct {
	Destinations []Destination `json:"destinations"`
	Attribution  []Attribution `json:"attribution"`
}

// recorder accumulates the artefact as requests are observed. Its methods take
// the destination and the request's header NAMES; no value is ever passed in.
// Destinations are held as stable pointers, so the entry observe returns stays
// valid for the recorder's lifetime and a caller can keep it from the request
// hook until the response hook.
type recorder struct {
	destinations []*Destination
	attribution  []Attribution
}

// observe folds one request into the artefact and returns the destination entry
// it belongs to, so the caller can complete what only the transport knows (the
// port, and success once a response arrives). A repeated destination aggregates:
// its count rises and its last-seen time advances, and the returned pointer
// keeps naming the same entry. The header names are added to the destination's
// attribution; a request carrying none stays unattributed. The pointer stays
// valid for the recorder's lifetime — until result or teardown tears it down —
// so a caller may hold it across later observe calls.
//
//deadcode:ignore reason="the channel's capture entry point; the terminating proxy (next step in S4) calls observe from its request hook"
func (r *recorder) observe(host, ip, path, protocol string, headerNames []string) *Destination {
	destination := r.destination(host, ip, path, protocol)
	destination.Count++
	now := time.Now()
	if destination.FirstSeen.IsZero() {
		destination.FirstSeen = now
	}
	destination.LastSeen = now
	r.attribute(host, path, headerNames)
	return destination
}

// deny records that a request to a destination was refused. It marks every
// entry with that host, IP and path denied and not successful, and records the
// destination when the denial arrived before any request was seen (a refused
// connection). A denial never changes the request count.
//
//deadcode:ignore reason="records a denial; the terminating proxy's RoundTrip error hook (next step in S4) calls deny"
func (r *recorder) deny(host, ip, path string) {
	matched := false
	for _, destination := range r.destinations {
		if destination.Host == host && destination.IP == ip && destination.Path == path {
			destination.Denied = true
			destination.Success = false
			matched = true
		}
	}
	if !matched {
		r.destinations = append(r.destinations, &Destination{
			Host:      host,
			IP:        ip,
			Port:      0,
			Protocol:  "",
			Path:      path,
			Success:   false,
			Denied:    true,
			Count:     0,
			FirstSeen: time.Time{},
			LastSeen:  time.Time{},
			Channel:   observedViaProxy,
		})
	}
}

// result returns obs combined with what this recorder observed, every list
// sorted so the same observations always produce the same artefact. The
// recorder's own entries win for a destination or attribution both sides record,
// which makes result(r.obs) the recorder's own sorted artefact.
//
//deadcode:ignore reason="finalises the artefact; the terminating proxy's Stop (next step in S4) returns result"
func (r *recorder) result(obs Observation) Observation {
	combined := Observation{
		Destinations: append(make([]Destination, 0, len(obs.Destinations)+len(r.destinations)), obs.Destinations...),
		Attribution: append(
			append(make([]Attribution, 0, len(obs.Attribution)+len(r.attribution)), obs.Attribution...),
			r.attribution...),
	}
	for _, destination := range r.destinations {
		combined.Destinations = append(combined.Destinations, *destination)
	}
	combined.Destinations = dedupeDestinations(combined.Destinations)
	combined.Attribution = dedupeAttributions(combined.Attribution)
	slices.SortFunc(combined.Destinations, compareDestination)
	slices.SortFunc(combined.Attribution, compareAttribution)
	return combined
}

// destination returns the entry for a destination, appending a new one when the
// destination was not yet seen. The first observation fixes the channel; the
// entry is heap-allocated, so the returned pointer outlives later appends.
//
//deadcode:ignore reason="reached through observe, whose pending production caller is the terminating proxy (next step in S4)"
func (r *recorder) destination(host, ip, path, protocol string) *Destination {
	for _, candidate := range r.destinations {
		if candidate.Host == host && candidate.IP == ip && candidate.Path == path && candidate.Protocol == protocol {
			return candidate
		}
	}
	destination := &Destination{
		Host:      host,
		IP:        ip,
		Port:      0,
		Protocol:  protocol,
		Path:      path,
		Success:   false,
		Denied:    false,
		Count:     0,
		FirstSeen: time.Time{},
		LastSeen:  time.Time{},
		Channel:   observedViaProxy,
	}
	r.destinations = append(r.destinations, destination)
	return destination
}

// attribute adds the request's credential-bearing header names to the
// destination's attribution, creating the attribution on first sight. A request
// with no credential-bearing header records nothing, so an unattributed
// destination stays unattributed rather than gaining an empty entry.
//
//deadcode:ignore reason="reached through observe, whose pending production caller is the terminating proxy (next step in S4)"
func (r *recorder) attribute(host, path string, headerNames []string) {
	if len(headerNames) == 0 {
		return
	}
	for i := range r.attribution {
		attribution := &r.attribution[i]
		if attribution.Host != host || attribution.Path != path {
			continue
		}
		attribution.HeaderNames = mergeHeaderNames(attribution.HeaderNames, headerNames)
		return
	}
	r.attribution = append(r.attribution, Attribution{
		Host:           host,
		Path:           path,
		Credential:     "",
		HeaderNames:    mergeHeaderNames(nil, headerNames),
		SawReal:        false,
		SawPlaceholder: false,
	})
}

// mergeHeaderNames returns the union of the recorded and incoming header names,
// deduplicated and sorted, so the artefact does not depend on the order the
// header names arrived in.
//
//deadcode:ignore reason="reached through observe and attribute, whose pending production caller is the terminating proxy (next step in S4)"
func mergeHeaderNames(recorded, incoming []string) []string {
	seen := make(map[string]bool, len(recorded)+len(incoming))
	merged := make([]string, 0, len(recorded)+len(incoming))
	for _, name := range append(append(make([]string, 0, len(recorded)+len(incoming)), recorded...), incoming...) {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		merged = append(merged, name)
	}
	slices.Sort(merged)
	return merged
}

// dedupeDestinations keeps one entry per destination identity, the last one
// winning, so the recorder's own entry (appended second) supersedes an equal
// destination in the given artefact.
//
//deadcode:ignore reason="reached through recorder.result, whose pending production caller is the terminating proxy's Stop (next step in S4)"
func dedupeDestinations(destinations []Destination) []Destination {
	kept := make([]Destination, 0, len(destinations))
	for _, destination := range slices.Backward(destinations) {
		if slices.ContainsFunc(kept, func(candidate Destination) bool {
			return sameDestinationIdentity(candidate, destination)
		}) {
			continue
		}
		kept = append(kept, destination)
	}
	slices.Reverse(kept)
	return kept
}

// dedupeAttributions keeps one entry per attributed destination, the last one
// winning.
//
//deadcode:ignore reason="reached through recorder.result, whose pending production caller is the terminating proxy's Stop (next step in S4)"
func dedupeAttributions(attributions []Attribution) []Attribution {
	kept := make([]Attribution, 0, len(attributions))
	for _, attribution := range slices.Backward(attributions) {
		if slices.ContainsFunc(kept, func(candidate Attribution) bool {
			return candidate.Host == attribution.Host && candidate.Path == attribution.Path
		}) {
			continue
		}
		kept = append(kept, attribution)
	}
	slices.Reverse(kept)
	return kept
}

// sameDestinationIdentity reports whether two entries name the same destination;
// the port is not part of the identity the recorder can key on, so the fields it
// observes are.
//
//deadcode:ignore reason="keys destination identity for dedupeDestinations, reached through the terminating proxy's Stop (next step in S4)"
func sameDestinationIdentity(a, b Destination) bool {
	return a.Host == b.Host && a.IP == b.IP && a.Path == b.Path && a.Protocol == b.Protocol
}

// compareDestination orders destinations by their identity, so the artefact is
// deterministic regardless of the order requests arrived in.
//
//deadcode:ignore reason="orders the artefact for deterministic capture, reached through the terminating proxy's Stop (next step in S4)"
func compareDestination(a, b Destination) int {
	if order := strings.Compare(a.Host, b.Host); order != 0 {
		return order
	}
	if order := strings.Compare(a.IP, b.IP); order != 0 {
		return order
	}
	if order := strings.Compare(a.Path, b.Path); order != 0 {
		return order
	}
	return strings.Compare(a.Protocol, b.Protocol)
}

// compareAttribution orders attributions by their destination.
//
//deadcode:ignore reason="orders the artefact for deterministic capture, reached through the terminating proxy's Stop (next step in S4)"
func compareAttribution(a, b Attribution) int {
	if order := strings.Compare(a.Host, b.Host); order != 0 {
		return order
	}
	return strings.Compare(a.Path, b.Path)
}
