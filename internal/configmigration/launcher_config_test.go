package configmigration

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

const commentedLauncherConfig = `# launcher settings
upgrade: never
network:
  profile: none # deny by default
  egress-allow:
    # needed for the docs
    - existing.test
agent: opencode
`

func assertCommentsAndOrderPreserved(t *testing.T, updated string) {
	t.Helper()
	for _, comment := range []string{"# launcher settings", "# deny by default", "# needed for the docs"} {
		if !strings.Contains(updated, comment) {
			t.Errorf("updated config lost comment %q:\n%s", comment, updated)
		}
	}
	if strings.Index(updated, "upgrade:") > strings.Index(updated, "network:") ||
		strings.Index(updated, "network:") > strings.Index(updated, "agent:") {
		t.Errorf("updated config changed the key order:\n%s", updated)
	}
}

func writeUserLauncherConfig(t *testing.T, name, content string) {
	t.Helper()
	userConfigDir := configpaths.Get().UserConfigDir()
	if err := os.MkdirAll(userConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, userConfigDir, name, content)
}

func TestBuildLauncherConfigPreservesYAMLCommentsAndOrder(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeUserLauncherConfig(t, "config.yaml", commentedLauncherConfig)
	a, _ := agent.Lookup("opencode")
	provider, _ := agent.AsMigrationSpecProvider(a)
	_, updated, err := buildLauncherConfig(a, provider.MigrationSpec())
	if err != nil {
		t.Fatalf("buildLauncherConfig: %v", err)
	}
	assertCommentsAndOrderPreserved(t, string(updated))
	if !strings.Contains(string(updated), ".local/share/opencode/auth.json: opencode/auth.json") {
		t.Errorf("updated config has no auth mapping:\n%s", updated)
	}
}

func TestUpdateLauncherNetworkPreservesYAMLCommentsAndOrder(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	_, updated, added, err := updateLauncherNetwork(
		"config.yaml",
		[]byte(commentedLauncherConfig),
		[]string{"existing.test", "new.test"},
	)
	if err != nil {
		t.Fatalf("updateLauncherNetwork: %v", err)
	}
	assertCommentsAndOrderPreserved(t, string(updated))
	if !slicesEqual(added, []string{"new.test"}) {
		t.Errorf("added = %v, want [new.test]", added)
	}
	if strings.Index(string(updated), "existing.test") > strings.Index(string(updated), "new.test") {
		t.Errorf("new host was not appended after the existing ones:\n%s", updated)
	}
}

func TestUpdateLauncherNetworkReplacesCommaSeparatedYAMLString(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	_, updated, _, err := updateLauncherNetwork(
		"config.yaml",
		[]byte("network:\n  egress-allow: \"a.test, b.test\"\n"),
		[]string{"c.test"},
	)
	if err != nil {
		t.Fatalf("updateLauncherNetwork: %v", err)
	}
	want := "network:\n  egress-allow:\n    - a.test\n    - b.test\n    - c.test\n"
	if string(updated) != want {
		t.Errorf("updated config =\n%s\nwant\n%s", updated, want)
	}
}

func TestUpdateLauncherNetworkStartsBlockStyleConfigFromEmptyDefault(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	_, updated, _, err := updateLauncherNetwork("config.yaml", []byte("{}\n"), []string{"a.test"})
	if err != nil {
		t.Fatalf("updateLauncherNetwork: %v", err)
	}
	if want := "network:\n  egress-allow:\n    - a.test\n"; string(updated) != want {
		t.Errorf("updated config =\n%s\nwant\n%s", updated, want)
	}
}

func TestEnableNativeProvisioningPreservesYAMLCommentsAndOrder(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeUserLauncherConfig(t, "config.yaml", commentedLauncherConfig)
	if err := EnableNativeProvisioning(); err != nil {
		t.Fatalf("EnableNativeProvisioning: %v", err)
	}
	updated, err := os.ReadFile(filepath.Join(configpaths.Get().UserConfigDir(), "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	assertCommentsAndOrderPreserved(t, string(updated))
	if !strings.Contains(string(updated), "provision-host-config: true") {
		t.Errorf("updated config does not enable provisioning:\n%s", updated)
	}
}

func TestReviewWarnsThatJSONLauncherConfigLosesComments(t *testing.T) {
	plan := &Plan{
		ConfigPath: "/home/user/.config/agents-sandbox/config.jsonc",
		ConfigData: []byte("{}\n"),
		SecretPath: "/tmp/env.secret.yaml",
	}
	ui := termio.NewTestMock(t)
	if err := Review(plan, &ui); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, "\n"), "comments") {
		t.Errorf("warnings = %v, want a warning about lost comments", ui.WarnCalls)
	}
}

