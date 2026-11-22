package channel

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRecorderAggregatesRepeatedDestination(t *testing.T) {
	var r recorder
	first := r.observe("api.example", "203.0.113.7", "/v1/chat", "https", 443, []string{"authorization"})
	second := r.observe("api.example", "203.0.113.7", "/v1/chat", "https", 443, []string{"authorization"})

	if first != second {
		t.Fatal("the same destination must aggregate onto one entry")
	}
	destinations := r.result(Observation{}).Destinations
	if got := len(destinations); got != 1 {
		t.Fatalf("destinations = %d, want 1", got)
	}
	destination := destinations[0]
	if destination.Count != 2 {
		t.Fatalf("count = %d, want 2", destination.Count)
	}
	if destination.Host != "api.example" || destination.IP != "203.0.113.7" ||
		destination.Path != "/v1/chat" || destination.Protocol != "https" {
		t.Fatalf("destination identity not recorded: %+v", destination)
	}
	if destination.FirstSeen.IsZero() || destination.LastSeen.Before(destination.FirstSeen) {
		t.Fatalf("timestamps not recorded: first=%v last=%v", destination.FirstSeen, destination.LastSeen)
	}
}

// TestRecorderObservedPointerStaysValid pins the recorder's pointer contract: a
// request hook holds the entry observe returned until the response hook, so a
// later observe that grows the recorder must not invalidate it, and what the
// holder wrote through it must reach the artefact.
func TestRecorderObservedPointerStaysValid(t *testing.T) {
	var r recorder
	held := r.observe("api.example", "203.0.113.7", "/v1/chat", "https", 443, []string{"authorization"})

	// More destinations reallocate whatever backs the recorder's collection.
	r.observe("other.example", "198.51.100.2", "/v1/models", "https", 443, nil)
	r.observe("third.example", "198.51.100.3", "/", "connect", 443, nil)

	held.Port = 443
	held.Success = true

	for _, destination := range r.result(Observation{}).Destinations {
		if destination.Host != "api.example" || destination.Path != "/v1/chat" {
			continue
		}
		if destination.Port != 443 || !destination.Success {
			t.Fatalf("a mutation through the held pointer was lost: %+v", destination)
		}
		return
	}
	t.Fatal("the held destination is missing from the artefact")
}

// TestRecorderNamesAnIPTunnelByItsSNIHostname: the msb runtime opens a tunnel
// by IP and names the host only in the TLS SNI. Once the name is known the
// tunnel is recorded under it, keeping the IP, so the analyser proposes a
// hostname rather than an address a CDN may rotate.
func TestRecorderNamesAnIPTunnelByItsSNIHostname(t *testing.T) {
	r := new(recorder)
	r.observe("160.79.104.10", "160.79.104.10", "", "connect", 443, nil).Success = true
	r.observe("160.79.104.10", "160.79.104.10", "", "connect", 443, nil).Success = true

	r.nameTunnel("160.79.104.10", 443, "api.anthropic.com")

	destinations := r.result(Observation{}).Destinations
	if len(destinations) != 2 {
		t.Fatalf("destinations = %+v, want the named tunnel and the one still unnamed", destinations)
	}
	named := destinationByHost(t, destinations, "api.anthropic.com")
	if named.IP != "160.79.104.10" || named.Protocol != "connect" || named.Port != 443 {
		t.Errorf("named tunnel = %+v, want connect to 160.79.104.10:443", named)
	}
	if !named.Success || named.Count != 1 {
		t.Errorf("named tunnel = %+v, want one successful tunnel", named)
	}
	unnamed := destinationByHost(t, destinations, "160.79.104.10")
	if unnamed.Count != 1 {
		t.Errorf("unnamed tunnel count = %d, want 1 left", unnamed.Count)
	}
}

func TestRecorderNamingTheLastIPTunnelRemovesTheIPEntry(t *testing.T) {
	r := new(recorder)
	r.observe("160.79.104.10", "160.79.104.10", "", "connect", 443, nil).Success = true

	r.nameTunnel("160.79.104.10", 443, "api.anthropic.com")

	destinations := r.result(Observation{}).Destinations
	if len(destinations) != 1 || destinations[0].Host != "api.anthropic.com" {
		t.Fatalf("destinations = %+v, want only the named tunnel", destinations)
	}
}

