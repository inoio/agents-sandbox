package viperconfig

import (
	"slices"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestEnvVarName(t *testing.T) {
	tests := []struct {
		prefix string
		key    string
		want   string
	}{
		{envPrefix, "cpus", "AGENTS_SANDBOX_CPUS"},
		{legacyEnvPrefix, "auto-stop-timeout", "OPENCODE_SANDBOX_AUTO_STOP_TIMEOUT"},
		{envPrefix, "network.profile", "AGENTS_SANDBOX_NETWORK_PROFILE"},
		{legacyEnvPrefix, "network.dns-servers", "OPENCODE_SANDBOX_NETWORK_DNS_SERVERS"},
	}
	for _, tt := range tests {
		if got := envVarName(tt.prefix, tt.key); got != tt.want {
			t.Errorf("envVarName(%q, %q) = %q; want %q", tt.prefix, tt.key, got, tt.want)
		}
	}
}

func TestResolverAgentsSandboxEnvPrefix(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"cpus": 2})
	t.Setenv("AGENTS_SANDBOX_CPUS", "6")

	r, err := NewResolver(nil, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.CPUs != 6 {
		t.Errorf("CPUs = %d; want 6 (AGENTS_SANDBOX_ env overrides config)", r.cfg.CPUs)
	}
	if got := r.LegacyEnvVars(); len(got) != 0 {
		t.Errorf("LegacyEnvVars = %v; want none", got)
	}
}

func TestResolverLegacyEnvPrefixStillWorks(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("OPENCODE_SANDBOX_CPUS", "6")

	r, err := NewResolver(nil, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.CPUs != 6 {
		t.Errorf("CPUs = %d; want 6 (legacy env still resolves)", r.cfg.CPUs)
	}
	if got := r.LegacyEnvVars(); !slices.Contains(got, "OPENCODE_SANDBOX_CPUS") {
		t.Errorf("LegacyEnvVars = %v; want it to contain OPENCODE_SANDBOX_CPUS", got)
	}
}

func TestResolverAgentsEnvTakesPrecedenceOverLegacy(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_CPUS", "7")
	t.Setenv("OPENCODE_SANDBOX_CPUS", "6")

	r, err := NewResolver(nil, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.CPUs != 7 {
		t.Errorf("CPUs = %d; want 7 (AGENTS_ wins over legacy)", r.cfg.CPUs)
	}
	if got := r.LegacyEnvVars(); len(got) != 0 {
		t.Errorf("LegacyEnvVars = %v; want none when the new prefix is set", got)
	}
}

func TestResolverDottedKeyUsesAgentsEnvPrefix(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_NETWORK_PROFILE", "private")

	r, err := NewResolver(nil, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.Network.Profile != "private" {
		t.Errorf("Network.Profile = %q; want private", r.cfg.Network.Profile)
	}
}
