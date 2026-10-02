package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configmigration"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// mustOpencode returns the built-in opencode agent profile, failing the test if
// it is not registered.
func mustOpencode(t *testing.T) agent.Agent {
	t.Helper()
	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}
	return a
}

func TestConfigAgentPrintsMergedAndHostFiles(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "config", "agent", "opencode")
	snippetPath := filepath.Join(configpaths.Get().UserAgentConfigDir(mustOpencode(t)), "opencode-x.json5")
	testutil.WritePath(t, snippetPath, `{"model":"x"}`)

	// A host opencode.jsonc plus a non-config file exercise both statuses.
	hostOcDir := filepath.Join(os.Getenv("HOME"), ".config", "opencode")
	if err := os.MkdirAll(hostOcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, filepath.Join(hostOcDir, "opencode.jsonc"), `{"model":"host"}`)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config agent: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(joined, `"model": "x"`) {
		t.Errorf("expected merged model in output, got:\n%s", joined)
	}
	if !strings.Contains(joined, "merged files:") {
		t.Errorf("expected merged files listing header, got:\n%s", joined)
	}
	if !strings.Contains(joined, snippetPath) {
		t.Errorf("expected merged source path %q in output, got:\n%s", snippetPath, joined)
	}
	if !strings.Contains(joined, "opencode.jsonc") {
		t.Errorf("expected host drop-in file in output, got:\n%s", joined)
	}
	if !strings.Contains(joined, "provision-host-config=false") {
		t.Errorf("expected secure default in output, got:\n%s", joined)
	}
}

func TestConfigAgentReportsProvisionHostConfigOptIn(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "config", "agent", "opencode")
	testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "config.yaml", "provision-host-config: true\n")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config agent: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(joined, "provision-host-config=true") {
		t.Errorf("expected explicit opt-in in output, got:\n%s", joined)
	}
}

func TestConfigHomeListsMappings(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "config", "home")
	testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "config.yaml", "home:\n  .gitconfig:\n")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config home: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(joined, "/home/dev/.gitconfig") {
		t.Errorf("expected home target in output, got:\n%s", joined)
	}
}

func TestConfigHomeRejectsReservedMergedConfigTarget(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "config", "home")
	testutil.WriteFile(
		t,
		configpaths.Get().UserConfigDir(),
		"config.yaml",
		"home:\n  .config/opencode/opencode.jsonc:\n",
	)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for a reserved home target")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("expected 'reserved' in error, got: %v", err)
	}
}

func TestConfigHomeNotFoundManifest(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "config", "home")
	if err := os.MkdirAll(configpaths.Get().UserConfigDir(), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config home: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(joined, "No home configuration found.") {
		t.Errorf("expected not-found message, got:\n%s", joined)
	}
}

func TestConfigHomeEmptyManifest(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "config", "home")
	testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "config.yaml", "home:\n")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config home: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(joined, "No home mappings.") {
		t.Errorf("expected empty-manifest message, got:\n%s", joined)
	}
	if strings.Contains(joined, "No home configuration found.") {
		t.Errorf("found misleading not-found message for an existing empty home config:\n%s", joined)
	}
}

func TestConfigAgentNoSnippetFiles(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "config", "agent", "opencode")
	a, _ := agent.Lookup("opencode")
	if err := os.MkdirAll(configpaths.Get().UserAgentConfigDir(a), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config agent: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(joined, "No snippet files found") {
		t.Errorf("expected no-snippets message, got:\n%s", joined)
	}
}

func TestConfigAgentFlag(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "config", "agent", "--agent", "pi")
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config agent --agent pi: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(joined, "pi") {
		t.Errorf("expected output to reference pi, got %q", joined)
	}
}

func TestConfigAgentAmbiguousFlagAndPositional(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "config", "agent", "--agent", "pi", "claude-code")
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected ambiguous error, got %v", err)
	}
}

func TestConfigMigrateAmbiguousFlagAndPositional(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "config", "migrate", "--agent", "pi", "claude-code")
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected ambiguous migration error, got %v", err)
	}
}

func TestConfigAgentUnknownAgent(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "config", "agent", "bogus")
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error for an unknown agent")
	}
}

func TestConfigHomeManifestError(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "config", "home")
	testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "config.yaml", "home:\n  ../escape:\n")

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for an escaping home target")
	}
	if !strings.Contains(err.Error(), "escapes the home directory") {
		t.Errorf("expected 'escapes the home directory' error, got: %v", err)
	}
}

