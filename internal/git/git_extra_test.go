package git

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestIsBare(t *testing.T) {
	repo := testutil.InitRepo(t)
	if isBare(filepath.Join(repo, ".git")) {
		t.Error("a normal checkout's git dir must not be bare")
	}
	if isBare(t.TempDir()) {
		t.Error("a git dir without a readable config must not be reported bare")
	}
}

// TestWorktreeRegistrationRejectsMalformedNames pins the two name guards: an
// empty final segment (a trailing "/worktrees/") and a name containing "/" are
// rejected even when the candidate carries a commondir marker, so a malformed
// gitdir cannot strip to a shared dir.
func TestWorktreeRegistrationRejectsMalformedNames(t *testing.T) {
	base := mustResolvePath(t, t.TempDir())
	marker := func(dir string) string {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "commondir"), []byte("../..\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", dir, err)
		}
		return dir
	}
	emptyName := marker(filepath.Join(base, "one", "worktrees")) + string(filepath.Separator)
	if shared, ok := worktreeRegistration(base, emptyName); ok || shared != "" {
		t.Errorf("empty worktree name: got (%q, %v), want (\"\", false)", shared, ok)
	}
	slashedName := marker(filepath.Join(base, "two", "worktrees", "name", "sub"))
	if shared, ok := worktreeRegistration(base, slashedName); ok || shared != "" {
		t.Errorf("slashed worktree name: got (%q, %v), want (\"\", false)", shared, ok)
	}
}

// TestResolvePathErrorsForMissingPath pins that resolvePath no longer
// substitutes the unresolved absolute path for a path it cannot resolve: a
// missing path is an error, so no caller can hash a degraded path into a
// project identity (#354).
func TestResolvePathErrorsForMissingPath(t *testing.T) {
	dir := mustResolvePath(t, t.TempDir())
	missing := filepath.Join(dir, "does-not-exist")
	_, err := resolvePath(missing)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("resolvePath(%q) = %v, want an error wrapping fs.ErrNotExist", missing, err)
	}
}

func TestResolveGitDir(t *testing.T) {
	t.Run("missing dotgit", func(t *testing.T) {
		dir := t.TempDir()
		if got := resolveGitDir(dir); got != "" {
			t.Errorf("resolveGitDir(%q) = %q, want empty", dir, got)
		}
	})

	t.Run("dotgit is a directory", func(t *testing.T) {
		repo := testutil.InitRepo(t)
		want := filepath.Join(repo, ".git")
		if got := resolveGitDir(repo); got != want {
			t.Errorf("resolveGitDir(%q) = %q, want %q", repo, got, want)
		}
	})

	t.Run("linked worktree strips worktrees segment", func(t *testing.T) {
		repo := testutil.InitRepo(t)
		wtDir := filepath.Join(t.TempDir(), "wt")
		testutil.RunGit(t, repo, "worktree", "add", "--detach", wtDir)
		want := filepath.Join(repo, ".git")
		if got := resolveGitDir(wtDir); got != want {
			t.Errorf("resolveGitDir(%q) = %q, want %q", wtDir, got, want)
		}
	})

	t.Run("relative worktree gitdir resolves against root", func(t *testing.T) {
		repo := t.TempDir()
		wtDir := filepath.Join(t.TempDir(), "wt")
		if err := os.MkdirAll(wtDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		// A registration needs its commondir marker, so a stripped-and-joined
		// relative gitdir is validated as genuine.
		candidate := filepath.Join(repo, ".git", "worktrees", "wt")
		if err := os.MkdirAll(candidate, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(candidate, "commondir"), []byte("../..\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		relative, err := filepath.Rel(wtDir, candidate)
		if err != nil {
			t.Fatalf("Rel: %v", err)
		}
		if err := os.WriteFile(filepath.Join(wtDir, ".git"), []byte("gitdir: "+relative+"\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		want := filepath.Join(repo, ".git")
		if got := resolveGitDir(wtDir); got != want {
			t.Errorf("resolveGitDir(%q) = %q, want %q", wtDir, got, want)
		}
	})

	t.Run("dotgit file without worktrees segment", func(t *testing.T) {
		dir := t.TempDir()
		want := "/shared/.git"
		if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+want+"\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if got := resolveGitDir(dir); got != want {
			t.Errorf("resolveGitDir(%q) = %q, want %q", dir, got, want)
		}
	})

	t.Run("dotgit file without gitdir prefix", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("not-a-gitdir"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if got := resolveGitDir(dir); got != "" {
			t.Errorf("resolveGitDir(%q) = %q, want empty", dir, got)
		}
	})

	t.Run("unreadable dotgit file", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /x"), 0o000); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if _, err := os.ReadFile(filepath.Join(dir, ".git")); err == nil {
			t.Skip("file permissions are not enforced in this environment")
		}
		if got := resolveGitDir(dir); got != "" {
			t.Errorf("resolveGitDir(%q) = %q, want empty", dir, got)
		}
	})

	t.Run("submodule gitdir under a worktrees-named ancestor", func(t *testing.T) {
		sub := newSubmoduleUnderWorktreesAncestor(t)
		want := filepath.Join(filepath.Dir(sub), ".git", "modules", "sub")
		if got := resolveGitDir(sub); got != want {
			t.Errorf("resolveGitDir(%q) = %q, want %q", sub, got, want)
		}
		if isLinkedWorktree(sub) {
			t.Errorf("isLinkedWorktree(%q) = true, want false", sub)
		}
	})

	t.Run("relative gitdir under a worktrees-named ancestor", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "worktrees", "proj", "sub")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: ../shared.git\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		want := filepath.Join(root, "..", "shared.git")
		if got := resolveGitDir(root); got != want {
			t.Errorf("resolveGitDir(%q) = %q, want %q", root, got, want)
		}
		if isLinkedWorktree(root) {
			t.Errorf("isLinkedWorktree(%q) = true, want false", root)
		}
	})

	t.Run("absolute gitdir under a worktrees-named ancestor", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "proj")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		shared := filepath.Join(t.TempDir(), "worktrees", "proj", "shared.git")
		if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+shared+"\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if got := resolveGitDir(root); got != shared {
			t.Errorf("resolveGitDir(%q) = %q, want %q", root, got, shared)
		}
		if isLinkedWorktree(root) {
			t.Errorf("isLinkedWorktree(%q) = true, want false", root)
		}
	})

	t.Run("worktree registration under a worktrees-named ancestor", func(t *testing.T) {
		repo := newRepoUnderWorktreesAncestor(t)
		wt := filepath.Join(repo, "wt")
		testutil.RunGit(t, repo, "worktree", "add", wt)
		want := filepath.Join(repo, ".git")
		if got := resolveGitDir(wt); got != want {
			t.Errorf("resolveGitDir(%q) = %q, want %q", wt, got, want)
		}
		if !isLinkedWorktree(wt) {
			t.Errorf("isLinkedWorktree(%q) = false, want true", wt)
		}
	})
}