func TestRecorderNameTunnelIgnoresAnUnknownTunnel(t *testing.T) {
	r := new(recorder)
	r.observe("api.example", "", "", "connect", 443, nil).Success = true

	r.nameTunnel("203.0.113.7", 443, "other.example")

	destinations := r.result(Observation{}).Destinations
	if len(destinations) != 1 || destinations[0].Host != "api.example" {
		t.Fatalf("destinations = %+v, want the hostname tunnel untouched and nothing invented", destinations)
	}
}

func destinationByHost(t *testing.T, destinations []Destination, host string) Destination {
	t.Helper()
	for _, destination := range destinations {
		if destination.Host == host {
			return destination
		}
	}
	t.Fatalf("no destination for host %q in %+v", host, destinations)
	return Destination{}
}

func TestDestinationAddressOnlyTunnel(t *testing.T) {
	cases := map[string]struct {
		destination Destination
		want        bool
	}{
		"IPv4 tunnel":       {Destination{Host: "160.79.104.10", Protocol: ConnectProtocol}, true},
		"IPv6 tunnel":       {Destination{Host: "2607:6bc0::10", Protocol: ConnectProtocol}, true},
		"named tunnel":      {Destination{Host: "api.example", IP: "160.79.104.10", Protocol: ConnectProtocol}, false},
		"request to an IP":  {Destination{Host: "160.79.104.10", Protocol: "https"}, false},
		"request to a host": {Destination{Host: "api.example", Protocol: "https"}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.destination.AddressOnlyTunnel(); got != tc.want {
				t.Errorf("AddressOnlyTunnel(%+v) = %v, want %v", tc.destination, got, tc.want)
			}
		})
	}
}

// TestRecorderFailTunnelMarksTheLatestMatchingTunnel: goproxy reports a failed
// handshake by its CONNECT target. The failure belongs to the tunnel that most
// recently started a handshake to that target, which is the named entry when
// the client sent SNI.
func TestRecorderFailTunnelMarksTheLatestMatchingTunnel(t *testing.T) {
	r := new(recorder)
	r.observe("203.0.113.7", "203.0.113.7", "", ConnectProtocol, 443, nil).Success = true
	r.observe("203.0.113.7", "203.0.113.7", "", ConnectProtocol, 443, nil).Success = true
	r.nameTunnel("203.0.113.7", 443, "api.example")

	r.failTunnel("203.0.113.7:443")

	destinations := r.result(Observation{}).Destinations
	named := destinationByHost(t, destinations, "api.example")
	if named.Failed != 1 || !named.HandshakeFailed() {
		t.Errorf("named tunnel = %+v, want its one handshake failed", named)
	}
	unnamed := destinationByHost(t, destinations, "203.0.113.7")
	if unnamed.Failed != 0 {
		t.Errorf("unnamed tunnel = %+v, want no failure", unnamed)
	}
}

// TestRecorderFailTunnelChargesOnlyTheMatchingTunnel: a request entry for the
// same host and a tunnel on another port, both seen after the match, are not
// charged, and a non-canonical IPv6 target matches its canonical entry.
func TestRecorderFailTunnelChargesOnlyTheMatchingTunnel(t *testing.T) {
	r := new(recorder)
	// The matching tunnel comes first, so the most-recent rule would pick a
	// later non-matching entry if the filter let it through.
	r.observe("2607:6bc0::10", "2607:6bc0::10", "", ConnectProtocol, 443, nil).Success = true
	r.observe("2607:6bc0::10", "2607:6bc0::10", "", ConnectProtocol, 8443, nil).Success = true
	r.observe("2607:6bc0::10", "2607:6bc0::10", "", "https", 443, nil).Success = true

	r.failTunnel("[2607:6BC0:0::10]:443")

	for _, destination := range r.result(Observation{}).Destinations {
		want := 0
		if destination.Protocol == ConnectProtocol && destination.Port == 443 {
			want = 1
		}
		if destination.Failed != want {
			t.Errorf("destination %+v Failed = %d, want %d", destination, destination.Failed, want)
		}
	}
}