func TestConfigAgentPrintsMirrorFiles(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "config", "agent", "opencode")
	agentDir := configpaths.Get().UserAgentConfigDir(mustOpencode(t))
	testutil.WriteFile(t, agentDir, "tui.json", `{"theme":"dark"}`)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config agent: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(joined, "mirror files:") {
		t.Errorf("expected mirror files section, got:\n%s", joined)
	}
	if !strings.Contains(joined, filepath.Join(agentDir, "tui.json")) {
		t.Errorf("expected mirror source path, got:\n%s", joined)
	}
	if !strings.Contains(joined, "/home/dev/.config/opencode/tui.json") {
		t.Errorf("expected mirror VM path, got:\n%s", joined)
	}
}

func TestConfigMigrateWithoutNativeConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	cmd, ui := setupCommandFixtures(t, "config", "migrate")
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config migrate: %v", err)
	}
	if got := strings.Join(ui.OutCalls, "\n"); !strings.Contains(got, "No native opencode configuration found.") {
		t.Errorf("unexpected migration output: %s", got)
	}
}

func TestConfigMigrateUnsupportedAgent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	cmd, ui := setupCommandFixtures(t, "config", "migrate", "pi")
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config migrate pi: %v", err)
	}
	if got := strings.Join(ui.WarnCalls, "\n"); !strings.Contains(got, "safe migration is not available") {
		t.Errorf("unexpected migration warning: %s", got)
	}
}

func TestConfigMigrateWritesSanitizedOpenCodeAuth(t *testing.T) {
	hostHome := t.TempDir()
	t.Setenv("HOME", hostHome)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	cmd, ui := setupCommandFixtures(t, "config", "migrate", "opencode")
	authDir := filepath.Join(hostHome, ".local", "share", "opencode")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, authDir, "auth.json", `{"openrouter":{"type":"api","key":"secret-key"}}`)
	ui.IsInteractiveResult = true
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return "y", nil }

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config migrate opencode: %v", err)
	}
	managedAuth := filepath.Join(configpaths.Get().UserAgentConfigDir(mustOpencode(t)), "auth.json")
	data, err := os.ReadFile(managedAuth)
	if err != nil {
		t.Fatalf("read managed auth: %v", err)
	}
	if strings.Contains(string(data), "secret-key") || !strings.Contains(string(data), "$MSB_") {
		t.Errorf("managed auth was not sanitized: %s", data)
	}
	if !strings.Contains(strings.Join(ui.OutCalls, "\n"), "Safe migration completed") {
		t.Errorf("missing completion output: %v", ui.OutCalls)
	}
}

func TestConfigMigrateReportsUnsupportedNativeConfig(t *testing.T) {
	hostHome := t.TempDir()
	t.Setenv("HOME", hostHome)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	cmd, ui := setupCommandFixtures(t, "config", "migrate", "opencode")
	authDir := filepath.Join(hostHome, ".local", "share", "opencode")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, authDir, "auth.json", `{"custom":{"type":"unsupported","token":"raw-token"}}`)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config migrate: %v", err)
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, "\n"), "no supported credential migration") {
		t.Errorf("warnings = %v", ui.WarnCalls)
	}
}

func TestConfigMigrateRefusesManagedConfigOverwrite(t *testing.T) {
	hostHome := t.TempDir()
	t.Setenv("HOME", hostHome)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	cmd, ui := setupCommandFixtures(t, "config", "migrate", "opencode")
	testutil.WriteFile(t, configpaths.Get().UserAgentConfigDir(mustOpencode(t)), "existing.json", "{}")
	authDir := filepath.Join(hostHome, ".local", "share", "opencode")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, authDir, "auth.json", `{"openrouter":{"type":"api","key":"secret"}}`)
	ui.IsInteractiveResult = true
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return "y", nil }
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config migrate: %v", err)
	}
	if strings.Contains(strings.Join(ui.WarnCalls, "\n"), "refusing to overwrite") {
		t.Errorf("unrelated managed files should not block migration: %v", ui.WarnCalls)
	}
}

