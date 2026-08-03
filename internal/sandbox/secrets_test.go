package sandbox

import (
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
)

func TestBuildSecretsSkipsEmptyEnv(t *testing.T) {
	testUI := testutil.NewTestio(t)
	secrets := BuildSecrets(make(map[string]string), &testUI)
	if len(secrets) != 0 {
		t.Errorf("expected 0 secrets when no env vars set, got %d", len(secrets))
	}
}

func TestBuildSecretsCreatesEntryForSetEnv(t *testing.T) {
	env := make(map[string]string)

	env["FOO"] = "bar@example.org"

	testUI := testutil.NewTestio(t)
	secrets := BuildSecrets(env, &testUI)
	if len(secrets) != 1 {
		t.Fatalf("expected 1 secret, got %d", len(secrets))
	}
	if secrets[0].EnvVar != "FOO" {
		t.Errorf("expected EnvVar=FOO, got %q", secrets[0].EnvVar)
	}
	if secrets[0].Value != "bar" {
		t.Errorf("expected Value=bar, got %q", secrets[0].Value)
	}
	if len(secrets[0].AllowHosts) != 1 || secrets[0].AllowHosts[0] != "example.org" {
		t.Errorf("expected AllowHosts=[example.org], got %v", secrets[0].AllowHosts)
	}
}
