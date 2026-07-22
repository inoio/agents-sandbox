package sandbox

import (
	"io"
	"os"
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/log"
)

func TestSecretMapContents(t *testing.T) {
	if secretMap["LITELLM_API_KEY"] != "litellm.inoio.de" {
		t.Errorf("expected LITELLM_API_KEY -> litellm.inoio.de, got %v", secretMap["LITELLM_API_KEY"])
	}
}

func TestBuildSecretsSkipsEmptyEnv(t *testing.T) {
	os.Unsetenv("LITELLM_API_KEY")
	l := log.New(io.Discard, false)
	secrets := BuildSecrets(l)
	if len(secrets) != 0 {
		t.Errorf("expected 0 secrets when no env vars set, got %d", len(secrets))
	}
}

func TestBuildSecretsCreatesEntryForSetEnv(t *testing.T) {
	os.Setenv("LITELLM_API_KEY", "sk-test-123")
	defer os.Unsetenv("LITELLM_API_KEY")

	l := log.New(io.Discard, false)
	secrets := BuildSecrets(l)
	if len(secrets) != 1 {
		t.Fatalf("expected 1 secret, got %d", len(secrets))
	}
	if secrets[0].EnvVar != "LITELLM_API_KEY" {
		t.Errorf("expected EnvVar=LITELLM_API_KEY, got %q", secrets[0].EnvVar)
	}
	if secrets[0].Value != "sk-test-123" {
		t.Errorf("expected Value=sk-test-123, got %q", secrets[0].Value)
	}
	if len(secrets[0].AllowHosts) != 1 || secrets[0].AllowHosts[0] != "litellm.inoio.de" {
		t.Errorf("expected AllowHosts=[litellm.inoio.de], got %v", secrets[0].AllowHosts)
	}
}
