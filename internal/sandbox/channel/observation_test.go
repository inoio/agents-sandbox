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
	first := r.observe("api.example", "203.0.113.7", "/v1/chat", "https", []string{"authorization"})
	second := r.observe("api.example", "203.0.113.7", "/v1/chat", "https", []string{"authorization"})

	if first != second {
		t.Fatal("the same destination must aggregate onto one entry")
	}
	if got := len(r.obs.Destinations); got != 1 {
		t.Fatalf("destinations = %d, want 1", got)
	}
	destination := r.obs.Destinations[0]
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

func TestRecorderKeepsDestinationsDistinct(t *testing.T) {
	var r recorder
	r.observe("api.example", "203.0.113.7", "/v1/chat", "https", nil)
	r.observe("api.example", "203.0.113.7", "/v1/models", "https", nil)
	r.observe("api.example", "203.0.113.7", "/v1/chat", "http", nil)

	if got := len(r.obs.Destinations); got != 3 {
		t.Fatalf("destinations = %d, want 3 (path and protocol both identify)", got)
	}
}

func TestRecorderRecordsCredentialBearingHeaderNames(t *testing.T) {
	var r recorder
	r.observe("api.example", "203.0.113.7", "/v1/chat", "https", []string{"x-api-key", "authorization"})
	r.observe("api.example", "203.0.113.7", "/v1/chat", "https", []string{"authorization"})

	if got := len(r.obs.Attribution); got != 1 {
		t.Fatalf("attributions = %d, want 1", got)
	}
	attribution := r.obs.Attribution[0]
	if attribution.Host != "api.example" || attribution.Path != "/v1/chat" {
		t.Fatalf("attribution names the wrong destination: %+v", attribution)
	}
	if want := []string{"authorization", "x-api-key"}; !slices.Equal(attribution.HeaderNames, want) {
		t.Fatalf("header names = %v, want %v (sorted, deduplicated)", attribution.HeaderNames, want)
	}
}

func TestRecorderRecordsNoAttributionWithoutCredentialHeader(t *testing.T) {
	var r recorder
	r.observe("api.example", "203.0.113.7", "/v1/chat", "https", nil)

	if got := len(r.obs.Attribution); got != 0 {
		t.Fatalf("attributions = %d, want 0: a request with no credential header is unattributed", got)
	}
}

func TestRecorderDenyMarksDestinationWithoutAllow(t *testing.T) {
	var r recorder
	destination := r.observe("blocked.example", "198.51.100.4", "/v1/chat", "https", []string{"authorization"})
	destination.Success = true

	r.deny("blocked.example", "198.51.100.4", "/v1/chat")

	if len(r.obs.Destinations) != 1 {
		t.Fatalf("destinations = %d, want 1", len(r.obs.Destinations))
	}
	got := r.obs.Destinations[0]
	if !got.Denied {
		t.Fatal("deny must mark the destination denied")
	}
	if got.Success {
		t.Fatal("a denied destination must not be marked successful")
	}
}

func TestRecorderDenyRecordsUnobservedDestination(t *testing.T) {
	var r recorder
	r.deny("blocked.example", "198.51.100.4", "/v1/chat")

	if len(r.obs.Destinations) != 1 {
		t.Fatalf("destinations = %d, want 1", len(r.obs.Destinations))
	}
	if !r.obs.Destinations[0].Denied {
		t.Fatal("a denial before any observed request must still be recorded")
	}
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
	r.observe("api.example", "203.0.113.7", "/v1/chat", "https", []string{"authorization"})

	given := Observation{Destinations: []Destination{{Host: "config.example", Path: "/", Channel: "readback"}}}
	got := r.result(given)

	if want := []string{"api.example", "config.example"}; !slices.Equal(destinationHosts(got), want) {
		t.Fatalf("hosts = %v, want %v", destinationHosts(got), want)
	}
	if merged := r.result(r.obs); len(merged.Destinations) != 1 {
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
