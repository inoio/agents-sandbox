package viperconfig

import (
	"slices"
	"testing"
)

// TestEnvVarNames pins the canonical-then-legacy order a caller needs to check
// whether config key K is supplied by the environment.
func TestEnvVarNames(t *testing.T) {
	got := EnvVarNames("memory")
	want := []string{"AGENTS_SANDBOX_MEMORY", "OPENCODE_SANDBOX_MEMORY"}
	if !slices.Equal(got, want) {
		t.Errorf("EnvVarNames(memory) = %v; want %v", got, want)
	}
}

// TestEnvVarNamesDottedKey pins that separators map to underscores.
func TestEnvVarNamesDottedKey(t *testing.T) {
	got := EnvVarNames("network.profile")
	want := []string{"AGENTS_SANDBOX_NETWORK_PROFILE", "OPENCODE_SANDBOX_NETWORK_PROFILE"}
	if !slices.Equal(got, want) {
		t.Errorf("EnvVarNames(network.profile) = %v; want %v", got, want)
	}
}
