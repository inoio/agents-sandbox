package upgrade

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

// legacyStateFileName is the retired per-launcher upgrade state file, replaced
// by the launcher-global state document.
const legacyStateFileName = "upgrade.json"

// LauncherState is the launcher's own upgrade-check state: when the GitHub
// release check last ran and which newer versions the user asked not to be
// notified about again.
type LauncherState struct {
	LastChecked       time.Time
	DismissedVersions []string
}

// Store reads and mutates LauncherState. The production implementation is
// cmd/agents-sandbox's launcherStore, which keeps the state in the
// launcher-global state.yaml document (updates.launcher); a nil Store falls
// back to memoryStore, which checks normally but persists nothing.
//
// Update is the only mutation. Load followed by a write is two transactions, so
// a value derived from the loaded snapshot can land after a concurrent launcher
// already wrote newer state and clobber it. Update applies its mutation to the
// state as it is at write time, inside one locked transaction.
type Store interface {
	Load() (LauncherState, error)
	Update(mutate func(*LauncherState)) error
}

// memoryStore is the default Store for a caller that injects none. It keeps
// state in memory, so an unconfigured check behaves identically within one
// process and leaves no trace behind.
type memoryStore struct {
	state LauncherState
}

func (m *memoryStore) Load() (LauncherState, error) { return m.state, nil }

func (m *memoryStore) Update(mutate func(*LauncherState)) error {
	mutate(&m.state)
	return nil
}

// legacyState mirrors the retired upgrade.json shape.
type legacyState struct {
	LastChecked       time.Time `json:"last_check"`
	DismissedVersions []string  `json:"dismissed_versions"`
}

func legacyStatePath() string {
	return filepath.Join(configpaths.Get().UserStateDir(), legacyStateFileName)
}

// FoldLegacyState folds the retired upgrade.json into the injected store and
// deletes it, so a launcher that upgrades keeps its check cadence and
// dismissals. It is non-interactive and mirrors migrateLegacyUpdaterState.
//
// A missing file is a no-op that touches neither the store nor the file: the
// common case must not take an extra root lock or rewrite state on every
// startup. A present value wins over the legacy one and dismissals are unioned
// per element, so folding never drops state the store already holds. Missing,
// empty, or corrupt files fold nothing but are still removed; a read or write
// failure leaves the file in place so a later check retries, and returns the
// error so the caller can report it.
func FoldLegacyState(s Store) error {
	path := legacyStatePath()
	data, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		return nil
	case err != nil:
		return err
	}

	if err := foldLegacyState(s, parseLegacyState(data)); err != nil {
		return err
	}
	_ = os.Remove(path)
	return nil
}

// parseLegacyState tolerantly parses the retired upgrade.json: a corrupt
// document yields the zero state so the fold deletes it without copying.
func parseLegacyState(data []byte) legacyState {
	var legacy legacyState
	if err := json.Unmarshal(data, &legacy); err != nil {
		return legacyState{}
	}
	return legacy
}

// foldLegacyState merges the legacy state into the store, preserving anything
// the store already holds: an absent last_checked adopts the legacy timestamp
// and dismissals are unioned per element. The merge runs as one Update so a
// concurrent launcher's write between our read and our write cannot be lost.
func foldLegacyState(s Store, legacy legacyState) error {
	return s.Update(func(st *LauncherState) {
		if st.LastChecked.IsZero() {
			st.LastChecked = legacy.LastChecked
		}
		for _, version := range legacy.DismissedVersions {
			if !slices.Contains(st.DismissedVersions, version) {
				st.DismissedVersions = append(st.DismissedVersions, version)
			}
		}
	})
}
