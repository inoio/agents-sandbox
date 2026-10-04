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

// specOnlyMigrationAgent provides migration metadata but no snippet merge, so
// it can never hold managed configuration.
type specOnlyMigrationAgent struct {
	unsupportedMigrationAgent

	spec agent.ConfigMigrationSpec
}

func (a specOnlyMigrationAgent) MigrationSpec() agent.ConfigMigrationSpec { return a.spec }

var errInjected = errors.New("injected failure")

// makeUnreadableLauncherConfig turns the user launcher config into a
// directory, so reading it fails with an error other than "not exist".
func makeUnreadableLauncherConfig(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(configpaths.Get().UserConfigDir(), "config.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func denyAllEgressInLauncherConfig(t *testing.T) {
	t.Helper()
	writeUserLauncherConfig(t, "config.yaml", "network:\n  egress-deny:\n    - \"*\"\n")
}

func TestMigrationMergeSecretFileReportsMarshalError(t *testing.T) {
	withMigrationSeams(t)
	marshalYAMLFn = func(any) ([]byte, error) { return nil, errInjected }
	_, err := migrationMergeSecretFile(filepath.Join(t.TempDir(), "env.secret.yaml"), map[string]Secret{})
	if !errors.Is(err, errInjected) {
		t.Errorf("error = %v, want %v", err, errInjected)
	}
}

func TestWriteMigrationManifestReportsMarshalError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMigrationSeams(t)
	marshalYAMLFn = func(any) ([]byte, error) { return nil, errInjected }
	if err := writeMigrationManifest(migrationManifest{Agent: "opencode"}); !errors.Is(err, errInjected) {
		t.Errorf("error = %v, want %v", err, errInjected)
	}
}

func TestMigrationAtomicWriteFailsWhenParentIsAFile(t *testing.T) {
	parentFile := filepath.Join(t.TempDir(), "file")
	testutil.WritePath(t, parentFile, "")
	if err := migrationAtomicWrite(filepath.Join(parentFile, "out.yaml"), []byte("x"), 0o600); err == nil {
		t.Error("migrationAtomicWrite succeeded below a regular file, want an error")
	}
}

func TestMigrationAtomicWriteFailsInReadOnlyDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := migrationAtomicWrite(filepath.Join(dir, "out.yaml"), []byte("x"), 0o600); err == nil {
		t.Error("migrationAtomicWrite succeeded in a read-only directory, want an error")
	}
}

func TestCommitTempFileReportsErrorsOfClosedFile(t *testing.T) {
	dir := t.TempDir()
	file, err := os.CreateTemp(dir, "closed-*")
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if err := commitTempFile(file, filepath.Join(dir, "out.yaml"), []byte("x"), 0o600); !errors.Is(err, os.ErrClosed) {
		t.Errorf("error = %v, want %v", err, os.ErrClosed)
	}
}

func TestMigrationAlreadyConfiguredIsFalseWhenPrerequisitesFail(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	spec := a.(agent.MigrationSpecProvider).MigrationSpec()
	tests := map[string]struct {
		configFile, content string
		configRequired      bool
		unreadableConfig    bool
	}{
		"missing managed snippet":    {configRequired: true},
		"unreadable launcher config": {unreadableConfig: true},
		"malformed JSON config":      {configFile: "config.json", content: "{"},
		"malformed YAML config":      {configFile: "config.yaml", content: "home: ["},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			if test.unreadableConfig {
				makeUnreadableLauncherConfig(t)
			}
			configPath := filepath.Join(configpaths.Get().UserConfigDir(), "config.yaml")
			if test.configFile != "" {
				writeUserLauncherConfig(t, test.configFile, test.content)
				configPath = filepath.Join(configpaths.Get().UserConfigDir(), test.configFile)
			}
			managedConfigPath := filepath.Join(t.TempDir(), "missing.jsonc")
			if migrationAlreadyConfigured(a, spec, "", managedConfigPath, configPath, false, test.configRequired, nil) {
				t.Error("migrationAlreadyConfigured = true, want false")
			}
		})
	}
}

