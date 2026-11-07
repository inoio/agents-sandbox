package git

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// mustResolvePath is the test-side wrapper for the now-fallible resolvePath:
// every fixture path exists, so an error is a broken test rather than an input.
func mustResolvePath(t testing.TB, path string) string {
	t.Helper()
	resolved, err := resolvePath(path)
	require.NoError(t, err)
	return resolved
}

// worktreeConfigOn is a shared config with extensions.worktreeConfig enabled.
const worktreeConfigOn = "[core]\n\trepositoryformatversion = 1\n[extensions]\n\tworktreeConfig = true\n"
