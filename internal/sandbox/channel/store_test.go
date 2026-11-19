package channel

import (
	"encoding/json"
	"errors"
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

// The learn tree is written 0700: the captures are the user's private state, so
// no group or other access may be set on the directory either.
func TestArtefactDirMode(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	path, err := Save("my-checkout", 1763458200, sampleObservation())
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("learn dir mode = %o, want 700", perm)
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
	r.observe("api.example.com", "", "/v1/tokens", "https", 443, []string{"Authorization"})
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

	if _, err := Load(filepath.Join(t.TempDir(), "absent.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Load of a missing file: err = %v, want a wrapped os.ErrNotExist", err)
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

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load of a malformed file returned no error")
	}
	assertWrapsCause(t, err)
}

// Delete removes one saved artefact, and a missing path is not an error: the
// after-run deletes its own artefact, and a replay may find it already gone.
func TestArtefactDelete(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	path, err := Save("delete-me", 1763458200, sampleObservation())
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := Delete(path); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("artefact still present after Delete (err = %v)", err)
	}
	if err := Delete(path); err != nil {
		t.Errorf("Delete of a missing artefact = %v, want nil", err)
	}
}

// Delete reports a path it cannot remove that is not simply missing, rather than
// silently treating a failed removal as done. A non-empty directory cannot be
// removed by os.Remove.
func TestArtefactDeleteFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := Delete(dir)
	if err == nil {
		t.Fatal("Delete of a non-empty directory returned no error")
	}
	if !strings.Contains(err.Error(), "remove observation") {
		t.Errorf("Delete error = %q, want it to name the removal", err)
	}
	assertWrapsCause(t, err)
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

// relativeStateConfigPaths is a ConfigPaths whose state dir is relative. It
// embeds the interface so only UserStateDir is implemented; the store reads no
// other method.
type relativeStateConfigPaths struct {
	configpaths.ConfigPaths
}

func (relativeStateConfigPaths) UserStateDir() string { return "relative-user-state" }

// A relative user state dir is refused rather than silently writing the capture
// into the process's working directory.
func TestArtefactStoreRefusesRelativeStateDir(t *testing.T) {
	orig := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths { return relativeStateConfigPaths{} }
	t.Cleanup(func() { configpaths.Get = orig })

	if _, err := Save("my-checkout", 1763458200, sampleObservation()); err == nil {
		t.Error("Save with a relative state dir returned no error")
	}
	if removed, err := Prune(time.Now()); err == nil || removed != 0 {
		t.Errorf("Prune with a relative state dir = (%d, %v), want (0, error)", removed, err)
	}
}

// fixedStateConfigPaths is a ConfigPaths whose state dir is a fixed absolute
// path. It embeds the interface so only UserStateDir is implemented; the store
// reads no other method.
type fixedStateConfigPaths struct {
	configpaths.ConfigPaths

	stateDir string
}

func (f fixedStateConfigPaths) UserStateDir() string { return f.stateDir }

// withStateDir points the store at an arbitrary absolute state dir, so a test
// can build the failure shape (a file where a directory must be, a mode that
// forbids a write) that a real user state dir would not have.
func withStateDir(t *testing.T, stateDir string) {
	t.Helper()

	orig := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths { return fixedStateConfigPaths{stateDir: stateDir} }
	t.Cleanup(func() { configpaths.Get = orig })
}

// chmodAt changes path's mode for the test and restores a traversable, writable
// mode on cleanup, so the temp tree can still be removed.
func chmodAt(t *testing.T, path string, mode os.FileMode) {
	t.Helper()

	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o700) })
}

// Save reports the directory it cannot create: a state dir under a regular file
// cannot hold the learn tree.
func TestArtefactStoreSaveMkdirFailure(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "state-is-a-file")
	if err := os.WriteFile(stateFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	withStateDir(t, stateFile)

	_, err := Save("my-checkout", 1763458200, sampleObservation())
	if err == nil {
		t.Fatal("Save with a state dir under a regular file returned no error")
	}
	if !strings.Contains(err.Error(), "create learn artefact directory") {
		t.Errorf("Save mkdir error = %q, want it to name the directory it could not create", err)
	}
	assertWrapsCause(t, err)
}

// Save reports an artefact it cannot encode: a timestamp outside the JSON range
// fails the marshal before any file is written.
func TestArtefactStoreSaveMarshalFailure(t *testing.T) {
	withStateDir(t, t.TempDir())

	obs := Observation{Destinations: []Destination{{
		Host:      "api.example.com",
		FirstSeen: time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC),
	}}}
	_, err := Save("my-checkout", 1763458200, obs)
	if err == nil {
		t.Fatal("Save with an out-of-range timestamp returned no error")
	}
	assertWrapsCause(t, err)
}

