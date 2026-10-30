package upgrade

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

func TestMemoryStoreRoundTrips(t *testing.T) {
	var s memoryStore
	got, err := s.Load()
	if err != nil || !got.LastChecked.IsZero() || len(got.DismissedVersions) != 0 {
		t.Fatalf("empty load = %+v, %v", got, err)
	}
	want := LauncherState{DismissedVersions: []string{"2.0.0"}}
	if err := s.Update(func(st *LauncherState) { *st = want }); err != nil {
		t.Fatal(err)
	}
	got, err = s.Load()
	if err != nil || len(got.DismissedVersions) != 1 || got.DismissedVersions[0] != "2.0.0" {
		t.Fatalf("round-trip = %+v, %v", got, err)
	}
}

// writeLegacy writes the retired upgrade.json at the legacy path, creating the
// user state directory first.
func writeLegacy(t *testing.T, body string) {
	t.Helper()
	if err := os.MkdirAll(configpaths.Get().UserStateDir(), 0o700); err != nil {
		t.Fatalf("create user state dir: %v", err)
	}
	if err := os.WriteFile(legacyStatePath(), []byte(body), 0o600); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}
}

func TestFoldLegacyStateCopiesAndRemoves(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeLegacy(t, `{"last_check":"2026-09-01T00:00:00Z","dismissed_versions":["1.5.2"]}`)
	s := &fakeStore{}
	if err := FoldLegacyState(s); err != nil {
		t.Fatal(err)
	}
	if s.state.LastChecked.IsZero() || len(s.state.DismissedVersions) != 1 {
		t.Fatalf("fold = %+v", s.state)
	}
	if _, err := os.Stat(legacyStatePath()); !os.IsNotExist(err) {
		t.Fatalf("legacy file not removed: %v", err)
	}
}

func TestFoldLegacyStateUnionsDismissalsAndKeepsPresentLastChecked(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeLegacy(t, `{"last_check":"2026-09-01T00:00:00Z","dismissed_versions":["1.5.2"]}`)
	present := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	s := &fakeStore{state: LauncherState{LastChecked: present, DismissedVersions: []string{"2.0.0"}}}
	if err := FoldLegacyState(s); err != nil {
		t.Fatal(err)
	}
	if !s.state.LastChecked.Equal(present) {
		t.Fatalf("present last_checked overridden: %v", s.state.LastChecked)
	}
	if len(s.state.DismissedVersions) != 2 {
		t.Fatalf("dismissals not unioned: %+v", s.state.DismissedVersions)
	}
}

func TestFoldLegacyStateCorruptRemovedWithoutFolding(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeLegacy(t, "{not json")
	s := &fakeStore{}
	if err := FoldLegacyState(s); err != nil {
		t.Fatal(err)
	}
	if !s.state.LastChecked.IsZero() || len(s.state.DismissedVersions) != 0 {
		t.Fatalf("corrupt file folded something: %+v", s.state)
	}
	if _, err := os.Stat(legacyStatePath()); !os.IsNotExist(err) {
		t.Fatalf("corrupt legacy file not removed: %v", err)
	}
}

func TestFoldLegacyStateKeepsFileOnWriteFailure(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeLegacy(t, `{"dismissed_versions":["1.5.2"]}`)
	s := &writeFailingStore{inner: &fakeStore{}}
	if err := FoldLegacyState(s); err == nil {
		t.Fatal("expected the write failure to surface")
	}
	if _, err := os.Stat(legacyStatePath()); err != nil {
		t.Fatalf("legacy file removed despite write failure: %v", err)
	}
}

// interleavingStore simulates a concurrent launcher writing between the fold's
// read and its write: Load hands back the pre-race snapshot, Update applies the
// mutation after the race. The fold must use Update, so the merge sees the
// concurrent write; a read-then-write fold would save its stale snapshot over it.
type interleavingStore struct {
	state LauncherState
	raced bool
}

func (s *interleavingStore) injectRace() {
	if s.raced {
		return
	}
	s.raced = true
	s.state.DismissedVersions = append(s.state.DismissedVersions, "9.9.9")
}

func (s *interleavingStore) Load() (LauncherState, error) {
	snapshot := s.state
	s.injectRace()
	return snapshot, nil
}

func (s *interleavingStore) Update(mutate func(*LauncherState)) error {
	s.injectRace()
	mutate(&s.state)
	return nil
}

func TestFoldLegacyStateDoesNotClobberAConcurrentDismissal(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeLegacy(t, `{"dismissed_versions":["1.5.2"]}`)
	s := &interleavingStore{}
	if err := FoldLegacyState(s); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(s.state.DismissedVersions, "9.9.9") {
		t.Fatalf("fold clobbered a concurrent dismissal: %+v", s.state.DismissedVersions)
	}
	if !slices.Contains(s.state.DismissedVersions, "1.5.2") {
		t.Fatalf("fold dropped the legacy dismissal: %+v", s.state.DismissedVersions)
	}
}

func TestFoldLegacyStateUnreadableFileIsRetried(t *testing.T) {
	// A read error other than "does not exist" leaves the file in place so a
	// later check retries the fold.
	configpaths.WithMockConfigPaths(t)
	if err := os.Mkdir(legacyStatePath(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := FoldLegacyState(&fakeStore{}); err == nil {
		t.Fatal("expected a read error to surface")
	}
}

// storeNotTouchedStore fails the test if Load or Update is called.
type storeNotTouchedStore struct{ t *testing.T }

func (s *storeNotTouchedStore) Load() (LauncherState, error) {
	s.t.Fatal("Load must not be called")
	return LauncherState{}, nil
}

func (s *storeNotTouchedStore) Update(func(*LauncherState)) error {
	s.t.Fatal("Update must not be called")
	return nil
}

func TestFoldLegacyStateMissingFileLeavesStoreUntouched(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	_ = os.Remove(legacyStatePath())
	if err := FoldLegacyState(&storeNotTouchedStore{t: t}); err != nil {
		t.Fatalf("missing file must be a no-op, got %v", err)
	}
}

func TestLegacyStatePathLivesInUserStateDir(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	want := filepath.Join(configpaths.Get().UserStateDir(), "upgrade.json")
	if got := legacyStatePath(); got != want {
		t.Fatalf("legacyStatePath() = %q, want %q", got, want)
	}
}
