package state

import (
	"cmp"
	"slices"
)

// WithKeyLocks runs fn while holding, for every key, the blocking home lock and
// the blocking project-VM creation lock, taken in a deterministic (slug, agent)
// order (home before project-VM within a key). It is the only place two per-key
// locks are held at once, so a multi-key operation cannot be interleaved by a
// single-key state writer or VM creation. An empty or nil set runs fn with no
// locks. A failed acquire releases every lock already taken. The global order
// root -> per-key holds: callers take per-key locks only inside a root
// transaction, never the reverse. See specs/project-state-transactions.md.
func WithKeyLocks(keys []Key, fn func() error) error {
	var releases []func()
	releaseAll := func() {
		for _, release := range slices.Backward(releases) {
			release()
		}
	}
	for _, k := range sortedUniqueKeys(keys) {
		releaseHome, _, err := lockHomeState(k, true)
		if err != nil {
			releaseAll()
			return err
		}
		releases = append(releases, releaseHome)
		releaseVM, _, err := lockProjectVM(k, true)
		if err != nil {
			releaseAll()
			return err
		}
		releases = append(releases, releaseVM)
	}
	defer releaseAll()
	return fn()
}

// sortedUniqueKeys returns a sorted, de-duplicated copy of keys, ordered by
// (slug, agent). The copy keeps the caller's slice untouched, and the order
// makes concurrent multi-key acquisitions lock in the same sequence.
func sortedUniqueKeys(keys []Key) []Key {
	out := slices.Clone(keys)
	slices.SortFunc(out, func(a, b Key) int {
		return cmp.Or(cmp.Compare(a.Slug, b.Slug), cmp.Compare(a.Agent, b.Agent))
	})
	return slices.Compact(out)
}
