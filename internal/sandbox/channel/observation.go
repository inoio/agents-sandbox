package channel

import (
	"cmp"
	"net"
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
	Host      string    `json:"host"`             // Host header / SNI
	IP        string    `json:"ip"`               // literal IP when the CONNECT target is an IP; empty for a hostname target (unknown to the analyser)
	Port      uint16    `json:"port"`             // port the request used
	Protocol  string    `json:"protocol"`         // "https" | "http" | "connect" | "socks5" | "tcp"; empty only when a recorded denial named none
	Path      string    `json:"path"`             // request path, never a query
	Success   bool      `json:"success"`          // a response arrived, or — for a "connect" entry — the tunnel was accepted
	Denied    bool      `json:"denied"`           // our proxy's dial to it failed; not a sandbox-policy denial (the runtime enforces egress before our proxy under T2)
	Count     int       `json:"count"`            // requests observed to this destination
	FirstSeen time.Time `json:"first_seen"`       // first request observed
	LastSeen  time.Time `json:"last_seen"`        // most recent request observed
	Channel   string    `json:"channel"`          // which observer saw it, e.g. "proxy-terminate"
	Failed    int       `json:"failed,omitempty"` // tunnels whose terminating TLS handshake the client aborted
}

// HandshakeFailed reports whether d is a tunnel whose every terminating TLS
// handshake failed: a client that pins its certificates, or one missing the
// session CA trust (#812). The two look the same to the proxy.
func (d Destination) HandshakeFailed() bool {
	return d.Protocol == ConnectProtocol && d.Count > 0 && d.Failed >= d.Count
}

// ConnectProtocol is the protocol of an accepted CONNECT tunnel's entry.
const ConnectProtocol = "connect"

// AddressOnlyTunnel reports whether d is a CONNECT tunnel still recorded under
// its IP. The proxy names every tunnel that carried a TLS handshake with SNI by
// that hostname, and the requests inside any tunnel are recorded on their own
// entries, so an address-only tunnel is no evidence of use on its own: it is an
// abandoned attempt (happy eyeballs, a pre-warmed connection) or non-HTTP
// traffic the channel cannot attribute to a host.
func (d Destination) AddressOnlyTunnel() bool {
	return d.Protocol == ConnectProtocol && net.ParseIP(d.Host) != nil
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
	// ProxyLog is the session's proxy log file, empty when no log was created.
	ProxyLog string `json:"proxy_log,omitempty"`
	// ProxyWarnings counts warnings, including those omitted after the log cap.
	ProxyWarnings int `json:"proxy_warnings,omitempty"`
	// UnattributedHandshakes counts failures that could not be tied to one
	// CONNECT session and destination without guessing.
	UnattributedHandshakes int `json:"unattributed_handshakes,omitempty"`
	// ProxyLogTruncated reports that the bounded log omitted warning lines.
	ProxyLogTruncated bool `json:"proxy_log_truncated,omitempty"`
	// ProxyLogWriteFailed reports an open, write, or close error.
	ProxyLogWriteFailed bool `json:"proxy_log_write_failed,omitempty"`
}

// recorder accumulates the artefact as requests are observed. Its methods take
// the destination and the request's header NAMES; no value is ever passed in.
// Destinations are held as stable pointers, so the entry observe returns stays
// valid for the recorder's lifetime and a caller can keep it from the request
// hook until the response hook.
type recorder struct {
	destinations     []*Destination
	attribution      []Attribution
	tunnelsBySession map[uint16]tunnelSession
}

type tunnelSession struct {
	destination *Destination
}

// observe folds one request into the artefact and returns the destination entry
// it belongs to, so the caller can complete what only the transport knows
// (success once a response arrives). A repeated destination aggregates: its
// count rises and its last-seen time advances, and the returned pointer keeps
// naming the same entry. A destination is identified by host, IP, port, path and
// protocol, so the same host on two ports — two CONNECT tunnels, say — stays two
// entries rather than collapsing into one whose port is whichever arrived last.
// The header names are added to the destination's attribution; a request
// carrying none stays unattributed. The pointer stays valid for the recorder's
// lifetime — until result or teardown tears it down — so a caller may hold it
// across later observe calls.
func (r *recorder) observe(host, ip, path, protocol string, port uint16, headerNames []string) *Destination {
	destination := r.destination(host, ip, path, protocol, port)
	destination.Count++
	now := time.Now()
	if destination.FirstSeen.IsZero() {
		destination.FirstSeen = now
	}
	destination.LastSeen = now
	r.attribute(host, path, headerNames)
	return destination
}

// observeTunnel records an accepted CONNECT and associates it with goproxy's
// session number. The logger carries the same 16-bit session number in a
// handshake warning, so SNI naming and failure accounting can follow one
// tunnel even when other hosts share its IP.
func (r *recorder) observeTunnel(sessionID uint16, host, ip string, port uint16) {
	tunnel := r.observe(host, ip, "", ConnectProtocol, port, nil)
	tunnel.Success = true
	if r.tunnelsBySession == nil {
		r.tunnelsBySession = make(map[uint16]tunnelSession)
	}
	if _, ok := r.tunnelsBySession[sessionID]; ok {
		r.tunnelsBySession[sessionID] = tunnelSession{destination: nil}
		return
	}
	r.tunnelsBySession[sessionID] = tunnelSession{destination: tunnel}
}

