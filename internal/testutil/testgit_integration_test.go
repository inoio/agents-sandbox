//go:build integration

package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunGit_SucceedsOnBasicCommand(t *testing.T) {
	repo := InitRepo(t)
	out := RunGit(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	if out != "main\n" {
		t.Errorf("expected 'main\\n', got %q", out)
	}
}

func TestRunGit_ReturnsEmptyOutput(t *testing.T) {
	repo := InitRepo(t)
	// git status outputs to stderr, so CombinedOutput captures it.
	out := RunGit(t, repo, "status", "--porcelain")
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
	repo := InitRepo(t)
	// Verify git itself fails on invalid commands.
	err := exec.Command("git", "-C", repo, "nonexistentcommand123").Run()
	if err == nil {
		t.Fatal("expected git to fail on invalid command")
	}
}

func TestRunGit_CwdInDir(t *testing.T) {
	repo := InitRepo(t)
	// Create a file in the repo and add it.
	WriteFile(t, repo, "added.txt", "content")
	RunGit(t, repo, "add", "added.txt")

	// Verify the file is actually in the repo by checking status from repo dir.
	out := RunGit(t, repo, "status", "--porcelain")
	if out == "" {
		t.Error("expected staged file in output, got empty string")
	}
}

func TestConfigureRepo_SetsUserEmail(t *testing.T) {
	repo := InitRepo(t)
	// Remove the email config we set during InitRepo, then set it again.
	RunGit(t, repo, "config", "--unset", "user.email")
	RunGit(t, repo, "config", "--unset", "user.name")

	ConfigureRepo(t, repo)

	email := RunGit(t, repo, "config", "user.email")
	if email != "test@example.com\n" {
		t.Errorf("user.email = %q, want %q", email, "test@example.com\n")
	}
}

func TestConfigureRepo_SetsUserName(t *testing.T) {
	repo := InitRepo(t)
	RunGit(t, repo, "config", "--unset", "user.name")

	ConfigureRepo(t, repo)

	name := RunGit(t, repo, "config", "user.name")
	if name != "Test User\n" {
		t.Errorf("user.name = %q, want %q", name, "Test User\n")
	}
}

func TestInitRepo_InitialBranchIsMain(t *testing.T) {
	repo := InitRepo(t)
	branch := RunGit(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	if branch != "main\n" {
		t.Errorf("expected branch 'main\\n', got %q", branch)
	}
}

func TestInitRepo_HasInitialCommit(t *testing.T) {
	repo := InitRepo(t)
	log := RunGit(t, repo, "log", "--format=%s", "-n", "1")
	if log != "initial\n" {
		t.Errorf("expected commit message 'initial\\n', got %q", log)
	}
}

func TestInitRepo_HasREADME(t *testing.T) {
	repo := InitRepo(t)
	// README should be committed and tracked.
	status := RunGit(t, repo, "status", "--porcelain")
	if status != "" {
		t.Errorf("expected clean working tree, got %q", status)
	}

	RunGit(t, repo, "show", "HEAD:README.md")
}

func TestInitRepo_WritableDir(t *testing.T) {
	repo := InitRepo(t)
	WriteFile(t, repo, "extra.txt", "data")
	out := RunGit(t, repo, "status", "--porcelain")
	// extra.txt should appear as untracked.
	if out != "?? extra.txt\n" {
		t.Errorf("expected untracked extra.txt, got %q", out)
	}
}

// TestRunGitIgnoresAmbientGitConfig pins the test-side isolation: RunGit must
// run git without the developer's global config, so an ambient setting such as
// commit signing — which can pop a GUI askpass (ksshaskpass) — never reaches a
// fixture commit.
func TestRunGitIgnoresAmbientGitConfig(t *testing.T) {
	poisoned := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(
		poisoned,
		[]byte("[commit]\n\tgpgsign = true\n[gpg]\n\tprogram = /bin/false\n"),
		0o644,
	); err != nil {
		t.Fatalf("write poisoned global config: %v", err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", poisoned)

	repo := InitRepo(t)
	WriteFile(t, repo, "second.txt", "content")
	RunGit(t, repo, "add", "second.txt")
	RunGit(t, repo, "commit", "-m", "second")

	log := RunGit(t, repo, "log", "--format=%s", "-n", "1")
	if log != "second\n" {
		t.Errorf("commit did not land under the isolated config, log = %q", log)
	}
}

func TestRunGitReportsTheFailedCommand(t *testing.T) {
	fake := &recordingTB{}
	message := captureFatalf(t, fake, func() {
		RunGit(fake, t.TempDir(), "nonexistentcommand123")
	})
	if !strings.Contains(message, "nonexistentcommand123") {
		t.Errorf("RunGit fatal message %q does not name the failing command", message)
	}
}

func TestInitRepoReportsAnExistingRepository(t *testing.T) {
	dir := t.TempDir()
	RunGit(t, dir, "init", "--quiet")

	fake := &recordingTB{tempDir: dir}
	message := captureFatalf(t, fake, func() {
		InitRepo(fake)
	})
	if !strings.Contains(message, "init repo") {
		t.Errorf("InitRepo fatal message %q does not mention repository init", message)
	}
}