func TestIsLinkedWorktree(t *testing.T) {
	t.Run("dotgit directory", func(t *testing.T) {
		repo := testutil.InitRepo(t)
		if isLinkedWorktree(repo) {
			t.Errorf("isLinkedWorktree(%q) = true, want false", repo)
		}
	})

	t.Run("linked worktree", func(t *testing.T) {
		repo := testutil.InitRepo(t)
		wtDir := filepath.Join(t.TempDir(), "wt")
		testutil.RunGit(t, repo, "worktree", "add", "--detach", wtDir)
		if !isLinkedWorktree(wtDir) {
			t.Errorf("isLinkedWorktree(%q) = false, want true", wtDir)
		}
	})

	t.Run("separate git dir", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /shared/repo.git\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if isLinkedWorktree(dir) {
			t.Errorf("isLinkedWorktree(%q) = true, want false", dir)
		}
	})

	t.Run("missing dotgit", func(t *testing.T) {
		dir := t.TempDir()
		if isLinkedWorktree(dir) {
			t.Errorf("isLinkedWorktree(%q) = true, want false", dir)
		}
	})
}

func TestOriginURL(t *testing.T) {
	t.Run("not a repository", func(t *testing.T) {
		dir := t.TempDir()
		if got := originURL(dir); got != "" {
			t.Errorf("originURL(%q) = %q, want empty", dir, got)
		}
	})

	t.Run("returns origin url", func(t *testing.T) {
		const origin = "git@github.com:inoio/agents-sandbox.git"
		repo := testutil.InitRepo(t)
		testutil.RunGit(t, repo, "remote", "add", "origin", origin)
		if got := originURL(repo); got != origin {
			t.Errorf("originURL(%q) = %q, want %q", repo, got, origin)
		}
	})

	t.Run("no origin remote", func(t *testing.T) {
		repo := testutil.InitRepo(t)
		if got := originURL(repo); got != "" {
			t.Errorf("originURL(%q) = %q, want empty", repo, got)
		}
	})

	t.Run("missing config file", func(t *testing.T) {
		repo := testutil.InitRepo(t)
		if err := os.Remove(filepath.Join(repo, ".git", "config")); err != nil {
			t.Fatalf("Remove config: %v", err)
		}
		if got := originURL(repo); got != "" {
			t.Errorf("originURL(%q) = %q, want empty", repo, got)
		}
	})

	t.Run("malformed config file", func(t *testing.T) {
		repo := testutil.InitRepo(t)
		if err := os.WriteFile(
			filepath.Join(repo, ".git", "config"),
			[]byte("not [valid] git config"),
			0o644,
		); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if got := originURL(repo); got != "" {
			t.Errorf("originURL(%q) = %q, want empty", repo, got)
		}
	})
}
