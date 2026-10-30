package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/upgrade"
)

func TestLauncherStoreRoundTripsThroughRootState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	s := launcherStore{}
	if got, err := s.Load(); err != nil || !got.LastChecked.IsZero() {
		t.Fatalf("empty load = %+v, %v", got, err)
	}
	// Seed an unrelated migrations entry so the round-trip also proves a
	// launcherStore.Update preserves the rest of the root document.
	if _, err := state.WithRootState(true, func(tx *state.RootTxn) error {
		tx.State().SetMigration("runtime_home", state.Migration{Status: state.MigrationCompleted})
		return tx.Commit()
	}); err != nil {
		t.Fatal(err)
	}

	checked := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	want := upgrade.LauncherState{LastChecked: checked, DismissedVersions: []string{"2.0.0"}}
	if err := s.Update(func(st *upgrade.LauncherState) { *st = want }); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastChecked.Equal(checked) {
		t.Fatalf("LastChecked round-trip = %v, want %v", got.LastChecked, checked)
	}
	if len(got.DismissedVersions) != 1 || got.DismissedVersions[0] != "2.0.0" {
		t.Fatalf("round-trip = %+v", got)
	}
	root, err := state.ReadRootState()
	if err != nil {
		t.Fatal(err)
	}
	if !root.Updates.Launcher.LastChecked.Equal(checked) {
		t.Fatalf("not persisted under updates.launcher: %+v", root.Updates)
	}
	if m, ok := root.Migration("runtime_home"); !ok || m.Status != state.MigrationCompleted {
		t.Fatalf("launcherStore.Update dropped the migrations entry: %+v", root.Migrations)
	}
}

// A contended root lock must surface as errRootStateLocked from both Load and
// Update: the non-blocking acquire returns ok=false rather than an error, and the
// adapter turns that into an error so upgrade.Check defers instead of reading a
// false empty state or clobbering a concurrent writer.
func TestLauncherStoreReportsContendedRootLock(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ran := false
	_, _ = state.WithRootState(true, func(_ *state.RootTxn) error {
		ran = true
		if _, err := (launcherStore{}).Load(); !errors.Is(err, errRootStateLocked) {
			t.Errorf("Load under held lock = %v, want errRootStateLocked", err)
		}
		err := (launcherStore{}).Update(func(st *upgrade.LauncherState) {
			st.DismissedVersions = []string{"2.0.0"}
		})
		if !errors.Is(err, errRootStateLocked) {
			t.Errorf("Update under held lock = %v, want errRootStateLocked", err)
		}
		return errors.New("closure ran")
	})
	if !ran {
		t.Fatal("blocking root transaction did not run")
	}
}

// FoldLegacyState must leave upgrade.json in place when the root lock is held,
// so the next run retries the fold instead of losing the dismissals.
func TestFoldLegacyStateDefersOnContendedRootLock(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacyPath := filepath.Join(configpaths.Get().UserStateDir(), "upgrade.json")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte(`{"dismissed_versions":["1.5.2"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ran := false
	_, _ = state.WithRootState(true, func(_ *state.RootTxn) error {
		ran = true
		if err := upgrade.FoldLegacyState(launcherStore{}); !errors.Is(err, errRootStateLocked) {
			t.Errorf("FoldLegacyState under held lock = %v, want errRootStateLocked", err)
		}
		return errors.New("closure ran")
	})
	if !ran {
		t.Fatal("blocking root transaction did not run")
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("legacy upgrade.json removed while the lock was held: %v", err)
	}
}

// A corrupt root document must surface as an error rather than read as an empty
// state: upgrade.Check then defers the check and the legacy-file fold keeps its
// file, instead of overwriting the unreadable document with a zero one.
func TestLauncherStoreErrorsOnCorruptRootState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	statePath := filepath.Join(configpaths.Get().UserStateDir(), "state.yaml")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte("migrations: [not-a-map\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := launcherStore{}
	if _, err := s.Load(); err == nil {
		t.Fatal("expected Load to fail on a corrupt root document")
	}
	err := s.Update(func(st *upgrade.LauncherState) { st.DismissedVersions = []string{"2.0.0"} })
	if err == nil {
		t.Fatal("expected Update to fail on a corrupt root document")
	}
	remaining, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(remaining), "not-a-map") {
		t.Fatalf("failed Update clobbered the corrupt document: %q", remaining)
	}
}
