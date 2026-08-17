package viperconfig

import (
	"testing"
	"time"

	"github.com/spf13/cobra"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/testutil"
)

func TestResolverGettersReturnConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cfg := Config{
		CPUs: 4, Memory: "8G", TmpSize: "4G", DiskSize: "32G",
		Yes: true, Verbose: true,
		AutoPruneAge: 7 * 24 * time.Hour, ManualPruneAge: 14 * 24 * time.Hour,
		AutoStopOnActiveSessions: true, AutoStopTimeout: 30 * time.Second, AutoStopMaxSessionRetries: 5,
	}
	r := NewResolverWithConfig(cfg)
	if r.CPUs() != 4 || r.Memory() != "8G" || r.TmpSize() != "4G" || r.DiskSize() != "32G" {
		t.Errorf("resource getters mismatch: %+v", cfg)
	}
	if !r.Yes() || !r.Verbose() || r.Quiet() {
		t.Error("UI getters mismatch")
	}
	if r.AutoPruneAge() != 7*24*time.Hour || r.ManualPruneAge() != 14*24*time.Hour {
		t.Error("prune getters mismatch")
	}
	if !r.AutoStopOnActiveSessions() || r.AutoStopTimeout() != 30*time.Second || r.AutoStopMaxSessionRetries() != 5 {
		t.Error("autostop getters mismatch")
	}
	if r.IdleTimeout() != 30*time.Second {
		t.Errorf("IdleTimeout = %v; want 30s", r.IdleTimeout())
	}
}

func TestResolverIdleTimeoutDefault(t *testing.T) {
	r := NewResolverWithConfig(Config{})
	if r.IdleTimeout() != 10*time.Second {
		t.Errorf("IdleTimeout default = %v; want 10s", r.IdleTimeout())
	}
}

func TestResolverEnvPrecedenceOverConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"cpus": 2})
	t.Setenv("OPENCODE_SANDBOX_CPUS", "6")

	r, err := NewResolver(nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.CPUs() != 6 {
		t.Errorf("CPUs = %d; want 6 (env overrides config)", r.CPUs())
	}
}

func TestResolverConfigNoFlag(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"cpus": 3})

	r, err := NewResolver(nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.CPUs() != 3 {
		t.Errorf("CPUs = %d; want 3", r.CPUs())
	}
}

func TestResolverEnvKeyReplacement(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("OPENCODE_SANDBOX_AUTO_STOP_ON_ACTIVE_SESSIONS", "true")

	r, err := NewResolver(nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if !r.AutoStopOnActiveSessions() {
		t.Error("expected AutoStopOnActiveSessions true from env")
	}
}

func TestResolverEnvInvalidCPUs(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("OPENCODE_SANDBOX_CPUS", "300")

	if _, err := NewResolver(nil); err == nil {
		t.Fatal("expected error for cpus=300 from env")
	}
}

// Flag-over-env-over-config precedence via a real cobra command.
func TestResolverFlagOverridesEnv(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"cpus": 2})
	t.Setenv("OPENCODE_SANDBOX_CPUS", "4")

	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().Uint8("cpus", 0, "")
	if err := root.ParseFlags([]string{"--cpus", "6"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	r, err := NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.CPUs() != 6 {
		t.Errorf("CPUs = %d; want 6 (explicit flag overrides env/config)", r.CPUs())
	}
}

// An unspecified flag with a default must NOT override env/config.
func TestResolverUnspecifiedFlagDefaultDoesNotOverride(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"memory": "8G"})

	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().String("memory", "4G", "") // default 4G, not changed
	r, err := NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.Memory() != "8G" {
		t.Errorf("Memory = %q; want 8G (config beats unspecified flag default)", r.Memory())
	}
}

// With no env/config, an unspecified flag's default is the resolution.
func TestResolverFlagDefaultUsedWhenNothingElse(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().String("memory", "4G", "")
	r, err := NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.Memory() != "4G" {
		t.Errorf("Memory = %q; want 4G (flag default)", r.Memory())
	}
}

// rebuild is not a config-backed key; a config file setting it is ignored.
func TestResolverIgnoresRebuildKey(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"rebuild": true, "cpus": 2})

	r, err := NewResolver(nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.CPUs() != 2 {
		t.Errorf("CPUs = %d; want 2", r.CPUs())
	}
	// There is no Rebuild getter; the field is dropped silently.
}