// Save reports a write it cannot complete: the atomic write renames onto the
// artefact path, which fails when a directory already occupies it.
func TestArtefactStoreSaveWriteFailure(t *testing.T) {
	stateDir := t.TempDir()
	withStateDir(t, stateDir)

	dir := filepath.Join(stateDir, learnStateDirName, "my-checkout")
	if err := os.MkdirAll(dir, artefactDirMode); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "1763458200.json"), artefactDirMode); err != nil {
		t.Fatalf("Mkdir target: %v", err)
	}

	_, err := Save("my-checkout", 1763458200, sampleObservation())
	if err == nil {
		t.Fatal("Save with a directory in place of the artefact file returned no error")
	}
	assertWrapsCause(t, err)
}

// Prune tolerates a learn tree that does not exist: nothing to prune is the same
// outcome as having pruned everything.
func TestArtefactPruneWithoutLearnTree(t *testing.T) {
	withStateDir(t, t.TempDir())

	removed, err := Prune(time.Now())
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 0 {
		t.Errorf("Prune removed %d artefacts, want 0", removed)
	}
}

// Prune reports a learn path it cannot read that is not simply missing.
func TestArtefactPruneReadDirFailure(t *testing.T) {
	stateDir := t.TempDir()
	withStateDir(t, stateDir)
	if err := os.WriteFile(filepath.Join(stateDir, learnStateDirName), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	removed, err := Prune(time.Now())
	if err == nil {
		t.Fatal("Prune with a regular file at the learn dir returned no error")
	}
	if removed != 0 {
		t.Errorf("Prune returned %d removed, want 0 when it cannot read the learn tree", removed)
	}
	assertWrapsCause(t, err)
}

// Prune skips an entry under the learn tree that is not a slug directory.
func TestArtefactPruneSkipsNonDirectoryEntries(t *testing.T) {
	stateDir := t.TempDir()
	withStateDir(t, stateDir)
	learnDir := filepath.Join(stateDir, learnStateDirName)
	if err := os.MkdirAll(learnDir, artefactDirMode); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(learnDir, "stray-file"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	removed, err := Prune(time.Now())
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 0 {
		t.Errorf("Prune removed %d artefacts, want 0", removed)
	}
}

// Prune propagates a slug directory it cannot read, rather than silently
// treating the unreadable tree as pruned.
func TestArtefactPrunePropagatesSlugDirFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 0 does not block a directory read")
	}
	stateDir := t.TempDir()
	withStateDir(t, stateDir)
	slugDir := filepath.Join(stateDir, learnStateDirName, "my-checkout")
	if err := os.MkdirAll(slugDir, artefactDirMode); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	chmodAt(t, slugDir, 0o000)

	if _, err := Prune(time.Now()); err == nil {
		t.Error("Prune with an unreadable slug directory returned no error")
	} else {
		assertWrapsCause(t, err)
	}
}

// pruneSlugDir skips a nested directory: only artefact files are removed.
func TestPruneSlugDirSkipsSubdirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "nested"), artefactDirMode); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	removed, err := pruneSlugDir(dir, time.Now())
	if err != nil {
		t.Fatalf("pruneSlugDir: %v", err)
	}
	if removed != 0 {
		t.Errorf("pruneSlugDir removed %d entries, want 0", removed)
	}
}

// pruneSlugDir reports a directory it cannot read and removes nothing, so the
// caller's count does not advance on a failure.
func TestPruneSlugDirReadFailureReturnsZero(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 0 does not block a directory read")
	}
	dir := t.TempDir()
	chmodAt(t, dir, 0o000)

	removed, err := pruneSlugDir(dir, time.Now())
	if err == nil {
		t.Fatal("pruneSlugDir with an unreadable directory returned no error")
	}
	if removed != 0 {
		t.Errorf("pruneSlugDir removed %d, want 0 when it cannot read the directory", removed)
	}
	assertWrapsCause(t, err)
}

// pruneSlugDir reports an artefact it cannot stat, so an unreadable entry fails
// the sweep rather than being reported as pruned.
func TestPruneSlugDirStatFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a mode without execute does not block stat")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "artefact.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	chmodAt(t, dir, 0o400)

	if _, err := pruneSlugDir(dir, time.Now()); err == nil {
		t.Error("pruneSlugDir with an unstattable artefact returned no error")
	} else {
		assertWrapsCause(t, err)
	}
}