// TestRecorderFailTunnelMatchesANamedTunnelByItsIP: the runtime's failure names
// the IP it opened, while the tunnel is already recorded under its SNI name.
func TestRecorderFailTunnelMatchesANamedTunnelByItsIP(t *testing.T) {
	r := new(recorder)
	r.observe("api.example", "203.0.113.7", "", ConnectProtocol, 443, nil).Success = true

	r.failTunnel("203.0.113.7:443")

	if failed := r.result(Observation{}).Destinations[0].Failed; failed != 1 {
		t.Errorf("Failed = %d, want the named tunnel charged by its IP", failed)
	}
}

// TestRecorderFailTunnelLooksPastANonMatchingEntry: an earlier entry that does
// not match does not end the search.
func TestRecorderFailTunnelLooksPastANonMatchingEntry(t *testing.T) {
	r := new(recorder)
	r.observe("other.example", "", "", ConnectProtocol, 443, nil).Success = true
	r.observe("api.example", "", "", ConnectProtocol, 443, nil).Success = true

	r.failTunnel("api.example:443")

	for _, destination := range r.result(Observation{}).Destinations {
		want := 0
		if destination.Host == "api.example" {
			want = 1
		}
		if destination.Failed != want {
			t.Errorf("destination %+v Failed = %d, want %d", destination, destination.Failed, want)
		}
	}
}

func TestRecorderFailTunnelMatchesAHostnameTarget(t *testing.T) {
	r := new(recorder)
	r.observe("api.example", "", "", ConnectProtocol, 443, nil).Success = true

	r.failTunnel("api.example:443")
	r.failTunnel("other.example:443")

	destinations := r.result(Observation{}).Destinations
	if len(destinations) != 1 || destinations[0].Failed != 1 {
		t.Fatalf("destinations = %+v, want the one tunnel failed once and nothing invented", destinations)
	}
}

func TestDestinationHandshakeFailed(t *testing.T) {
	cases := map[string]struct {
		destination Destination
		want        bool
	}{
		"request": {Destination{Protocol: "https", Success: true, Count: 1, Failed: 1}, false},
		"tunnel":  {Destination{Protocol: ConnectProtocol, Success: true, Count: 2}, false},
		"tunnel, one of two failed": {
			Destination{Protocol: ConnectProtocol, Success: true, Count: 2, Failed: 1},
			false,
		},
		"tunnel, every one failed": {Destination{Protocol: ConnectProtocol, Success: true, Count: 2, Failed: 2}, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.destination.HandshakeFailed(); got != tc.want {
				t.Errorf("HandshakeFailed(%+v) = %v, want %v", tc.destination, got, tc.want)
			}
		})
	}
}

func TestRecorderKeepsDestinationsDistinct(t *testing.T) {
	var r recorder
	r.observe("api.example", "203.0.113.7", "/v1/chat", "https", 443, nil)
	r.observe("api.example", "203.0.113.7", "/v1/models", "https", 443, nil)
	r.observe("api.example", "203.0.113.7", "/v1/chat", "http", 443, nil)

	if got := len(r.result(Observation{}).Destinations); got != 3 {
		t.Fatalf("destinations = %d, want 3 (path and protocol both identify)", got)
	}
}

func TestRecorderRecordsCredentialBearingHeaderNames(t *testing.T) {
	var r recorder
	r.observe("api.example", "203.0.113.7", "/v1/chat", "https", 443, []string{"x-api-key", "authorization"})
	r.observe("api.example", "203.0.113.7", "/v1/chat", "https", 443, []string{"authorization"})

	attributions := r.result(Observation{}).Attribution
	if got := len(attributions); got != 1 {
		t.Fatalf("attributions = %d, want 1", got)
	}
	attribution := attributions[0]
	if attribution.Host != "api.example" || attribution.Path != "/v1/chat" {
		t.Fatalf("attribution names the wrong destination: %+v", attribution)
	}
	if want := []string{"authorization", "x-api-key"}; !slices.Equal(attribution.HeaderNames, want) {
		t.Fatalf("header names = %v, want %v (sorted, deduplicated)", attribution.HeaderNames, want)
	}
}

