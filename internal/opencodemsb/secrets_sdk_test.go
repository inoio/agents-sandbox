//go:build cgo

package opencodemsb

import (
	"testing"

	m "github.com/superradcompany/microsandbox/sdk/go"
)

func TestSecretEntryMatchesSDKFactory(t *testing.T) {
	entry := m.Secret.Env("TEST_VAR", "val", m.SecretEnvOptions{AllowHosts: []string{"example.com"}})
	if entry.EnvVar != "TEST_VAR" {
		t.Errorf("expected EnvVar=TEST_VAR, got %q", entry.EnvVar)
	}
}