func TestBuildFailsWhenProviderHostIsDenied(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	denyAllEgressInLauncherConfig(t)
	hostHome := t.TempDir()
	writeNativeAuth(t, hostHome)
	a, _ := agent.Lookup("opencode")
	if _, err := Build(a, hostHome); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Errorf("Build error = %v, want a denied host error", err)
	}
}

func TestBuildFailsOnSecretNameCollisionBetweenConfigAndAuth(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	withMigrationSeams(t)
	hostHome := t.TempDir()
	writeNativeAuth(t, hostHome)
	migrateNativeConfigFn = func(string, agent.ConfigMigrationSpec) ([]byte, map[string]Secret, []string, map[string]string, error) {
		return []byte("{}\n"), map[string]Secret{"OPENCODE_SHARED": {Value: "from-config"}}, nil, nil, nil
	}
	sanitizeAuthWithSpecFn = func([]byte, agent.ConfigMigrationSpec, map[string]string, map[string]string) ([]byte, map[string]Secret, []string, error) {
		return []byte("{}\n"), map[string]Secret{"OPENCODE_SHARED": {Value: "from-auth"}}, nil, nil
	}
	a, _ := agent.Lookup("opencode")
	if _, err := Build(a, hostHome); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Errorf("Build error = %v, want a secret name collision", err)
	}
}

func TestBuildLauncherConfigForPlanReportsReadErrors(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMigrationSeams(t)
	a, _ := agent.Lookup("opencode")
	spec := a.(agent.MigrationSpecProvider).MigrationSpec()

	missingPath := filepath.Join(t.TempDir(), "config.yaml")
	buildLauncherConfigFn = func(agent.Agent, agent.ConfigMigrationSpec) (string, []byte, error) {
		return missingPath, nil, nil
	}
	if _, _, err := buildLauncherConfigForPlan(a, spec, true); err == nil {
		t.Error("unchanged but unreadable launcher config: want an error")
	}

	makeUnreadableLauncherConfig(t)
	if _, _, err := buildLauncherConfigForPlan(a, spec, false); err == nil {
		t.Error("unreadable launcher config without credential mapping: want an error")
	}
}

func TestUpdateLauncherNetworkReportsMarshalError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMigrationSeams(t)
	marshalJSONIndentFn = func(any, string, string) ([]byte, error) { return nil, errInjected }
	if _, _, _, err := updateLauncherNetwork(
		"config.json",
		[]byte("{}"),
		[]string{"a.test"},
	); !errors.Is(
		err,
		errInjected,
	) {
		t.Errorf("error = %v, want %v", err, errInjected)
	}
}

func TestProjectNetworkDenyHosts(t *testing.T) {
	tests := map[string]struct {
		file, content string
		want          []string
	}{
		"JSON project config": {
			file:    "config.json",
			content: `{"network":{"egress-deny":["a.test"]}}`,
			want:    []string{"a.test"},
		},
		"YAML project config": {
			file:    "config.yaml",
			content: "network:\n  egress-deny: [b.test]\n",
			want:    []string{"b.test"},
		},
		"malformed JSON":         {file: "config.json", content: "{"},
		"malformed YAML":         {file: "config.yaml", content: "network: ["},
		"config without network": {file: "config.yaml", content: "agent: pi\n"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			testutil.WriteFile(t, configpaths.Get().ProjectConfigDir(), test.file, test.content)
			if got := projectNetworkDenyHosts(); !slicesEqual(got, test.want) {
				t.Errorf("projectNetworkDenyHosts() = %v, want %v", got, test.want)
			}
		})
	}
}

func newApplyTestPlan(t *testing.T) *Plan {
	t.Helper()
	dir := t.TempDir()
	return &Plan{
		AgentName:  "opencode",
		SourceHash: "hash",
		SecretPath: filepath.Join(dir, "env.secret.yaml"),
		Files:      []File{{Path: filepath.Join(dir, "auth.json"), Data: []byte("{}\n"), Mode: 0o600}},
	}
}

