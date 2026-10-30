package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestResolveIdentityNonRepoLayout(t *testing.T) {
	dir := mustResolvePath(t, t.TempDir())

	got := mustResolveIdentity(t, dir)

	if got.BindRoot != dir {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, dir)
	}
	if got.CheckoutRoot != "" {
		t.Errorf("CheckoutRoot = %q, want empty", got.CheckoutRoot)
	}
	if got.OutsideWorktree {
		t.Errorf("OutsideWorktree = true, want false")
	}
}

func TestResolveIdentityRepoRoot(t *testing.T) {
	repo := mustResolvePath(t, testutil.InitRepo(t))

	got := mustResolveIdentity(t, repo)

	if got.BindRoot != repo {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, repo)
	}
	if got.CheckoutRoot != repo {
		t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, repo)
	}
	if got.OutsideWorktree {
		t.Errorf("OutsideWorktree = true, want false")
	}
}

func TestResolveIdentityNestedWorktree(t *testing.T) {
	repo := mustResolvePath(t, testutil.InitRepo(t))
	wt := filepath.Join(repo, "wt")
	testutil.RunGit(t, repo, "worktree", "add", wt)

	got := mustResolveIdentity(t, wt)

	if got.BindRoot != repo {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, repo)
	}
	if got.CheckoutRoot != repo {
		t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, repo)
	}
	if got.OutsideWorktree {
		t.Errorf("OutsideWorktree = true, want false")
	}
}

func TestResolveIdentityOutsideWorktree(t *testing.T) {
	repo := mustResolvePath(t, testutil.InitRepo(t))
	wt := filepath.Join(mustResolvePath(t, t.TempDir()), "wt")
	testutil.RunGit(t, repo, "worktree", "add", wt)

	got := mustResolveIdentity(t, wt)

	if got.BindRoot != wt {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, wt)
	}
	if got.CheckoutRoot != repo {
		t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, repo)
	}
	if !got.OutsideWorktree {
		t.Errorf("OutsideWorktree = false, want true")
	}
}

func TestResolveIdentityMalformedGitFile(t *testing.T) {
	dir := mustResolvePath(t, t.TempDir())
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("this is not a gitdir pointer"), 0o644); err != nil {
		t.Fatalf("write .git: %v", err)
	}

	got := mustResolveIdentity(t, dir)

	if got.BindRoot != dir {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, dir)
	}
	if got.CheckoutRoot != "" {
		t.Errorf("CheckoutRoot = %q, want empty", got.CheckoutRoot)
	}
	if got.OutsideWorktree {
		t.Errorf("OutsideWorktree = true, want false")
	}
}

// TestResolveIdentityOutsideWorktreeOfBareGitDir builds a bare clone at
// tmp/bare.git and adds a worktree at tmp/wt, a sibling of the bare dir. The
// worktree is a linked worktree placed outside its checkout, so no containment
// is possible: it is reported as outside and scoped as its own checkout rather
// than sharing the bare dir's VM with a different /workspace bind.
func TestResolveIdentityOutsideWorktreeOfBareGitDir(t *testing.T) {
	origin := testutil.InitRepo(t)
	tmp := mustResolvePath(t, t.TempDir())
	bare := filepath.Join(tmp, "bare.git")
	testutil.RunGit(t, tmp, "clone", "--bare", origin, "bare.git")
	wt := filepath.Join(tmp, "wt")
	testutil.RunGit(t, bare, "worktree", "add", wt)

	got := mustResolveIdentity(t, wt)

	if !got.OutsideWorktree {
		t.Errorf("OutsideWorktree = false, want true")
	}
	if got.BindRoot != wt {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, wt)
	}
	if got.CheckoutRoot != bare {
		t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, bare)
	}
}

func TestResolveIdentityBareWorktree(t *testing.T) {
	origin := testutil.InitRepo(t)
	root := mustResolvePath(t, t.TempDir())
	testutil.RunGit(t, root, "clone", "--bare", origin, ".git")
	testutil.RunGit(t, filepath.Join(root, ".git"), "worktree", "add", filepath.Join(root, "main"))

	// The bare root and every worktree inside it share one checkout slug.
	rootIdentity := mustResolveIdentity(t, root)

	t.Run("bare root", func(t *testing.T) {
		got := mustResolveIdentity(t, root)
		if got.BindRoot != root {
			t.Errorf("BindRoot = %q, want %q", got.BindRoot, root)
		}
		if got.CheckoutRoot != root {
			t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, root)
		}
		if got.OutsideWorktree {
			t.Errorf("OutsideWorktree = true, want false")
		}
	})

	t.Run("worktree root", func(t *testing.T) {
		got := mustResolveIdentity(t, filepath.Join(root, "main"))
		if got.BindRoot != root {
			t.Errorf("BindRoot = %q, want %q", got.BindRoot, root)
		}
		if got.CheckoutRoot != root {
			t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, root)
		}
		if got.OutsideWorktree {
			t.Errorf("OutsideWorktree = true, want false")
		}
		if got.CheckoutSlug != rootIdentity.CheckoutSlug {
			t.Errorf("CheckoutSlug = %q, want the bare root's %q", got.CheckoutSlug, rootIdentity.CheckoutSlug)
		}
	})

	t.Run("worktree subdirectory", func(t *testing.T) {
		sub := filepath.Join(root, "main", "internal")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
		got := mustResolveIdentity(t, sub)
		if got.BindRoot != root {
			t.Errorf("BindRoot = %q, want %q", got.BindRoot, root)
		}
		if got.CheckoutRoot != root {
			t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, root)
		}
		if got.OutsideWorktree {
			t.Errorf("OutsideWorktree = true, want false")
		}
	})
}

func TestResolveIdentityRelativeGitDir(t *testing.T) {
	origin := testutil.InitRepo(t)
	root := mustResolvePath(t, t.TempDir())
	testutil.RunGit(t, root, "clone", "--bare", origin, ".git")
	testutil.RunGit(t, filepath.Join(root, ".git"), "config", "worktree.useRelativePaths", "true")
	testutil.RunGit(t, filepath.Join(root, ".git"), "worktree", "add", filepath.Join(root, "main"))

	got := mustResolveIdentity(t, filepath.Join(root, "main"))

	if got.BindRoot != root {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, root)
	}
	if got.OutsideWorktree {
		t.Errorf("OutsideWorktree = true, want false")
	}
}

// TestIsNamedBareGitDirRequiresBareConfig pins the final isBare check: a
// directory under a name other than ".git" that holds a HEAD file is still not a
// bare git dir unless its config says core.bare = true. A stray "config" with
// core.bare = false (or no config at all) must not pass as a repository.
func TestIsNamedBareGitDirRequiresBareConfig(t *testing.T) {
	base := mustResolvePath(t, t.TempDir())
	writeHead := func(dir string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(HEAD): %v", err)
		}
	}

	t.Run("HEAD file without a config", func(t *testing.T) {
		dir := filepath.Join(base, "head-only")
		writeHead(dir)
		if isNamedBareGitDir(dir) {
			t.Errorf("isNamedBareGitDir(%q) = true, want false without a bare config", dir)
		}
	})

	t.Run("HEAD file with core.bare = false", func(t *testing.T) {
		dir := filepath.Join(base, "non-bare-config")
		writeHead(dir)
		if err := os.WriteFile(filepath.Join(dir, "config"), []byte("[core]\n\tbare = false\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(config): %v", err)
		}
		if isNamedBareGitDir(dir) {
			t.Errorf("isNamedBareGitDir(%q) = true, want false for core.bare = false", dir)
		}
	})
}