func TestRecorderRecordsNoAttributionWithoutCredentialHeader(t *testing.T) {
	var r recorder
	r.observe("api.example", "203.0.113.7", "/v1/chat", "https", 443, nil)

	if got := len(r.result(Observation{}).Attribution); got != 0 {
		t.Fatalf("attributions = %d, want 0: a request with no credential header is unattributed", got)
	}
}

func TestRecorderDenyMarksDestinationWithoutAllow(t *testing.T) {
	var r recorder
	destination := r.observe("blocked.example", "198.51.100.4", "/v1/chat", "https", 443, []string{"authorization"})
	destination.Success = true

	r.deny("blocked.example", "198.51.100.4", "/v1/chat", "https", 443)

	destinations := r.result(Observation{}).Destinations
	if len(destinations) != 1 {
		t.Fatalf("destinations = %d, want 1", len(destinations))
	}
	got := destinations[0]
	if !got.Denied {
		t.Fatal("deny must mark the destination denied")
	}
	if got.Success {
		t.Fatal("a denied destination must not be marked successful")
	}
	if got.Count != 1 {
		t.Fatalf("count = %d, want 1: deny must not change the request count", got.Count)
	}
}

func TestRecorderDenyRecordsUnobservedDestination(t *testing.T) {
	var r recorder
	r.deny("blocked.example", "198.51.100.4", "/v1/chat", "", 0)

	destinations := r.result(Observation{}).Destinations
	if len(destinations) != 1 {
		t.Fatalf("destinations = %d, want 1", len(destinations))
	}
	if !destinations[0].Denied {
		t.Fatal("a denial before any observed request must still be recorded")
	}
	if destinations[0].Protocol != "" {
		t.Fatalf("protocol = %q, want empty: a protocol-less deny cannot name one", destinations[0].Protocol)
	}
	if destinations[0].Port != 0 {
		t.Errorf("port = %d, want 0: a denial arrives before any request names a port", destinations[0].Port)
	}
	if destinations[0].Count != 0 {
		t.Errorf("count = %d, want 0: a denial is not a request", destinations[0].Count)
	}
	if destinations[0].Channel != observedViaProxy {
		t.Errorf("channel = %q, want %q", destinations[0].Channel, observedViaProxy)
	}
	// A denial is not a failed handshake, so the entry carries no failure count
	// and the serialised artefact omits the omitempty key entirely.
	if destinations[0].Failed != 0 {
		t.Errorf("failed = %d, want 0: a denial is not a failed handshake", destinations[0].Failed)
	}
	encoded, err := json.Marshal(destinations[0])
	if err != nil {
		t.Fatalf("marshal the deny entry: %v", err)
	}
	if strings.Contains(string(encoded), `"failed"`) {
		t.Errorf("deny entry = %s, want no failed count in the artefact", encoded)
	}
}

// A protocol-less deny must not match a "connect" entry: the tunnel was
// accepted, and a bare deny (which cannot name a protocol) clearing its
// Success would report a live tunnel as refused.
func TestRecorderDenyDoesNotClearConnectEntry(t *testing.T) {
	var r recorder
	connectEntry := r.observe("api.example", "", "", "connect", 443, nil)
	connectEntry.Success = true

	r.deny("api.example", "", "", "", 0)

	for _, destination := range r.result(Observation{}).Destinations {
		if destination.Protocol != "connect" {
			continue
		}
		if !destination.Success {
			t.Fatalf("the connect entry lost its Success: %+v", destination)
		}
		if destination.Denied {
			t.Fatalf("the connect entry was marked denied: %+v", destination)
		}
		return
	}
	t.Fatal("the connect entry is missing from the artefact")
}

