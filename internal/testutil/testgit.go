package testutil

import (
	"context"
	"os/exec"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	formatcfg "github.com/go-git/go-git/v5/plumbing/format/config"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// RunGit executes git with the given args in dir and returns combined output.
func RunGit(tb testing.TB, dir string, args ...string) string {
	tb.Helper()
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		tb.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// ConfigureRepo sets up basic git config in dir.
func ConfigureRepo(tb testing.TB, dir string) {
	tb.Helper()
	RunGit(tb, dir, "config", "user.email", "test@example.com")
	RunGit(tb, dir, "config", "user.name", "Test User")
}

// InitRepo creates a new git repo in a temp dir with initial config and commit.
// It initializes the repo with go-git (in-process) rather than the git CLI:
// the resulting objects and config are standard, so real-git operations such as
// `git worktree add` work against it, but tests no longer pay for several `git`
// subprocess spawns on every call.
func InitRepo(tb testing.TB) string {
	tb.Helper()
	dir := tb.TempDir()

	// keyed by field so exhaustruct sees every PlainInitOptions field; the
	// embedded InitOptions key is what exhaustruct needs but modernize's
	// embedlit rule dislikes, so suppress only that.
	//nolint:modernize // embedded InitOptions cannot be set without the key
	initOptions := git.PlainInitOptions{
		InitOptions:  git.InitOptions{DefaultBranch: plumbing.Main},
		Bare:         false,
		ObjectFormat: formatcfg.ObjectFormat(""),
	}
	repo, err := git.PlainInitWithOptions(dir, &initOptions)
	if err != nil {
		tb.Fatalf("init repo in %s: %v", dir, err)
	}

	cfg, err := repo.Config()
	if err != nil {
		tb.Fatalf("read repo config in %s: %v", dir, err)
	}
	cfg.User.Name = "Test User"
	cfg.User.Email = "test@example.com"
	if err = repo.Storer.SetConfig(cfg); err != nil {
		tb.Fatalf("write repo config in %s: %v", dir, err)
	}

	WriteFile(tb, dir, "README.md", "hello")
	worktree, err := repo.Worktree()
	if err != nil {
		tb.Fatalf("open worktree in %s: %v", dir, err)
	}
	if _, err = worktree.Add("README.md"); err != nil {
		tb.Fatalf("stage README.md in %s: %v", dir, err)
	}

	commitOptions := git.CommitOptions{
		Author:            &object.Signature{Name: "Test User", Email: "test@example.com", When: time.Now()},
		Committer:         nil,
		Parents:           nil,
		SignKey:           nil,
		Signer:            nil,
		All:               false,
		AllowEmptyCommits: false,
		Amend:             false,
	}
	if _, err = worktree.Commit("initial", &commitOptions); err != nil {
		tb.Fatalf("commit initial in %s: %v", dir, err)
	}
	return dir
}
