package viperconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/network"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/testutil"
)

func TestResolverGettersReturnConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cfg := Config{
		CPUs: 4, Memory: "8G", TmpSize: "4G", DiskSize: "32G", WorkspaceQuota: "64G",
		Yes: true, LogLevel: "verbose", Quiet: true,
		AutoPruneAge: 7 * 24 * time.Hour, ManualPruneAge: 14 * 24 * time.Hour,
		AutoStopOnActiveSessions: true, AutoStopTimeout: 30 * time.Second, AutoStopMaxSessionRetries: 5,
	}
	r := NewResolverWithConfig(cfg)
	if r.CPUs() != 4 || r.Memory() != "8G" || r.TmpSize() != "4G" || r.DiskSize() != "32G" ||
		r.WorkspaceQuota() != "64G" {
		t.Errorf("resource getters mismatch: %+v", cfg)
	}
	if !r.Yes() || r.LogLevel() != "verbose" || !r.Quiet() {
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

	r, err := NewResolver(nil, "")
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

	r, err := NewResolver(nil, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.CPUs() != 3 {
		t.Errorf("CPUs = %d; want 3", r.CPUs())
	}
}

func TestResolverConfigSyntaxErrorIsFriendly(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.UserConfigDir(), "config.yaml", "cpus: 2\n  memory: [\n")

	_, err := NewResolver(nil, "")
	if err == nil {
		t.Fatal("expected error for malformed config.yaml")
	}
	for _, want := range []string{"config.yaml", "invalid YAML", "line 2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestResolverEnvKeyReplacement(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("OPENCODE_SANDBOX_AUTO_STOP_ON_ACTIVE_SESSIONS", "true")

	r, err := NewResolver(nil, "")
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

	if _, err := NewResolver(nil, ""); err == nil {
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

	r, err := NewResolver(root, "")
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
	r, err := NewResolver(root, "")
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
	r, err := NewResolver(root, "")
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

	r, err := NewResolver(nil, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.CPUs() != 2 {
		t.Errorf("CPUs = %d; want 2", r.CPUs())
	}
	// There is no Rebuild getter; the field is dropped silently.
}

func TestResolverProjectOverridesUser(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{
		"cpus":   2,
		"memory": "4G",
		"yes":    true,
	})
	testutil.WriteYAML(t, cp.ProjectConfigDir(), "config.yaml", map[string]any{
		"memory": "8G",
		"yes":    false,
	})

	r, err := NewResolver(nil, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.CPUs() != 2 {
		t.Errorf("CPUs = %d; want 2 from user config", r.CPUs())
	}
	if r.Memory() != "8G" {
		t.Errorf("Memory = %q; want 8G from project override", r.Memory())
	}
	if r.Yes() {
		t.Error("expected yes=false from project override")
	}
}

func TestResolverInvalidPruneAgeFromFile(t *testing.T) {
	for _, tc := range []struct {
		key       string
		value     string
		errSuffix string
	}{
		{key: "auto-prune-age", value: "-1d", errSuffix: "auto-prune-age must be > 0"},
		{key: "manual-prune-age", value: "-10h", errSuffix: "manual-prune-age must be > 0"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			cp := configpaths.Get()
			testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{tc.key: tc.value})

			_, err := NewResolver(nil, "")
			if err == nil {
				t.Fatalf("expected error for %s=%s", tc.key, tc.value)
			}
			if !strings.Contains(err.Error(), tc.errSuffix) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.errSuffix)
			}
		})
	}
}

func TestResolverInvalidAutoStopRetriesFromFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{
		"auto-stop-max-session-retries": -1,
	})

	_, err := NewResolver(nil, "")
	if err == nil {
		t.Fatal("expected error for negative auto-stop-max-session-retries")
	}
	if !strings.Contains(err.Error(), "auto-stop-max-session-retries") {
		t.Errorf("error %q does not mention auto-stop-max-session-retries", err.Error())
	}
}