func TestRecorderResultIsDeterministic(t *testing.T) {
	observations := []struct {
		host, ip, path, protocol string
		headerNames              []string
	}{
		{"z.example", "198.51.100.9", "/z", "https", []string{"authorization"}},
		{"a.example", "203.0.113.1", "/a", "https", []string{"x-api-key"}},
		{"m.example", "203.0.113.2", "/m", "connect", nil},
	}

	forward := new(recorder)
	for _, observation := range observations {
		forward.observe(
			observation.host,
			observation.ip,
			observation.path,
			observation.protocol,
			443,
			observation.headerNames,
		)
	}
	backward := new(recorder)
	for _, observation := range slices.Backward(observations) {
		backward.observe(
			observation.host,
			observation.ip,
			observation.path,
			observation.protocol,
			443,
			observation.headerNames,
		)
	}

	result := forward.result(Observation{})
	if want := []string{"a.example", "m.example", "z.example"}; !slices.Equal(destinationHosts(result), want) {
		t.Fatalf("hosts = %v, want %v (sorted)", destinationHosts(result), want)
	}
	if got, want := withoutTimestamps(
		backward.result(Observation{}),
	), withoutTimestamps(
		result,
	); !reflect.DeepEqual(
		got,
		want,
	) {
		t.Fatalf("insertion order changed the artefact:\n got %+v\nwant %+v", got, want)
	}
	if got, want := forward.result(Observation{}), result; !reflect.DeepEqual(got, want) {
		t.Fatalf("result must be repeatable and must not mutate the recorder:\n got %+v\nwant %+v", got, want)
	}
	if got := attributionHosts(result); !slices.Equal(got, []string{"a.example", "z.example"}) {
		t.Fatalf("attributions = %v, want sorted [a.example z.example]", got)
	}
}

func TestRecorderResultUnionsObservedAndGivenArtefacts(t *testing.T) {
	var r recorder
	r.observe("api.example", "203.0.113.7", "/v1/chat", "https", 443, []string{"authorization"})

	given := Observation{Destinations: []Destination{{Host: "config.example", Path: "/", Channel: "readback"}}}
	got := r.result(given)

	if want := []string{"api.example", "config.example"}; !slices.Equal(destinationHosts(got), want) {
		t.Fatalf("hosts = %v, want %v", destinationHosts(got), want)
	}
	if merged := r.result(r.result(Observation{})); len(merged.Destinations) != 1 {
		t.Fatalf("merging a recorder's own artefact must not duplicate: %+v", merged.Destinations)
	}
}

// TestObservationCarriesNoValueBearingField pins the serialised shape of the
// frozen artefact. A new field must not be able to carry a request body or a
// header value, so the exact key set fails the test when it grows.
func TestObservationCarriesNoValueBearingField(t *testing.T) {
	wantKeys := map[string][]string{
		"Destination": {
			"channel", "count", "denied", "first_seen", "host", "ip",
			"last_seen", "path", "port", "protocol", "success",
		},
		"Attribution": {"credential", "header_names", "host", "path", "saw_placeholder", "saw_real"},
		"Observation": {"attribution", "destinations"},
	}
	shapes := map[string]any{
		"Destination": Destination{},
		"Attribution": Attribution{},
		"Observation": Observation{},
	}
	for name, shape := range shapes {
		if got := jsonKeys(shape); !slices.Equal(got, wantKeys[name]) {
			t.Errorf("%s JSON keys = %v, want %v (hub invariant 3)", name, got, wantKeys[name])
		}
	}
	// Header NAMES are the one credential-adjacent thing the artefact carries.
	encoded, err := json.Marshal(Observation{Attribution: []Attribution{{HeaderNames: []string{"authorization"}}}})
	if err != nil {
		t.Fatalf("marshal observation: %v", err)
	}
	if !strings.Contains(string(encoded), `"authorization"`) {
		t.Fatalf("header names must serialise, got %s", encoded)
	}
}

