package channel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
)

// secretValue is a stand-in for a credential that crossed the channel. The
// artefact must never contain it: the assertion that the saved bytes do not
// mention it is the value-free guarantee (hub invariant 3).
const secretValue = "sk-live-do-not-persist-0123456789"

// requestBody is a stand-in for a request body. Like a header value, a body is
// never part of the artefact, so the saved bytes must not contain it.
const requestBody = "grant_type=refresh_token&token=sensitive-body"

// sampleObservation is a fully populated artefact: one HTTPS destination with a
// path and one credential attribution carrying header NAMES only.
func sampleObservation() Observation {
	seen := time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC)
	return Observation{
		Destinations: []Destination{{
			Host:      "api.example.com",
			IP:        "",
			Port:      443,
			Protocol:  "https",
			Path:      "/v1/tokens",
			Success:   true,
			Denied:    false,
			Count:     3,
			FirstSeen: seen,
			LastSeen:  seen.Add(2 * time.Minute),
			Channel:   observedViaProxy,
		}},
		Attribution: []Attribution{{
			Host:           "api.example.com",
			Path:           "/v1/tokens",
			Credential:     "EXAMPLE_API_TOKEN",
			HeaderNames:    []string{"Authorization"},
			SawReal:        true,
			SawPlaceholder: false,
		}},
	}
}

// Save then Load round-trips the artefact through the file the run leaves
// behind. The write lands under the state dir's learn/<checkoutSlug>/ tree,
// keyed by the session's start time, and the loaded value equals what was saved.
func TestArtefactStoreRoundTrips(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	obs := sampleObservation()

	path, err := Save("my-checkout", 1763458200, obs)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	want := filepath.Join(configpaths.Get().UserStateDir(), "learn", "my-checkout", "1763458200.json")
	if path != want {
		t.Errorf("Save path = %q, want %q", path, want)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertObservationEqual(t, obs, loaded)
}

// The artefact is written 0600: it is state, not shared, so no group or other
// read bit may be set.
func TestArtefactFileMode(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	path, err := Save("my-checkout", 1763458200, sampleObservation())
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("artefact mode = %o, want 600", perm)
	}
}

// The artefact is value-free: a credential value and a request body that
// crossed the session never appear in the persisted bytes (hub invariant 3).
//
// The observation is built through the recorder, which is handed the request's
// header NAMES and nothing else, so the in-flight value and body exist only on
// the transport side. The persisted key inventory is asserted too: a field that
// could carry a value must not appear, so the grep for the value is not the
// only guard.
func TestArtefactCarriesNoValues(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	var r recorder
	// The recorder receives the header name only; the value and the body stay on
	// the transport side and are never arguments here.
	r.observe("api.example.com", "", "/v1/tokens", "https", []string{"Authorization"})
	obs := r.result(Observation{})

	path, err := Save("my-checkout", 1763458200, obs)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	data := string(raw)

	for name, value := range map[string]string{
		"credential value": secretValue,
		"request body":     requestBody,
	} {
		if strings.Contains(data, value) {
			t.Errorf("artefact contains the %s %q; it must never be persisted", name, value)
		}
	}

	// The header NAME is what the artefact keeps, so the value-free claim is not
	// vacuous: the name is present.
	if !strings.Contains(data, "Authorization") {
		t.Error("artefact omits the credential-bearing header name, so the value-free assertion proves nothing")
	}

	assertArtefactKeys(t, obs)
}

// assertArtefactKeys re-encodes the observation and walks the JSON for keys
// outside the value-free contract, so a new value-bearing field fails here
// rather than leaking silently into the persisted artefact.
func assertArtefactKeys(t *testing.T, obs Observation) {
	t.Helper()

	encoded, err := json.Marshal(obs)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	allowed := map[string]bool{
		"destinations": true, "attribution": true,
		"host": true, "ip": true, "port": true, "protocol": true, "path": true,
		"success": true, "denied": true, "count": true, "first_seen": true,
		"last_seen": true, "channel": true,
		"credential": true, "header_names": true, "saw_real": true,
		"saw_placeholder": true,
	}

	var document any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, key := range collectKeys(document) {
		if !allowed[key] {
			t.Errorf("artefact key %q is outside the value-free contract", key)
		}
	}
}

