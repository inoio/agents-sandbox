package testutil

import (
	"context"
	"os/exec"
	"testing"
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
func InitRepo(tb testing.TB) string {
	tb.Helper()
	dir := tb.TempDir()
	RunGit(tb, dir, "init", "-b", "main")
	ConfigureRepo(tb, dir)
	WriteFile(tb, dir, "README.md", "hello")
	RunGit(tb, dir, "add", "README.md")
	RunGit(tb, dir, "commit", "-m", "initial")
	return dir
}