// Destinations that share a host are ordered by the IP, path, then protocol
// tie-breakers, so the artefact does not depend on insertion order. Each pair
// differs in exactly one field after its predecessor, forcing that tie-breaker.
func TestRecorderOrdersDestinationsByTieBreakers(t *testing.T) {
	cases := []struct {
		name          string
		greaterLesser [2]Destination
	}{
		{
			"IP breaks a host tie",
			[2]Destination{
				{Host: "api.example", IP: "203.0.113.2", Path: "/a", Protocol: "https"},
				{Host: "api.example", IP: "203.0.113.1", Path: "/a", Protocol: "https"},
			},
		},
		{
			"path breaks an IP tie",
			[2]Destination{
				{Host: "api.example", IP: "203.0.113.1", Path: "/b", Protocol: "https"},
				{Host: "api.example", IP: "203.0.113.1", Path: "/a", Protocol: "https"},
			},
		},
		{
			"protocol breaks a path tie",
			[2]Destination{
				{Host: "api.example", IP: "203.0.113.1", Path: "/a", Protocol: "https"},
				{Host: "api.example", IP: "203.0.113.1", Path: "/a", Protocol: "http"},
			},
		},
		{
			"port breaks a protocol tie",
			[2]Destination{
				{Host: "api.example", IP: "203.0.113.1", Path: "/a", Protocol: "https", Port: 8443},
				{Host: "api.example", IP: "203.0.113.1", Path: "/a", Protocol: "https", Port: 443},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var r recorder
			for _, destination := range tc.greaterLesser {
				r.observe(
					destination.Host,
					destination.IP,
					destination.Path,
					destination.Protocol,
					destination.Port,
					nil,
				)
			}

			got := r.result(Observation{}).Destinations
			if len(got) != 2 {
				t.Fatalf("destinations = %+v, want two", got)
			}
			lesser := tc.greaterLesser[1]
			if got[0].IP != lesser.IP || got[0].Path != lesser.Path ||
				got[0].Protocol != lesser.Protocol || got[0].Port != lesser.Port {
				t.Fatalf("first destination = %+v, want the lesser of the pair %+v", got[0], lesser)
			}
		})
	}
}

// Attributions that share a host are ordered by path, so two credentials sent to
// different paths on one host sort deterministically.
func TestRecorderOrdersAttributionsByPath(t *testing.T) {
	var r recorder
	r.observe("api.example", "", "/b", "https", 443, []string{"authorization"})
	r.observe("api.example", "", "/a", "https", 443, []string{"x-api-key"})

	got := r.result(Observation{}).Attribution
	if len(got) != 2 {
		t.Fatalf("attributions = %+v, want two", got)
	}
	if got[0].Path != "/a" || got[1].Path != "/b" {
		t.Fatalf("attribution order = %q, %q, want /a, /b", got[0].Path, got[1].Path)
	}
}

// destinationHosts lists the destination hosts in the artefact's order, so a
// test can compare ordering without comparing timestamps.
func destinationHosts(obs Observation) []string {
	hosts := make([]string, 0, len(obs.Destinations))
	for _, destination := range obs.Destinations {
		hosts = append(hosts, destination.Host)
	}
	return hosts
}

// attributionHosts lists the attribution hosts in the artefact's order.
func attributionHosts(obs Observation) []string {
	hosts := make([]string, 0, len(obs.Attribution))
	for _, attribution := range obs.Attribution {
		hosts = append(hosts, attribution.Host)
	}
	return hosts
}

// withoutTimestamps zeroes the observation times so two artefacts recorded at
// different moments can be compared for everything else.
func withoutTimestamps(obs Observation) Observation {
	destinations := make([]Destination, len(obs.Destinations))
	for i, destination := range obs.Destinations {
		destination.FirstSeen = time.Time{}
		destination.LastSeen = time.Time{}
		destinations[i] = destination
	}
	obs.Destinations = destinations
	return obs
}

