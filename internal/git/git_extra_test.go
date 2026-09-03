package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-billy/v5/osfs"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/storage/filesystem"

	"github.com/inoio/opencode-sandbox/internal/testutil"
)

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
}

func TestOriginURL(t *testing.T) {
	t.Run("not a repository", func(t *testing.T) {
		dir := t.TempDir()
		if got := originURL(dir); got != "" {
			t.Errorf("originURL(%q) = %q, want empty", dir, got)
		}
	})

	t.Run("returns origin url", func(t *testing.T) {
		const origin = "git@github.com:inoio/opencode-sandbox.git"
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

func TestWorktreeRootBareRepo(t *testing.T) {
	dir := t.TempDir()
	if _, err := git.PlainInit(dir, true); err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	fs := osfs.New(dir)
	repo, err := git.Open(filesystem.NewStorage(fs, cache.NewObjectLRUDefault()), nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := worktreeRoot(repo); err == nil {
		t.Error("expected an error for a bare repository")
	}
}