func TestApplyReportsWriteErrors(t *testing.T) {
	tests := map[string]struct {
		fails     func(path string, data []byte) bool
		wantError string
	}{
		"migrated file": {
			fails:     func(path string, _ []byte) bool { return strings.HasSuffix(path, "auth.json") },
			wantError: "write migrated file",
		},
		"completed manifest": {
			fails: func(path string, data []byte) bool {
				return strings.HasSuffix(path, statusFileName) &&
					strings.Contains(string(data), "state: "+migrationStateCompleted)
			},
			wantError: "complete migration state",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			withMigrationSeams(t)
			atomicWriteFn = func(path string, data []byte, mode os.FileMode) error {
				if test.fails(path, data) {
					return errInjected
				}
				return migrationAtomicWrite(path, data, mode)
			}
			ui := termio.NewTestMock(t)
			err := Apply(newApplyTestPlan(t), &ui)
			if !errors.Is(err, errInjected) || !strings.Contains(err.Error(), test.wantError) {
				t.Errorf("Apply error = %v, want %q wrapping %v", err, test.wantError, errInjected)
			}
		})
	}
}

func TestReviewRejectsNilPlan(t *testing.T) {
	ui := termio.NewTestMock(t)
	if err := Review(nil, &ui); err == nil {
		t.Error("Review(nil) succeeded, want an error")
	}
}

func TestReviewFailsWhenSecretHostIsDenied(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	denyAllEgressInLauncherConfig(t)
	plan := &Plan{
		AgentName:  "opencode",
		SecretPath: filepath.Join(t.TempDir(), "env.secret.yaml"),
		Secrets:    map[string]Secret{"OPENCODE_KEY": {Value: "secret", Hosts: []string{"a.test"}}},
	}
	ui := termio.NewTestMock(t)
	if err := Review(plan, &ui); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Errorf("Review error = %v, want a denied host error", err)
	}
}

func TestReviewListsSecretHostsAndWarnings(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	plan := &Plan{
		AgentName:  "opencode",
		SecretPath: filepath.Join(t.TempDir(), "env.secret.yaml"),
		Secrets: map[string]Secret{
			"OPENCODE_KEY": {Value: "secret", Host: "single.test", Hosts: []string{"a.test"}},
		},
		Warnings: []string{"skipped malformed native config"},
	}
	ui := termio.NewTestMock(t)
	if err := Review(plan, &ui); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if output := strings.Join(ui.OutCalls, "\n"); !strings.Contains(output, "OPENCODE_KEY -> a.test, single.test") {
		t.Errorf("output does not list both secret hosts:\n%s", output)
	}
	if !slicesEqual(ui.WarnCalls, plan.Warnings) {
		t.Errorf("warnings = %v, want %v", ui.WarnCalls, plan.Warnings)
	}
}

func TestReviewAsksToConfirmUnrecognizedAuthFields(t *testing.T) {
	tests := map[string]struct {
		choice    string
		selectErr error
		wantErr   bool
	}{
		"continue":     {choice: "y"},
		"abort":        {choice: "n", wantErr: true},
		"select error": {selectErr: errInjected, wantErr: true},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			plan := &Plan{
				AgentName:      "opencode",
				SecretPath:     filepath.Join(t.TempDir(), "env.secret.yaml"),
				ReviewWarnings: []string{"review required: auth field openai.extra"},
			}
			ui := termio.NewTestMock(t)
			ui.IsInteractiveResult = true
			ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return test.choice, test.selectErr }
			if err := Review(plan, &ui); (err != nil) != test.wantErr {
				t.Errorf("Review error = %v, want error: %v", err, test.wantErr)
			}
		})
	}
}