// jsonKeys lists the JSON keys a value serialises to, sorted.
func jsonKeys(value any) []string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		panic(err)
	}
	keys := make([]string, 0, len(decoded))
	for key := range decoded {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// deny must match a destination on its whole identity: each case varies exactly
// one identity field from the denied identity, so removing that operand from the
// match lets the deny clear the success of an entry it does not name.
func TestRecorderDenyMatchesOnlyExactIdentity(t *testing.T) {
	observed := Destination{Host: "api.example", IP: "203.0.113.7", Path: "/v1/chat", Protocol: "https", Port: 443}
	cases := []struct {
		name                     string
		host, ip, path, protocol string
		port                     uint16
	}{
		{"host differs", "other.example", observed.IP, observed.Path, observed.Protocol, observed.Port},
		{"ip differs", observed.Host, "198.51.100.2", observed.Path, observed.Protocol, observed.Port},
		{"path differs", observed.Host, observed.IP, "/v1/models", observed.Protocol, observed.Port},
		{"protocol differs", observed.Host, observed.IP, observed.Path, "http", observed.Port},
		{"port differs", observed.Host, observed.IP, observed.Path, observed.Protocol, 8443},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var r recorder
			entry := r.observe(observed.Host, observed.IP, observed.Path, observed.Protocol, observed.Port, nil)
			entry.Success = true

			r.deny(tc.host, tc.ip, tc.path, tc.protocol, tc.port)

			for _, destination := range r.result(Observation{}).Destinations {
				if destination.Host != observed.Host || destination.IP != observed.IP ||
					destination.Path != observed.Path || destination.Protocol != observed.Protocol ||
					destination.Port != observed.Port {
					continue
				}
				if destination.Denied || !destination.Success {
					t.Fatalf("a deny with a differing identity cleared the observed entry: %+v", destination)
				}
				return
			}
			t.Fatal("the observed destination is missing from the artefact")
		})
	}
}

// A destination is keyed on host, IP, port, path and protocol, so entries
// differing in any one of those stay distinct, and observe records the port it
// is handed.
func TestRecorderKeepsPortAndIdentityDistinct(t *testing.T) {
	var r recorder
	fresh := r.observe("a.example", "203.0.113.7", "/v1/chat", "https", 8443, nil)
	if fresh.Port != 8443 {
		t.Fatalf("destination port = %d, want the observed 8443", fresh.Port)
	}
	r.observe("b.example", "203.0.113.7", "/v1/chat", "https", 8443, nil)  // host differs
	r.observe("a.example", "198.51.100.2", "/v1/chat", "https", 8443, nil) // ip differs
	r.observe("a.example", "203.0.113.7", "/v1/chat", "https", 443, nil)   // port differs

	if got := len(r.result(Observation{}).Destinations); got != 4 {
		t.Fatalf("destinations = %d, want 4: host, ip, and port all identify", got)
	}
}

// Attribution is keyed on the destination's host and path: two credentials to
// one host at different paths, or to different hosts at the same path, stay
// separate entries.
func TestRecorderAttributesSeparatelyPerHostAndPath(t *testing.T) {
	var r recorder
	r.observe("a.example", "", "/v1/chat", "https", 443, []string{"authorization"})
	r.observe("b.example", "", "/v1/chat", "https", 443, []string{"cookie"})
	r.observe("a.example", "", "/v1/models", "https", 443, []string{"x-api-key"})

	if got := len(r.result(Observation{}).Attribution); got != 3 {
		t.Fatalf("attributions = %d, want 3: host and path both identify", got)
	}
}

// A later request to an already-attributed destination merges its credential
// header names into that destination's entry — even when another destination's
// entry sorts first — rather than appending a second entry for the same host and
// path.
func TestRecorderAttributeMergesIntoExistingDestination(t *testing.T) {
	var r recorder
	r.observe("a.example", "", "/v1/chat", "https", 443, []string{"authorization"})
	r.observe("b.example", "", "/v1/chat", "https", 443, []string{"cookie"})
	r.observe("b.example", "", "/v1/chat", "https", 443, []string{"x-api-key"})

	attributions := r.result(Observation{}).Attribution
	if len(attributions) != 2 {
		t.Fatalf("attributions = %+v, want two", attributions)
	}
	for _, attribution := range attributions {
		if attribution.Host != "b.example" {
			continue
		}
		if want := []string{"cookie", "x-api-key"}; !slices.Equal(attribution.HeaderNames, want) {
			t.Errorf("merged header names = %v, want %v", attribution.HeaderNames, want)
		}
		return
	}
	t.Fatal("the b.example attribution is missing")
}