// pruneSlugDir reports an artefact it cannot remove, so a read-only tree fails
// the sweep rather than leaving the caller believing the artefact is gone.
func TestPruneSlugDirRemoveFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory does not block removal")
	}
	dir := t.TempDir()
	artefact := filepath.Join(dir, "artefact.json")
	if err := os.WriteFile(artefact, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(artefact, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	chmodAt(t, dir, 0o500)

	if _, err := pruneSlugDir(dir, time.Now()); err == nil {
		t.Error("pruneSlugDir with a non-removable artefact returned no error")
	} else {
		assertWrapsCause(t, err)
	}
}

// Prune skips a stray non-directory entry and keeps walking: a stray file that
// sorts before a slug directory must not stop the sweep from pruning that
// directory.
func TestArtefactPruneSkipsStrayFileBeforeSlugDir(t *testing.T) {
	stateDir := t.TempDir()
	withStateDir(t, stateDir)
	learnDir := filepath.Join(stateDir, learnStateDirName)
	if err := os.MkdirAll(learnDir, artefactDirMode); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(learnDir, "0-stray"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile stray: %v", err)
	}
	artefact := filepath.Join(learnDir, "z-slug", "1.json")
	if err := os.MkdirAll(filepath.Dir(artefact), artefactDirMode); err != nil {
		t.Fatalf("MkdirAll slug: %v", err)
	}
	if err := os.WriteFile(artefact, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile artefact: %v", err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(artefact, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	removed, err := Prune(time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 1 {
		t.Errorf("Prune removed %d, want 1: a stray file must not stop the sweep", removed)
	}
}

// Prune's count accumulates across slug directories, so the caller learns how
// many artefacts a sweep removed in total.
func TestArtefactPruneAccumulatesAcrossSlugDirs(t *testing.T) {
	stateDir := t.TempDir()
	withStateDir(t, stateDir)
	for _, slug := range []string{"a-slug", "b-slug"} {
		path := filepath.Join(stateDir, learnStateDirName, slug, "1.json")
		if err := os.MkdirAll(filepath.Dir(path), artefactDirMode); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		old := time.Now().Add(-time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}
	}

	removed, err := Prune(time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 2 {
		t.Errorf("Prune removed %d, want 2 (one per slug directory)", removed)
	}
}

// Prune reports the artefacts it removed before a later slug directory failed,
// so the caller does not lose the count it already accounted for.
func TestArtefactPruneReportsAccumulatedCountOnSlugFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 0 does not block a directory read")
	}
	stateDir := t.TempDir()
	withStateDir(t, stateDir)
	readable := filepath.Join(stateDir, learnStateDirName, "a-slug", "1.json")
	if err := os.MkdirAll(filepath.Dir(readable), artefactDirMode); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(readable, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(readable, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	unreadable := filepath.Join(stateDir, learnStateDirName, "b-slug")
	if err := os.MkdirAll(unreadable, artefactDirMode); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	chmodAt(t, unreadable, 0o000)

	removed, err := Prune(time.Now().Add(-time.Minute))
	if err == nil {
		t.Fatal("Prune with an unreadable slug directory returned no error")
	}
	if removed != 1 {
		t.Errorf("Prune removed %d before the failure, want 1", removed)
	}
}

// pruneSlugDir skips a nested directory and keeps walking: a nested directory
// that sorts before an old artefact must not stop the artefact's removal.
func TestPruneSlugDirSkipsNestedDirBeforeArtefact(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "a-nested"), artefactDirMode); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	artefact := filepath.Join(dir, "z.json")
	if err := os.WriteFile(artefact, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(artefact, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	removed, err := pruneSlugDir(dir, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("pruneSlugDir: %v", err)
	}
	if removed != 1 {
		t.Errorf("pruneSlugDir removed %d, want 1: a nested directory must not stop the sweep", removed)
	}
}

// pruneSlugDir skips a recent artefact and keeps walking: a recent entry that
// sorts before an old one must not stop the old one's removal.
func TestPruneSlugDirKeepsRecentBeforeOlder(t *testing.T) {
	dir := t.TempDir()
	recent := filepath.Join(dir, "a-recent.json")
	old := filepath.Join(dir, "z-old.json")
	for _, path := range []string{recent, old} {
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	removed, err := pruneSlugDir(dir, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("pruneSlugDir: %v", err)
	}
	if removed != 1 {
		t.Errorf("pruneSlugDir removed %d, want 1: a recent entry must not stop the sweep", removed)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Errorf("the recent artefact was removed: %v", err)
	}
}

// assertWrapsCause pins that an error preserves its cause: formatting a wrapped
// error with %v drops the chain, so errors.Unwrap would yield nil for it.
func assertWrapsCause(t *testing.T, err error) {
	t.Helper()

	if errors.Unwrap(err) == nil {
		t.Errorf("error %v does not wrap its cause (formatted with %%v, not %%w)", err)
	}
}
