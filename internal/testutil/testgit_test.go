package testutil

import (
	"os"
	"strings"
	"testing"
)

// TestGitIsolationEnvironmentNeutralizesAmbientGitConfig pins the env RunGit
// hands git: the developer's global/system config and askpass programs are
// dropped and replaced with non-interactive defaults, so a fixture commit can
// never reach a signing or credential prompt. The real-git behaviour is pinned
// separately in testgit_integration_test.go.
func TestGitIsolationEnvironmentNeutralizesAmbientGitConfig(t *testing.T) {
	isolated := gitIsolationEnvironment([]string{
		"PATH=/usr/bin",
		"GIT_CONFIG_GLOBAL=/home/dev/.gitconfig",
		"GIT_CONFIG_SYSTEM=/etc/gitconfig",
		"GIT_ASKPASS=ksshaskpass",
		"SSH_ASKPASS=ksshaskpass",
		"SSH_ASKPASS_REQUIRE=force",
	})

	values := make(map[string]string, len(isolated))
	for _, entry := range isolated {
		name, value, _ := strings.Cut(entry, "=")
		values[name] = value
	}

	for _, name := range []string{"GIT_CONFIG_SYSTEM", "GIT_ASKPASS", "SSH_ASKPASS", "SSH_ASKPASS_REQUIRE"} {
		if _, ok := values[name]; ok {
			t.Errorf("%s must be dropped from the isolated environment", name)
		}
	}
	if got := values["GIT_CONFIG_GLOBAL"]; got != os.DevNull {
		t.Errorf("GIT_CONFIG_GLOBAL = %q, want %q", got, os.DevNull)
	}
	if got := values["GIT_CONFIG_NOSYSTEM"]; got != "1" {
		t.Errorf("GIT_CONFIG_NOSYSTEM = %q, want %q", got, "1")
	}
	if got := values["GIT_TERMINAL_PROMPT"]; got != "0" {
		t.Errorf("GIT_TERMINAL_PROMPT = %q, want %q", got, "0")
	}
	if got := values["PATH"]; got != "/usr/bin" {
		t.Errorf("PATH = %q, want the ambient value preserved", got)
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
