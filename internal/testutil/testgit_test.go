package testutil_test

import (
	"os"
	"os/exec"
	"testing"

	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestRunGit_SucceedsOnBasicCommand(t *testing.T) {
	repo := testutil.InitRepo(t)
	out := testutil.RunGit(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	if out != "main\n" {
		t.Errorf("expected 'main\\n', got %q", out)
	}
}

func TestRunGit_ReturnsEmptyOutput(t *testing.T) {
	repo := testutil.InitRepo(t)
	// git status outputs to stderr, so CombinedOutput captures it.
	out := testutil.RunGit(t, repo, "status", "--porcelain")
	// README.md is committed, so nothing should be in the working tree.
	if out != "" {
		t.Errorf("expected empty output, got %q", out)
	}
}

func TestRunGit_FailsOnInvalidRepo(t *testing.T) {
	// Verify git itself fails when not in a repo dir.
	// RunGit will call tb.Fatalf (which fails the test) when git fails.
	// This test just verifies git fails as expected in a non-repo dir.
	err := exec.Command("git", "-C", t.TempDir(), "status").Run()
	if err == nil {
		t.Fatal("expected git to fail in non-repo dir")
	}
}

func TestRunGit_FailsOnInvalidCommand(t *testing.T) {
	repo := testutil.InitRepo(t)
	// Verify git itself fails on invalid commands.
	err := exec.Command("git", "-C", repo, "nonexistentcommand123").Run()
	if err == nil {
		t.Fatal("expected git to fail on invalid command")
	}
}

func TestRunGit_CwdInDir(t *testing.T) {
	repo := testutil.InitRepo(t)
	// Create a file in the repo and add it.
	testutil.WriteFile(t, repo, "added.txt", "content")
	testutil.RunGit(t, repo, "add", "added.txt")

	// Verify the file is actually in the repo by checking status from repo dir.
	out := testutil.RunGit(t, repo, "status", "--porcelain")
	if out == "" {
		t.Error("expected staged file in output, got empty string")
	}
}

func TestConfigureRepo_SetsUserEmail(t *testing.T) {
	repo := testutil.InitRepo(t)
	// Remove the email config we set during InitRepo, then set it again.
	testutil.RunGit(t, repo, "config", "--unset", "user.email")
	testutil.RunGit(t, repo, "config", "--unset", "user.name")

	testutil.ConfigureRepo(t, repo)

	email := testutil.RunGit(t, repo, "config", "user.email")
	if email != "test@example.com\n" {
		t.Errorf("user.email = %q, want %q", email, "test@example.com\n")
	}
}

func TestConfigureRepo_SetsUserName(t *testing.T) {
	repo := testutil.InitRepo(t)
	testutil.RunGit(t, repo, "config", "--unset", "user.name")

	testutil.ConfigureRepo(t, repo)

	name := testutil.RunGit(t, repo, "config", "user.name")
	if name != "Test User\n" {
		t.Errorf("user.name = %q, want %q", name, "Test User\n")
	}
}

func TestInitRepo_CreatesGitRepo(t *testing.T) {
	dir := testutil.InitRepo(t)

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("expected dir, got file")
	}

	info, err = os.Stat(dir + "/.git")
	if err != nil {
		t.Fatalf(".git dir missing: %v", err)
	}
	if !info.IsDir() {
		t.Error(".git is not a directory")
	}
}

func TestInitRepo_InitialBranchIsMain(t *testing.T) {
	repo := testutil.InitRepo(t)
	branch := testutil.RunGit(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	if branch != "main\n" {
		t.Errorf("expected branch 'main\\n', got %q", branch)
	}
}

func TestInitRepo_HasInitialCommit(t *testing.T) {
	repo := testutil.InitRepo(t)
	log := testutil.RunGit(t, repo, "log", "--format=%s", "-n", "1")
	if log != "initial\n" {
		t.Errorf("expected commit message 'initial\\n', got %q", log)
	}
}

func TestInitRepo_HasREADME(t *testing.T) {
	repo := testutil.InitRepo(t)
	// README should be committed and tracked.
	status := testutil.RunGit(t, repo, "status", "--porcelain")
	if status != "" {
		t.Errorf("expected clean working tree, got %q", status)
	}

	testutil.RunGit(t, repo, "show", "HEAD:README.md")
}

func TestInitRepo_WritableDir(t *testing.T) {
	repo := testutil.InitRepo(t)
	testutil.WriteFile(t, repo, "extra.txt", "data")
	out := testutil.RunGit(t, repo, "status", "--porcelain")
	// extra.txt should appear as untracked.
	if out != "?? extra.txt\n" {
		t.Errorf("expected untracked extra.txt, got %q", out)
	}
}

func TestInitRepo_SeparateCallsUseTempDirs(t *testing.T) {
	a := testutil.InitRepo(t)
	b := testutil.InitRepo(t)
	if a == b {
		t.Error("two InitRepo calls should return different dirs")
	}
}