// collectKeys gathers every object key in a decoded JSON document.
func collectKeys(node any) []string {
	var keys []string
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			keys = append(keys, key)
			keys = append(keys, collectKeys(child)...)
		}
	case []any:
		for _, child := range value {
			keys = append(keys, collectKeys(child)...)
		}
	}
	return keys
}

// Load of a path that was never written errors rather than returning an empty
// artefact, so a caller cannot mistake a missing capture for an empty session.
func TestArtefactLoadMissingFile(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	if _, err := Load(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Error("Load of a missing file returned no error")
	}
}

// Load of a malformed file errors rather than silently yielding a partial
// artefact.
func TestArtefactLoadMalformedFile(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Error("Load of a malformed file returned no error")
	}
}

// Prune removes artefacts modified before the cutoff, keeps recent ones, and
// removes the slug directory once it is empty.
func TestArtefactPruneCutoff(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	oldPath, err := Save("prune-me", 1763000000, sampleObservation())
	if err != nil {
		t.Fatalf("Save old: %v", err)
	}
	recentPath, err := Save("prune-me", 1763458200, sampleObservation())
	if err != nil {
		t.Fatalf("Save recent: %v", err)
	}

	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(oldPath, past, past); err != nil {
		t.Fatalf("Chtimes old: %v", err)
	}

	cutoff := time.Now().Add(-time.Minute)
	removed, err := Prune(cutoff)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 1 {
		t.Errorf("Prune removed %d artefacts, want 1", removed)
	}

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Errorf("old artefact still present after Prune (err = %v)", err)
	}
	if _, err := os.Stat(recentPath); err != nil {
		t.Errorf("recent artefact removed by Prune: %v", err)
	}
}

// Prune removes the slug directory once its last artefact is gone, so a
// completed sweep leaves no empty tree behind, and Prune without a learn tree
// is a no-op rather than an error.
func TestArtefactPruneRemovesEmptySlugDir(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	path, err := Save("prune-me", 1763000000, sampleObservation())
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	removed, err := Prune(time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 1 {
		t.Errorf("Prune removed %d artefacts, want 1", removed)
	}

	if _, err := os.Stat(filepath.Join(configpaths.Get().UserStateDir(), "learn", "prune-me")); !os.IsNotExist(err) {
		t.Errorf("slug directory still present after Prune (err = %v)", err)
	}

	// A second Prune with no learn tree left is a no-op, not an error.
	removed, err = Prune(time.Now())
	if err != nil {
		t.Fatalf("Prune with no learn tree: %v", err)
	}
	if removed != 0 {
		t.Errorf("Prune with no learn tree removed %d artefacts, want 0", removed)
	}
}

// assertObservationEqual compares two artefacts field by field so a failure
// names the field that diverged.
func assertObservationEqual(t *testing.T, want, got Observation) {
	t.Helper()

	if len(got.Destinations) != len(want.Destinations) {
		t.Fatalf("destinations = %d, want %d", len(got.Destinations), len(want.Destinations))
	}
	for i := range want.Destinations {
		if got.Destinations[i] != want.Destinations[i] {
			t.Errorf("destination %d = %+v, want %+v", i, got.Destinations[i], want.Destinations[i])
		}
	}

	if len(got.Attribution) != len(want.Attribution) {
		t.Fatalf("attribution = %d, want %d", len(got.Attribution), len(want.Attribution))
	}
	for i := range want.Attribution {
		if !equalAttribution(got.Attribution[i], want.Attribution[i]) {
			t.Errorf("attribution %d = %+v, want %+v", i, got.Attribution[i], want.Attribution[i])
		}
	}
}

func equalAttribution(a, b Attribution) bool {
	if a.Host != b.Host || a.Path != b.Path || a.Credential != b.Credential ||
		a.SawReal != b.SawReal || a.SawPlaceholder != b.SawPlaceholder {
		return false
	}
	if len(a.HeaderNames) != len(b.HeaderNames) {
		return false
	}
	for i := range a.HeaderNames {
		if a.HeaderNames[i] != b.HeaderNames[i] {
			return false
		}
	}
	return true
}
