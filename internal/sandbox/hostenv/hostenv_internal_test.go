package hostenv

import (
	"errors"
	"testing"
)

var errNotFound = errors.New("not found")

func TestHostGitIdentityReadsGlobalConfig(t *testing.T) {
	orig := gitRunner
	t.Cleanup(func() { gitRunner = orig })
	gitRunner = func(args ...string) (string, error) {
		switch args[len(args)-1] {
		case "user.name":
			return "Ada\n", nil
		case "user.email":
			return " ada@example.com \n", nil
		}
		return "", nil
	}

	name, email := hostGitIdentity()
	if name != "Ada" || email != "ada@example.com" {
		t.Errorf("hostGitIdentity() = (%q, %q), want (Ada, ada@example.com)", name, email)
	}
}

func TestGitConfigValueSwallowsError(t *testing.T) {
	orig := gitRunner
	t.Cleanup(func() { gitRunner = orig })
	gitRunner = func(...string) (string, error) { return "", errNotFound }

	if got := gitConfigValue("user.name"); got != "" {
		t.Errorf("gitConfigValue on error = %q, want empty", got)
	}
}
