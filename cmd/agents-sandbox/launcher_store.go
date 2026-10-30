package main

import (
	"errors"

	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/upgrade"
)

// errRootStateLocked reports a contended root-state lock. A non-blocking
// transaction returns ok=false with no error in that case, so the adapter turns
// it into an error: upgrade.Check then defers instead of reading a false empty
// state or writing over a concurrent writer's document.
var errRootStateLocked = errors.New("root state lock is held by another process")

// launcherStore persists the launcher's upgrade-check state in the
// launcher-global root state document (updates.launcher), so the retired
// upgrade.json has a single home alongside every other launcher-global fact.
//
// Every access goes through state.WithRootState, the only locked root-state
// primitive: a contended lock (or an unreadable document) is reported as an
// error rather than silently skipped, so upgrade.Check defers the check to a
// later run and the legacy-file fold keeps its file instead of dropping state.
type launcherStore struct{}

func (launcherStore) Load() (upgrade.LauncherState, error) {
	var loaded upgrade.LauncherState
	ok, err := state.WithRootState(false, func(tx *state.RootTxn) error {
		launcher := tx.State().LauncherUpdate()
		loaded = upgrade.LauncherState{
			LastChecked:       launcher.LastChecked,
			DismissedVersions: launcher.DismissedVersions,
		}
		return nil
	})
	if err != nil {
		return upgrade.LauncherState{}, err
	}
	if !ok {
		return upgrade.LauncherState{}, errRootStateLocked
	}
	return loaded, nil
}

func (launcherStore) Update(mutate func(*upgrade.LauncherState)) error {
	ok, err := state.WithRootState(false, func(tx *state.RootTxn) error {
		launcher := tx.State().LauncherUpdate()
		current := upgrade.LauncherState{
			LastChecked:       launcher.LastChecked,
			DismissedVersions: launcher.DismissedVersions,
		}
		mutate(&current)
		tx.State().SetLauncherUpdate(state.LauncherUpdateState{
			LastChecked:       current.LastChecked,
			DismissedVersions: current.DismissedVersions,
		})
		return tx.Commit()
	})
	if err != nil {
		return err
	}
	if !ok {
		return errRootStateLocked
	}
	return nil
}