func TestReviewDoesNotWarnAboutCommentsForYAMLLauncherConfig(t *testing.T) {
	plan := &Plan{
		ConfigPath: "/home/user/.config/agents-sandbox/config.yaml",
		ConfigData: []byte("{}\n"),
		SecretPath: "/tmp/env.secret.yaml",
	}
	ui := termio.NewTestMock(t)
	if err := Review(plan, &ui); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if strings.Contains(strings.Join(ui.WarnCalls, "\n"), "comments") {
		t.Errorf("warnings = %v, want no warning about comments for YAML", ui.WarnCalls)
	}
}

func TestGuideWarnsBeforeEnablingProvisioningInJSONLauncherConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	withMigrationSeams(t)
	enableNativeProvisioningFn = func() error { return nil }
	writeUserLauncherConfig(t, "config.jsonc", "{\n  // comment\n}\n")
	hostHome := t.TempDir()
	writeNativeAuth(t, hostHome)
	a, _ := agent.Lookup("opencode")
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return "p", nil }
	_ = Guide(a, hostHome, false, &ui)
	if !strings.Contains(strings.Join(ui.WarnCalls, "\n"), "comments") {
		t.Errorf("warnings = %v, want a warning about lost comments", ui.WarnCalls)
	}
}

func TestParseLauncherConfigRejectsNonMappingYAML(t *testing.T) {
	if _, err := parseLauncherConfig("config.yaml", []byte("- a.test\n")); err == nil {
		t.Fatal("parseLauncherConfig succeeded for a YAML sequence, want an error")
	}
}

func TestLauncherConfigSetStringStartsEmptyYAMLFile(t *testing.T) {
	for name, data := range map[string]string{"empty file": "", "null document": "~\n"} {
		t.Run(name, func(t *testing.T) {
			config, err := parseLauncherConfig("config.yaml", []byte(data))
			if err != nil {
				t.Fatalf("parseLauncherConfig: %v", err)
			}
			config.setString([]string{"home", ".config/tool/rc"}, "tool/rc")
			updated, err := config.marshal()
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if want := "home:\n  .config/tool/rc: tool/rc\n"; string(updated) != want {
				t.Errorf("updated config =\n%s\nwant\n%s", updated, want)
			}
		})
	}
}

func TestLauncherConfigSetBoolWritesYAMLBoolean(t *testing.T) {
	config, err := parseLauncherConfig("config.yaml", []byte("agent: pi\n"))
	if err != nil {
		t.Fatalf("parseLauncherConfig: %v", err)
	}
	config.setBool([]string{"provision-host-config"}, true)
	updated, err := config.marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := "agent: pi\nprovision-host-config: true\n"; string(updated) != want {
		t.Errorf("updated config =\n%s\nwant\n%s", updated, want)
	}
}

func TestLauncherConfigMarshalReportsJSONError(t *testing.T) {
	config, err := parseLauncherConfig("config.json", []byte("{}"))
	if err != nil {
		t.Fatalf("parseLauncherConfig: %v", err)
	}
	marshalErr := errors.New("marshal failed")
	originalMarshal := marshalJSONIndentFn
	t.Cleanup(func() { marshalJSONIndentFn = originalMarshal })
	marshalJSONIndentFn = func(any, string, string) ([]byte, error) { return nil, marshalErr }
	if _, err := config.marshal(); !errors.Is(err, marshalErr) {
		t.Errorf("marshal error = %v, want %v", err, marshalErr)
	}
}

type failingYAMLMarshaler struct{}

var errYAMLMarshal = errors.New("yaml marshal failed")

func (failingYAMLMarshaler) MarshalYAML() (any, error) { return nil, errYAMLMarshal }

func TestMarshalYAMLReportsEncodeError(t *testing.T) {
	if _, err := marshalYAML(failingYAMLMarshaler{}); !errors.Is(err, errYAMLMarshal) {
		t.Errorf("marshalYAML error = %v, want %v", err, errYAMLMarshal)
	}
}
