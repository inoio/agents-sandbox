package configmigration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestMigrationAlreadyConfiguredChecksSupplementalFiles(t *testing.T) {
	a, _ := agent.Lookup("pi")
	spec := a.(agent.MigrationSpecProvider).MigrationSpec()
	tests := map[string]struct {
		writeManagedModels bool
		homeMapping        string
		want               bool
	}{
		"missing managed models file": {homeMapping: "pi/models.json"},
		"home mapping to other file":  {writeManagedModels: true, homeMapping: "other/models.json"},
		"complete supplemental setup": {writeManagedModels: true, homeMapping: "pi/models.json", want: true},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			managedDir := configpaths.Get().UserAgentConfigDir(a)
			if test.writeManagedModels {
				testutil.WriteFile(t, managedDir, "models.json", "{}")
			}
			writeUserLauncherConfig(t, "config.yaml", "home:\n  .pi/agent/models.json: "+test.homeMapping+"\n")
			configPath := filepath.Join(configpaths.Get().UserConfigDir(), "config.yaml")
			got := migrationAlreadyConfigured(
				a, spec, "", filepath.Join(managedDir, spec.ManagedSnippet), configPath,
				false, false, []string{"models.json"},
			)
			if got != test.want {
				t.Errorf("migrationAlreadyConfigured = %v, want %v", got, test.want)
			}
		})
	}
}

func TestBuildFailsOnSecretNameCollisionWithSupplementalConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMigrationSeams(t)
	originalSupplemental := migrateNativeSupplementalConfigFn
	t.Cleanup(func() { migrateNativeSupplementalConfigFn = originalSupplemental })
	t.Setenv("PI_CODING_AGENT_DIR", "")
	hostHome := t.TempDir()
	piDir := filepath.Join(hostHome, ".pi", "agent")
	if err := os.MkdirAll(piDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, piDir, "settings.json", "{}")
	migrateNativeConfigFn = func(string, agent.ConfigMigrationSpec) ([]byte, map[string]Secret, []string, map[string]string, error) {
		return []byte("{}\n"), map[string]Secret{"PI_SHARED": {Value: "from-settings"}}, nil, nil, nil
	}
	migrateNativeSupplementalConfigFn = func(string, agent.ConfigMigrationSpec) (map[string][]byte, map[string]Secret, []string, map[string]string, error) {
		return nil, map[string]Secret{"PI_SHARED": {Value: "from-models"}}, nil, nil, nil
	}
	a, _ := agent.Lookup("pi")
	if _, err := Build(a, hostHome); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Errorf("Build error = %v, want a secret name collision", err)
	}
}

func TestMigrationEnvPathResolvesAgainstHostHome(t *testing.T) {
	hostHome := t.TempDir()
	for _, value := range []string{"~/pi-agent", "pi-agent", " ~/pi-agent "} {
		if got, want := migrationEnvPath(hostHome, value, true, ""), filepath.Join(hostHome, "pi-agent"); got != want {
			t.Errorf("migrationEnvPath(%q) = %q, want %q", value, got, want)
		}
	}
}
