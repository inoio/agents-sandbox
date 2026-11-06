package testutil

import (
	"os"
	"slices"
	"testing"
)

// TestGitIsolationEnvironmentNeutralizesAmbientGitConfig pins the exact env
// RunGit hands git: the developer's global/system config, askpass programs, and
// per-invocation config channels are dropped and replaced with non-interactive
// defaults, so a fixture commit can never reach a signing or credential prompt.
// The real-git behaviour is pinned separately in testgit_integration_test.go.
func TestGitIsolationEnvironmentNeutralizesAmbientGitConfig(t *testing.T) {
	isolated := gitIsolationEnvironment([]string{
		"GIT_CONFIG_GLOBAL=/home/dev/.gitconfig",
		"GIT_CONFIG_SYSTEM=/etc/gitconfig",
		"GIT_CONFIG_PARAMETERS='commit.gpgsign=true'",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=commit.gpgsign",
		"GIT_CONFIG_VALUE_0=true",
		"GIT_ASKPASS=ksshaskpass",
		"SSH_ASKPASS=ksshaskpass",
		"SSH_ASKPASS_REQUIRE=force",
		"PATH=/usr/bin",
	})

	want := []string{
		"PATH=/usr/bin",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
	}
	if !slices.Equal(isolated, want) {
		t.Errorf("gitIsolationEnvironment() = %q, want %q", isolated, want)
	}
}

func TestInitRepo_CreatesGitRepo(t *testing.T) {
	dir := InitRepo(t)

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

func TestInitRepo_SeparateCallsUseTempDirs(t *testing.T) {
	a := InitRepo(t)
	b := InitRepo(t)
	if a == b {
		t.Error("two InitRepo calls should return different dirs")
	}
}
