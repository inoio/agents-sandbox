package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/testutil"
)

// writeWorktreeConfigFixture lays out a git directory whose shared config and,
// when worktreeConfig is non-empty, config.worktree carry the given contents,
// and returns the git dir path.
func writeWorktreeConfigFixture(t *testing.T, sharedConfig, worktreeConfig string) string {
	t.Helper()
	gitDir := filepath.Join(mustResolvePath(t, t.TempDir()), ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", gitDir, err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(sharedConfig), 0o644); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}
	if worktreeConfig != "" {
		if err := os.WriteFile(filepath.Join(gitDir, "config.worktree"), []byte(worktreeConfig), 0o644); err != nil {
			t.Fatalf("WriteFile(config.worktree): %v", err)
		}
	}
	return gitDir
}

// worktreeConfigOn is a shared config with extensions.worktreeConfig enabled.
const worktreeConfigOn = "[core]\n\trepositoryformatversion = 1\n[extensions]\n\tworktreeConfig = true\n"

// TestIsBareReadsWorktreeConfig pins that a bare repo is still detected when
// git has moved core.bare out of the shared config into the main worktree's
// config.worktree, which it does when extensions.worktreeConfig is set (for
// example once sparse-checkout is used).
func TestIsBareReadsWorktreeConfig(t *testing.T) {
	gitDir := writeWorktreeConfigFixture(t, worktreeConfigOn, "[core]\n\tbare = true\n")
	if !isBare(gitDir) {
		t.Errorf("isBare(%q) = false, want true when core.bare is in config.worktree", gitDir)
	}
}

// TestIsBareIgnoresWorktreeConfigWithoutExtension pins that a config.worktree is
// not consulted unless extensions.worktreeConfig is enabled: git itself ignores
// the file then, so a leftover with core.bare must not make isBare report bare.
func TestIsBareIgnoresWorktreeConfigWithoutExtension(t *testing.T) {
	gitDir := writeWorktreeConfigFixture(t, "[core]\n\trepositoryformatversion = 1\n", "[core]\n\tbare = true\n")
	if isBare(gitDir) {
		t.Errorf("isBare(%q) = true, want false when extensions.worktreeConfig is disabled", gitDir)
	}
}

// TestIsBareWorktreeConfigEnabledStillNeedsCoreBare pins that enabling the
// extension only makes git read config.worktree; the file must itself set
// core.bare. An extension-enabled repo whose config.worktree does not claim
// bare is not bare.
func TestIsBareWorktreeConfigEnabledStillNeedsCoreBare(t *testing.T) {
	gitDir := writeWorktreeConfigFixture(t, worktreeConfigOn, "[core]\n\tbare = false\n")
	if isBare(gitDir) {
		t.Errorf("isBare(%q) = true, want false when config.worktree does not set core.bare", gitDir)
	}
}

// TestIsBareWorktreeConfigBooleanSpellings pins that enabling
// extensions.worktreeConfig is recognized for every boolean git accepts, not
// just "true": with the extension on and core.bare in config.worktree, each
// true spelling reports the repo bare and each false spelling does not.
func TestIsBareWorktreeConfigBooleanSpellings(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"true", true}, {"True", true}, {"yes", true}, {"YES", true}, {"on", true},
		{"1", true}, {"2", true}, {"-1", true},
		{"false", false}, {"no", false}, {"off", false}, {"0", false},
		{"", false}, {"nonsense", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			shared := "[core]\n\trepositoryformatversion = 1\n[extensions]\n\tworktreeConfig = " + tc.value + "\n"
			gitDir := writeWorktreeConfigFixture(t, shared, "[core]\n\tbare = true\n")
			if got := isBare(gitDir); got != tc.want {
				t.Errorf("isBare with extensions.worktreeConfig = %q = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

// TestResolveIdentityBareFromWorktreeConfig pins the resolver-level contract: a
// bare checkout whose core.bare lives only in config.worktree still resolves as
// bare, so project config is read from the bare root rather than the worktree.
func TestResolveIdentityBareFromWorktreeConfig(t *testing.T) {
	origin := testutil.InitRepo(t)
	root := mustResolvePath(t, t.TempDir())
	gitDir := filepath.Join(root, ".git")
	testutil.RunGit(t, root, "clone", "--bare", origin, ".git")
	testutil.RunGit(t, gitDir, "config", "worktree.useRelativePaths", "true")
	testutil.RunGit(t, gitDir, "worktree", "add", filepath.Join(root, "main"))

	// Reproduce the shape git leaves behind when extensions.worktreeConfig is
	// enabled: core.bare is gone from the shared config and lives in the main
	// worktree's config.worktree.
	if err := os.WriteFile(
		filepath.Join(gitDir, "config"),
		[]byte(worktreeConfigOn),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(gitDir, "config.worktree"),
		[]byte("[core]\n\tbare = true\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(config.worktree): %v", err)
	}

	got := mustResolveIdentity(t, filepath.Join(root, "main"))
	if !got.Bare {
		t.Errorf("ResolveIdentity(...).Bare = false, want true with core.bare in config.worktree")
	}
	if got.BindRoot != root {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, root)
	}
}
