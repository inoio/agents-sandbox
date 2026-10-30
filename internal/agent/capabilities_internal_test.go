package agent

import (
	"slices"
	"testing"
)

func TestAgentSpecsShareCredentialFieldMetadata(t *testing.T) {
	for _, name := range []string{opencode1Name, piName, claudeCodeName} {
		a, _ := Lookup(name)
		auth := a.(MigrationSpecProvider).MigrationSpec().Auth
		if !slices.Equal(auth.SensitiveFields, defaultSensitiveAuthFields) {
			t.Errorf("%s SensitiveFields diverges from the shared set", name)
		}
		if !slices.Equal(auth.ConfigSensitiveFields, defaultConfigSensitiveFields) {
			t.Errorf("%s ConfigSensitiveFields diverges from the shared set", name)
		}
	}
}