func TestResolverJSON5Config(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.UserConfigDir(), "config.json5", `{
		// a comment
		"cpus": 2,
		"memory": "512M",
		"yes": true
	}`)

	r, err := NewResolver(nil, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.CPUs() != 2 || r.Memory() != "512M" || !r.Yes() {
		t.Errorf("unexpected config: cpus=%d memory=%q yes=%v", r.CPUs(), r.Memory(), r.Yes())
	}
}

func TestResolverDiskSizeConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"disk-size": "24G"})

	r, err := NewResolver(nil, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.DiskSize() != "24G" {
		t.Errorf("DiskSize = %q; want 24G", r.DiskSize())
	}
}

func TestResolverWorkspaceQuotaConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"workspace-quota": "32G"})

	r, err := NewResolver(nil, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.WorkspaceQuota() != "32G" {
		t.Errorf("WorkspaceQuota = %q; want 32G", r.WorkspaceQuota())
	}
}

func TestResolverWorkspaceQuotaEnv(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("OPENCODE_SANDBOX_WORKSPACE_QUOTA", "48G")

	r, err := NewResolver(nil, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.WorkspaceQuota() != "48G" {
		t.Errorf("WorkspaceQuota = %q; want 48G from env", r.WorkspaceQuota())
	}
}

func TestResolverWorkspaceQuotaGetter(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cfg := Config{WorkspaceQuota: "16G"}
	r := NewResolverWithConfig(cfg)
	if r.WorkspaceQuota() != "16G" {
		t.Errorf("WorkspaceQuota = %q; want 16G", r.WorkspaceQuota())
	}
}

func TestResolverReapPolicyDefaults(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	r, err := NewResolver(nil, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	rp := options.NewReapPolicy(r.AutoStopOnActiveSessions(), r.AutoStopMaxSessionRetries())
	if rp.AutoStopOnActiveSessions {
		t.Error("expected AutoStopOnActiveSessions false by default")
	}
	if rp.MaxSessionRetries != 10 {
		t.Errorf("expected MaxSessionRetries 10 by default, got %d", rp.MaxSessionRetries)
	}
}

func TestResolverIdleTimeoutDefaultFromConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	r, err := NewResolver(nil, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.IdleTimeout() != 10*time.Second {
		t.Errorf("IdleTimeout default = %v; want 10s", r.IdleTimeout())
	}
}

func TestNetworkProfileEnvVar(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("OPENCODE_SANDBOX_NETWORK_PROFILE", "none")
	r, err := NewResolver(nil, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if got := r.Network(); got.Profile != network.ProfileNone {
		t.Fatalf("Network().Profile = %q, want %q", got.Profile, network.ProfileNone)
	}
}

func TestNetworkDefaultEmpty(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	r, err := NewResolver(nil, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if !r.Network().Empty() {
		t.Error("with no network config, Network() should be Empty (default public)")
	}
}

func TestNetworkInvalidProfileRejected(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("OPENCODE_SANDBOX_NETWORK_PROFILE", "bogus")
	if _, err := NewResolver(nil, ""); err == nil {
		t.Fatal("expected error for invalid network profile")
	}
}

func TestPerSlugConfigPrecedence(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()

	// generic user: cpus=2; per-slug user: cpus=3; project: cpus=4
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"cpus": 2})
	if err := os.MkdirAll(filepath.Join(cp.UserConfigDir(), "myslug"), 0o755); err != nil {
		t.Fatalf("mkdir per-slug: %v", err)
	}
	testutil.WriteYAML(t, filepath.Join(cp.UserConfigDir(), "myslug"), "config.yaml", map[string]any{"cpus": 3})
	testutil.WriteYAML(t, cp.ProjectConfigDir(), "config.yaml", map[string]any{"cpus": 4})

	r, err := NewResolver(nil, "myslug")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.CPUs() != 4 {
		t.Fatalf("CPUs = %d; want 4 (project overrides per-slug user)", r.CPUs())
	}

	// Remove the project file; per-slug user must now win over generic user.
	os.Remove(filepath.Join(cp.ProjectConfigDir(), "config.yaml"))
	r2, err := NewResolver(nil, "myslug")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r2.CPUs() != 3 {
		t.Fatalf("CPUs = %d; want 3 (per-slug user overrides generic user)", r2.CPUs())
	}
}