// mergeHeaderNames unions the recorded and incoming names, drops an empty name a
// caller can pass, deduplicates, and sorts. The empty and duplicate cases both
// sit before a keepable name, so a premature exit drops that name.
func TestMergeHeaderNamesDeduplicatesAndDropsEmpty(t *testing.T) {
	got := mergeHeaderNames([]string{"authorization", "", "cookie"}, []string{"cookie", "x-api-key"})
	if want := []string{"authorization", "cookie", "x-api-key"}; !slices.Equal(got, want) {
		t.Fatalf("mergeHeaderNames = %v, want %v", got, want)
	}
}

// dedupeDestinations keeps the last entry of each destination identity and
// restores the original relative order, and never merges entries that differ in
// host, IP, port, path, or protocol.
func TestDedupeDestinationsKeepsLastAndFullIdentity(t *testing.T) {
	first := Destination{Host: "a.example", IP: "1.1.1.1", Path: "/a", Protocol: "https", Port: 443, Count: 1}
	last := first
	last.Count = 2
	otherHost := first
	otherHost.Host = "b.example"
	otherIP := first
	otherIP.IP = "2.2.2.2"
	otherPath := first
	otherPath.Path = "/b"
	otherProtocol := first
	otherProtocol.Protocol = "http"
	otherPort := first
	otherPort.Port = 8443

	distinct := []Destination{otherHost, otherIP, otherPath, otherProtocol, otherPort}
	got := dedupeDestinations(append(append([]Destination{}, distinct...), first, last))
	if len(got) != len(distinct)+1 {
		t.Fatalf("dedupeDestinations = %+v, want the duplicate collapsed and every distinct identity kept", got)
	}
	for i, want := range append(append([]Destination{}, distinct...), last) {
		if got[i] != want {
			t.Fatalf("dedupeDestinations[%d] = %+v, want %+v (last wins, original order)", i, got[i], want)
		}
	}
}

// dedupeAttributions keeps the last entry per host and path and restores the
// original order, and never merges entries that differ in host or path.
func TestDedupeAttributionsKeepsLastAndFullIdentity(t *testing.T) {
	first := Attribution{Host: "a.example", Path: "/a", HeaderNames: []string{"authorization"}}
	last := first
	last.HeaderNames = []string{"cookie"}
	otherHost := first
	otherHost.Host = "b.example"
	otherPath := first
	otherPath.Path = "/b"

	got := dedupeAttributions([]Attribution{otherHost, first, last, otherPath})
	if len(got) != 3 {
		t.Fatalf("dedupeAttributions = %+v, want three", got)
	}
	if !equalAttribution(got[0], otherHost) || !equalAttribution(got[1], last) || !equalAttribution(got[2], otherPath) {
		t.Fatalf("dedupeAttributions = %+v, want [%+v %+v %+v]", got, otherHost, last, otherPath)
	}
}

// Attributions with different hosts order by host, not by path.
func TestRecorderOrdersAttributionsByHost(t *testing.T) {
	var r recorder
	r.observe("z.example", "", "/a", "https", 443, []string{"authorization"})
	r.observe("a.example", "", "/z", "https", 443, []string{"x-api-key"})

	got := r.result(Observation{}).Attribution
	if len(got) != 2 || got[0].Host != "a.example" || got[1].Host != "z.example" {
		t.Fatalf("attribution order = %+v, want a.example then z.example", got)
	}
}

// result deduplicates the attribution list it returns: an artefact given with
// two entries for one host and path collapses to the last one, so a read-back
// artefact and the proxy's own capture do not double-record a destination.
func TestRecorderResultDedupesGivenAttributions(t *testing.T) {
	var r recorder
	given := Observation{Attribution: []Attribution{
		{Host: "a.example", Path: "/a", HeaderNames: []string{"authorization"}},
		{Host: "a.example", Path: "/a", HeaderNames: []string{"cookie"}},
	}}

	got := r.result(given).Attribution
	if len(got) != 1 {
		t.Fatalf("attribution = %+v, want one after dedupe", got)
	}
	if want := []string{"cookie"}; !slices.Equal(got[0].HeaderNames, want) {
		t.Errorf("header names = %v, want the last entry %v", got[0].HeaderNames, want)
	}
}
