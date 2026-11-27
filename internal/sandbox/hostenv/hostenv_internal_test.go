package hostenv

import (
	"errors"
	"os/exec"
	"testing"
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