func TestConfigMigrateCommandOutcomes(t *testing.T) {
	originalBuild := buildMigrationPlan
	originalApply := applyMigrationPlan
	originalHome := migrationHomeDir
	t.Cleanup(func() {
		buildMigrationPlan = originalBuild
		applyMigrationPlan = originalApply
		migrationHomeDir = originalHome
	})
	basePlan := func() *configmigration.Plan {
		return &configmigration.Plan{
			HasNativeConfig: true,
			Files:           []configmigration.File{{Path: "file"}},
			SecretPath:      "secrets",
		}
	}
	migrationHomeDir = func() (string, error) { return t.TempDir(), nil }
	buildMigrationPlan = func(agent.Agent, string) (*configmigration.Plan, error) {
		plan := basePlan()
		plan.AlreadyConfigured = true
		return plan, nil
	}
	cmd, ui := setupCommandFixtures(t, "config", "migrate", "opencode")
	if err := cmd.Execute(); err != nil || !strings.Contains(strings.Join(ui.OutCalls, "\n"), "already configured") {
		t.Fatalf("already configured result = %v, %v", err, ui.OutCalls)
	}
	buildMigrationPlan = func(agent.Agent, string) (*configmigration.Plan, error) {
		plan := basePlan()
		plan.Dismissed = true
		return plan, nil
	}
	applyMigrationPlan = func(*configmigration.Plan, termio.UI) error { return nil }
	cmd, ui = setupCommandFixtures(t, "config", "migrate", "opencode")
	if err := cmd.Execute(); err != nil || !strings.Contains(strings.Join(ui.OutCalls, "\n"), "previously dismissed") {
		t.Fatalf("dismissed result = %v, %v", err, ui.OutCalls)
	}
	buildMigrationPlan = func(agent.Agent, string) (*configmigration.Plan, error) {
		plan := basePlan()
		plan.Warnings = []string{"warning"}
		return plan, nil
	}
	cmd, ui = setupCommandFixtures(t, "config", "migrate", "opencode")
	if err := cmd.Execute(); err != nil || len(ui.WarnCalls) == 0 {
		t.Fatalf("warning result = %v, %v", err, ui.WarnCalls)
	}
	buildMigrationPlan = func(agent.Agent, string) (*configmigration.Plan, error) {
		return &configmigration.Plan{
			HasNativeConfig:  true,
			Files:            []configmigration.File{{Path: "x"}},
			HasManagedConfig: true,
		}, nil
	}
	cmd, ui = setupCommandFixtures(t, "config", "migrate", "opencode")
	if err := cmd.Execute(); err != nil || len(ui.WarnCalls) == 0 ||
		!strings.Contains(strings.Join(ui.WarnCalls, "\n"), "refusing") {
		t.Fatalf("managed config result = %v, %v", err, ui.WarnCalls)
	}
}

func TestConfigMigrateCommandErrorBranches(t *testing.T) {
	originalBuild := buildMigrationPlan
	originalApply := applyMigrationPlan
	originalHome := migrationHomeDir
	t.Cleanup(func() {
		buildMigrationPlan = originalBuild
		applyMigrationPlan = originalApply
		migrationHomeDir = originalHome
	})
	cmd, _ := setupCommandFixtures(t, "config", "migrate", "opencode")
	migrationHomeDir = func() (string, error) { return "", errors.New("home failed") }
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "home failed") {
		t.Fatalf("home error = %v", err)
	}
	migrationHomeDir = os.UserHomeDir
	buildMigrationPlan = func(agent.Agent, string) (*configmigration.Plan, error) {
		return nil, errors.New("plan failed")
	}
	cmd, _ = setupCommandFixtures(t, "config", "migrate", "opencode")
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "plan failed") {
		t.Fatalf("plan error = %v", err)
	}
	buildMigrationPlan = func(agent.Agent, string) (*configmigration.Plan, error) {
		return &configmigration.Plan{HasNativeConfig: true, Files: []configmigration.File{{Path: "x"}}}, nil
	}
	applyMigrationPlan = func(*configmigration.Plan, termio.UI) error { return errors.New("apply failed") }
	cmd, _ = setupCommandFixtures(t, "config", "migrate", "opencode")
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "apply failed") {
		t.Fatalf("apply error = %v", err)
	}
}

func TestConfigMigrateReviewError(t *testing.T) {
	originalBuild := buildMigrationPlan
	originalHome := migrationHomeDir
	t.Cleanup(func() {
		buildMigrationPlan = originalBuild
		migrationHomeDir = originalHome
	})
	migrationHomeDir = func() (string, error) { return t.TempDir(), nil }
	buildMigrationPlan = func(agent.Agent, string) (*configmigration.Plan, error) {
		return &configmigration.Plan{
			AgentName:       "opencode",
			HasNativeConfig: true,
			Files:           []configmigration.File{{Path: "auth.json"}},
			ReviewWarnings:  []string{"review required"},
		}, nil
	}
	cmd, ui := setupCommandFixtures(t, "config", "migrate", "opencode")
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "interactive review") {
		t.Fatalf("review error = %v, output = %v", err, ui.WarnCalls)
	}
}

func TestConfigMigrateUnknownAgentAndHomeErrors(t *testing.T) {
	originalHome := migrationHomeDir
	t.Cleanup(func() { migrationHomeDir = originalHome })
	cmd, _ := setupCommandFixtures(t, "config", "migrate", "unknown")
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("unknown agent error = %v", err)
	}
	migrationHomeDir = func() (string, error) { return "", errors.New("home failed") }
	cmd, _ = setupCommandFixtures(t, "config", "migrate", "opencode")
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "home failed") {
		t.Fatalf("home error = %v", err)
	}
}
