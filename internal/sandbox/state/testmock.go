package state

import "testing"

// SetStateDirForTest overrides the state directory root for the given test.
// The original value is restored via t.Cleanup.
func SetStateDirForTest(t *testing.T, dir string) {
	old := StateDir
	StateDir = dir
	t.Cleanup(func() { StateDir = old })
}
