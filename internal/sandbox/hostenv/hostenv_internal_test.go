package hostenv

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

var errNotFound = errors.New("not found")

// echoGitConfig fakes the host git binary by returning a command whose output
// depends on the requested config key.
func echoGitConfig(_ string, args ...string) *exec.Cmd {
	key := ""
	if len(args) > 0 {
		key = args[len(args)-1]
	}
	switch key {
	case "user.name":
		return exec.Command("echo", "Ada")
	case "user.email":
		return exec.Command("echo", "ada@example.com")
	}
	return exec.Command("echo")
}

func TestDefaultsResolvesFromHostSources(t *testing.T) {
	origGetenv, origReadlink, origExec := hostGetenv, hostReadlink, execCommand
	t.Cleanup(func() { hostGetenv, hostReadlink, execCommand = origGetenv, origReadlink, origExec })

	hostGetenv = func(key string) string {
		if key == "TZ" {
			return "Europe/Paris"
		}
		return ""
	}
	hostReadlink = func(string) (string, error) { return "", errNotFound }
	execCommand = echoGitConfig

	got := Defaults(nil)
	if got["TZ"] != "Europe/Paris" {
		t.Errorf("TZ = %q, want Europe/Paris", got["TZ"])
	}
	if got["GIT_AUTHOR_NAME"] != "Ada" || got["GIT_AUTHOR_EMAIL"] != "ada@example.com" {
		t.Errorf("git identity = %v, want Ada <ada@example.com>", got)
	}
}

func TestGitConfigValueSwallowsError(t *testing.T) {
	orig := execCommand
	t.Cleanup(func() { execCommand = orig })
	execCommand = func(string, ...string) *exec.Cmd { return exec.Command("false") }

	if got := gitConfigValue("user.name"); got != "" {
		t.Errorf("gitConfigValue on error = %q, want empty", got)
	}
}

// TestDefaultsSuppressesGitIdentityForProvisionedGitConfig pins that a scope
// provisioning a git config suppresses the identity default end-to-end through
// Defaults, not only at ProvisionedGitConfig.
func TestDefaultsSuppressesGitIdentityForProvisionedGitConfig(t *testing.T) {
	origGetenv, origReadlink, origExec := hostGetenv, hostReadlink, execCommand
	t.Cleanup(func() { hostGetenv, hostReadlink, execCommand = origGetenv, origReadlink, origExec })
	hostGetenv = func(string) string { return "" }
	hostReadlink = func(string) (string, error) { return "", errNotFound }
	execCommand = echoGitConfig

	provisioning := configDirWithHome(t, ".gitconfig")
	if got := Defaults([]configpaths.ConfigScope{{Dir: provisioning}}); len(got) != 0 {
		t.Errorf("a provisioned git config did not suppress the identity: %v", got)
	}

	plain := configDirWithHome(t, ".inputrc")
	got := Defaults([]configpaths.ConfigScope{{Dir: plain}})
	if got["GIT_AUTHOR_NAME"] != "Ada" || got["GIT_AUTHOR_EMAIL"] != "ada@example.com" {
		t.Errorf("a non-provisioning scope = %v, want the host identity", got)
	}
}

// configDirWithHome writes a config.yaml declaring one home: target under a
// fresh temp dir and returns the dir.
func configDirWithHome(t *testing.T, target string) string {
	t.Helper()
	dir := t.TempDir()
	body := "home:\n  " + target + ": /host/file\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}
	return dir
}