// nameTunnel moves the session's CONNECT entry onto the hostname from its TLS
// SNI. The IP stays on the named entry; the IP-only entry loses that tunnel and
// disappears once it has none left. A session number collision or unknown
// session is left unnamed rather than charging the wrong tunnel.
func (r *recorder) nameTunnel(sessionID uint16, connectIP string, port uint16, hostname string) {
	tracked := r.tunnelsBySession[sessionID]
	if tracked.destination == nil {
		return
	}
	tunnel := tracked.destination
	if tunnel.Host != connectIP || tunnel.Port != port {
		return
	}
	for index, destination := range r.destinations {
		if destination != tunnel {
			continue
		}
		named := r.observe(hostname, connectIP, "", ConnectProtocol, port, nil)
		named.Success = true
		tunnel.Count--
		if tunnel.Count <= 0 {
			r.destinations = slices.Delete(r.destinations, index, index+1)
		}
		r.tunnelsBySession[sessionID] = tunnelSession{destination: named}
		return
	}
}

// failTunnel records a failed terminating handshake on the CONNECT session that
// goproxy names in its warning. A missing, reused, or mismatched session is left
// unattributed rather than charged to a plausible but unverified destination.
func (r *recorder) failTunnel(sessionID uint16, connectTarget string) bool {
	host, port := splitHostPort(connectTarget)
	if parsed := net.ParseIP(host); parsed != nil {
		host = parsed.String()
	}
	tracked := r.tunnelsBySession[sessionID]
	if tracked.destination == nil {
		return false
	}
	tunnel := tracked.destination
	if tunnel.Port != port || (tunnel.Host != host && tunnel.IP != host) {
		return false
	}
	tunnel.Failed++
	return true
}

// deny records that a request to a destination was refused by this proxy's own
// dial. It marks every entry with that host, IP, port, path and protocol denied
// and not successful, and records the destination when the denial arrived before
// any request was seen (a refused connection). Including the port and protocol
// keeps a denial from matching — and clearing the `Success` of — an entry on a
// different port or a `connect` (or `https`) entry. A denial never changes the
// request count.
func (r *recorder) deny(host, ip, path, protocol string, port uint16) {
	matched := false
	for _, destination := range r.destinations {
		if destination.Host == host && destination.IP == ip && destination.Path == path &&
			destination.Protocol == protocol && destination.Port == port {
			destination.Denied = true
			destination.Success = false
			matched = true
		}
	}
	if !matched {
		r.destinations = append(r.destinations, &Destination{
			Host:      host,
			IP:        ip,
			Port:      port,
			Protocol:  protocol,
			Path:      path,
			Success:   false,
			Denied:    true,
			Count:     0,
			FirstSeen: time.Time{},
			LastSeen:  time.Time{},
			Channel:   observedViaProxy,
			Failed:    0,
		})
	}
}

// result returns obs combined with what this recorder observed, every list
// sorted so the same observations always produce the same artefact. The
// recorder's own entries win for a destination or attribution both sides record,
// which makes result(Observation{}) the recorder's own sorted artefact.
func (r *recorder) result(obs Observation) Observation {
	//exhaustruct:ignore // the proxy log is set by stop, not by the recorder
	combined := Observation{
		Destinations: append(make([]Destination, 0, len(obs.Destinations)+len(r.destinations)), obs.Destinations...),
		Attribution: append(
			append(make([]Attribution, 0, len(obs.Attribution)+len(r.attribution)), obs.Attribution...),
			r.attribution...),
		ProxyLog:               obs.ProxyLog,
		ProxyWarnings:          obs.ProxyWarnings,
		UnattributedHandshakes: obs.UnattributedHandshakes,
		ProxyLogTruncated:      obs.ProxyLogTruncated,
		ProxyLogWriteFailed:    obs.ProxyLogWriteFailed,
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
func (r *recorder) destination(host, ip, path, protocol string, port uint16) *Destination {
	for _, candidate := range r.destinations {
		if candidate.Host == host && candidate.IP == ip && candidate.Path == path &&
			candidate.Protocol == protocol && candidate.Port == port {
			return candidate
		}
	}
	destination := &Destination{
		Host:      host,
		IP:        ip,
		Port:      port,
		Protocol:  protocol,
		Path:      path,
		Success:   false,
		Denied:    false,
		Count:     0,
		FirstSeen: time.Time{},
		LastSeen:  time.Time{},
		Channel:   observedViaProxy,
		Failed:    0,
	}
	r.destinations = append(r.destinations, destination)
	return destination
}

// attribute adds the request's credential-bearing header names to the
// destination's attribution, creating the attribution on first sight. A request
// with no credential-bearing header records nothing, so an unattributed
// destination stays unattributed rather than gaining an empty entry.
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
func mergeHeaderNames(recorded, incoming []string) []string {
	seen := make(map[string]bool)
	var merged []string
	for _, name := range slices.Concat(recorded, incoming) {
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

// sameDestinationIdentity reports whether two entries name the same destination:
// every field the recorder keys on — host, IP, port, path, and protocol.
func sameDestinationIdentity(a, b Destination) bool {
	return a.Host == b.Host && a.IP == b.IP && a.Port == b.Port && a.Path == b.Path && a.Protocol == b.Protocol
}

// compareDestination orders destinations by their identity, so the artefact is
// deterministic regardless of the order requests arrived in.
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
	if order := strings.Compare(a.Protocol, b.Protocol); order != 0 {
		return order
	}
	return cmp.Compare(a.Port, b.Port)
}

// compareAttribution orders attributions by their destination.
func compareAttribution(a, b Attribution) int {
	if order := strings.Compare(a.Host, b.Host); order != 0 {
		return order
	}
	return strings.Compare(a.Path, b.Path)
}
