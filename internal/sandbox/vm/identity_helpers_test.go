package vm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/git"
)

// mustIdentity resolves a fixture cwd to its project identity, failing the test
// on the now-fallible ResolveIdentity: fixtures always point at real paths.
func mustIdentity(t testing.TB, cwd string) git.Identity {
	t.Helper()
	identity, err := git.ResolveIdentity(cwd)
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
