package viperconfig

import (
	"slices"
	"testing"
)

func TestEnvVarName(t *testing.T) {
	tests := []struct {
		prefix string
		key    string
		want   string
	}{
		{EnvPrefix, "cpus", "AGENTS_SANDBOX_CPUS"},
		{LegacyEnvPrefix, "auto-stop-timeout", "OPENCODE_SANDBOX_AUTO_STOP_TIMEOUT"},
		{EnvPrefix, "network.profile", "AGENTS_SANDBOX_NETWORK_PROFILE"},
		{LegacyEnvPrefix, "network.dns-servers", "OPENCODE_SANDBOX_NETWORK_DNS_SERVERS"},
	}
	for _, tt := range tests {
		if got := envVarName(tt.prefix, tt.key); got != tt.want {
			t.Errorf("envVarName(%q, %q) = %q; want %q", tt.prefix, tt.key, got, tt.want)
		}
	}
}

func TestLegacyEnvVarsInUseScansEveryKey(t *testing.T) {
	for _, key := range configEnvKeys {
		t.Setenv(envVarName(LegacyEnvPrefix, key), "")
		t.Setenv(envVarName(EnvPrefix, key), "")
	}
	t.Setenv("OPENCODE_SANDBOX_AGENT", "pi")

	if got := legacyEnvVarsInUse(); !slices.Contains(got, "OPENCODE_SANDBOX_AGENT") {
		t.Fatalf("legacyEnvVarsInUse() = %v, want OPENCODE_SANDBOX_AGENT", got)
	}
}

func TestLegacyEnvVarsInUseSkipsShadowedAndContinues(t *testing.T) {
	// Clear every key the scan reads, so the developer's shell cannot shadow
	// or add a legacy variable.
	for _, key := range slices.Concat(envKeys, []string{"notify"}) {
		t.Setenv(envVarName(LegacyEnvPrefix, key), "")
		t.Setenv(envVarName(EnvPrefix, key), "")
	}
	// The shadowed key is scanned first and the unshadowed one last, so a scan
	// that stopped at the shadowed key is caught.
	shadowed, later := envKeys[0], envKeys[len(envKeys)-1]
	t.Setenv(envVarName(LegacyEnvPrefix, shadowed), "6")
	t.Setenv(envVarName(EnvPrefix, shadowed), "7")
	t.Setenv(envVarName(LegacyEnvPrefix, later), "pi")

	got := legacyEnvVarsInUse()
	if slices.Contains(got, envVarName(LegacyEnvPrefix, shadowed)) {
		t.Error("a shadowed legacy variable should be skipped")
	}
	if !slices.Contains(got, envVarName(LegacyEnvPrefix, later)) {
		t.Fatalf("legacyEnvVarsInUse() = %v, want %s", got, envVarName(LegacyEnvPrefix, later))
	}
}
