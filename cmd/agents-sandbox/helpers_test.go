package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// mustIdentity resolves a fixture cwd to its project identity, failing the test
// on the now-fallible resolution: fixtures always point at real paths. It reads
// through the same seam as the preflight, so a test that asserts the identity it
// expects and the command that resolves it share the memoized result.
func mustIdentity(t testing.TB, cwd string) git.Identity {
	t.Helper()
	identity, err := resolveIdentity(cwd)
	require.NoError(t, err)
	return identity
}

// unresolvableIdentityDir returns a directory whose .git file points at a git
// directory that does not exist, so git.ResolveIdentity fails for it while the
// directory itself still resolves.
func unresolvableIdentityDir(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, ".git"),
		[]byte("gitdir: "+filepath.Join(t.TempDir(), "gone.git")+"\n"),
		0o644,
	))
	return dir
}

// seedHomeState writes hs as the key's HomeState through the recovery
// transaction. Tests only.
func seedHomeState(k state.Key, hs state.HomeState) error {
	return state.RecoverHomeState(k, func(cur *state.HomeState, _ bool) (bool, error) {
		*cur = hs
		return true, nil
	})
}