func TestRefreshNetworkPlanLoadsLauncherConfigWithoutConfigPath(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeUserLauncherConfig(t, "config.yaml", "agent: opencode\n")
	plan := &Plan{RequiredNetworkHosts: []string{"a.test"}}
	if err := refreshNetworkPlan(plan); err != nil {
		t.Fatalf("refreshNetworkPlan: %v", err)
	}
	if want := filepath.Join(configpaths.Get().UserConfigDir(), "config.yaml"); plan.ConfigPath != want {
		t.Errorf("ConfigPath = %q, want %q", plan.ConfigPath, want)
	}
	if !strings.Contains(string(plan.ConfigData), "- a.test") {
		t.Errorf("ConfigData does not allow a.test:\n%s", plan.ConfigData)
	}
}

func TestRefreshNetworkPlanReportsLauncherConfigErrors(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	makeUnreadableLauncherConfig(t)
	if err := refreshNetworkPlan(&Plan{RequiredNetworkHosts: []string{"a.test"}}); err == nil {
		t.Error("unreadable launcher config: want an error")
	}

	configpaths.WithMockConfigPaths(t)
	denyAllEgressInLauncherConfig(t)
	if err := refreshNetworkPlan(&Plan{RequiredNetworkHosts: []string{"a.test"}}); err == nil {
		t.Error("denied host: want an error")
	}
}

func TestValidatePlanOutputsRejectsInvalidPaths(t *testing.T) {
	parentFile := filepath.Join(t.TempDir(), "file")
	testutil.WritePath(t, parentFile, "")
	tests := map[string][]File{
		"empty path":           {{Path: ""}},
		"duplicated path":      {{Path: "/tmp/a.json"}, {Path: "/tmp/a.json"}},
		"uninspectable parent": {{Path: filepath.Join(parentFile, "sub", "out.json")}},
	}
	for name, files := range tests {
		t.Run(name, func(t *testing.T) {
			if err := validatePlanOutputs(&Plan{Files: files}, nil); err == nil {
				t.Error("validatePlanOutputs succeeded, want an error")
			}
		})
	}
}

func TestGuideIgnoresAgentsWithoutSafeMigration(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	if err := Guide(unsupportedMigrationAgent{}, t.TempDir(), false, &ui); err != nil {
		t.Errorf("Guide: %v", err)
	}
}

func TestGuideReportsUnreadableNativeConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(hostHome, ".local", "share", "opencode", "auth.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	a, _ := agent.Lookup("opencode")
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	if err := Guide(a, hostHome, false, &ui); err == nil {
		t.Error("Guide succeeded with an unreadable native auth file, want an error")
	}
}

func TestNativeConfigExists(t *testing.T) {
	clearOpenCodeXDG(t)
	if nativeConfigExists(unsupportedMigrationAgent{}, t.TempDir()) {
		t.Error("nativeConfigExists = true for an agent without safe migration")
	}
	hostHome := t.TempDir()
	configDir := filepath.Join(hostHome, ".config", "opencode")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, configDir, "opencode.json", "{}")
	a, _ := agent.Lookup("opencode")
	if !nativeConfigExists(a, hostHome) {
		t.Error("nativeConfigExists = false with a native config file")
	}
}

func TestEnableNativeProvisioningReportsErrors(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	makeUnreadableLauncherConfig(t)
	if err := EnableNativeProvisioning(); err == nil {
		t.Error("unreadable launcher config: want an error")
	}

	configpaths.WithMockConfigPaths(t)
	withMigrationSeams(t)
	writeUserLauncherConfig(t, "config.json", "{}")
	marshalJSONIndentFn = func(any, string, string) ([]byte, error) { return nil, errInjected }
	if err := EnableNativeProvisioning(); !errors.Is(err, errInjected) {
		t.Errorf("marshal error = %v, want %v", err, errInjected)
	}
}

func TestHasManagedConfigIsFalseWithoutSnippetMerge(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	spec := agent.ConfigMigrationSpec{ManagedSnippet: "snippet.json"}
	if hasManagedConfig(specOnlyMigrationAgent{spec: spec}, spec) {
		t.Error("hasManagedConfig = true for an agent without snippet merge")
	}
}
