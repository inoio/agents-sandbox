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

func TestConfigMigrateClaudeWithoutNativeConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	cmd, ui := setupCommandFixtures(t, "config", "migrate", "claude-code")
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config migrate claude-code: %v", err)
	}
	if got := strings.Join(ui.OutCalls, "\n"); !strings.Contains(got, "api.anthropic.com") ||
		!strings.Contains(got, "platform.claude.com") || !strings.Contains(got, "claude.ai") {
		t.Errorf("expected default Claude egress output, got: %s", got)
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
	choices := []string{"login", "y"}
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		choice := choices[0]
		choices = choices[1:]
		return choice, nil
	}

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

func TestConfigMigrateWritesSanitizedPiFiles(t *testing.T) {
	hostHome := t.TempDir()
	t.Setenv("HOME", hostHome)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	cmd, ui := setupCommandFixtures(t, "config", "migrate", "pi")
	nativeDir := filepath.Join(hostHome, ".pi", "agent")
	if err := os.MkdirAll(nativeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, nativeDir, "settings.json", `{"defaultProvider":"anthropic"}`)
	testutil.WriteFile(t, nativeDir, "auth.json", `{"anthropic":{"type":"api_key","key":"pi-secret"}}`)
	testutil.WriteFile(
		t,
		nativeDir,
		"models.json",
		`{"providers":{"gateway":{"baseUrl":"https://gateway.example.test/v1","apiKey":"gateway-secret"}}}`,
	)
	ui.IsInteractiveResult = true
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return "y", nil }

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config migrate pi: %v", err)
	}
	pi, _ := agent.Lookup("pi")
	for _, name := range []string{"auth.json", "models.json", "settings-migrated.json"} {
		data, err := os.ReadFile(filepath.Join(configpaths.Get().UserAgentConfigDir(pi), name))
		if err != nil {
			t.Fatalf("read managed Pi %s: %v", name, err)
		}
		if strings.Contains(string(data), "pi-secret") || strings.Contains(string(data), "gateway-secret") {
			t.Errorf("managed Pi %s contains a raw credential: %s", name, data)
		}
	}
	if !strings.Contains(strings.Join(ui.OutCalls, "\n"), "Safe migration completed") {
		t.Errorf("missing Pi completion output: %v", ui.OutCalls)
	}
}

func TestConfigMigrateWritesSanitizedClaudeSettings(t *testing.T) {
	hostHome := t.TempDir()
	t.Setenv("HOME", hostHome)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	cmd, ui := setupCommandFixtures(t, "config", "migrate", "claude-code")
	nativeDir := filepath.Join(hostHome, ".claude")
	if err := os.MkdirAll(nativeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, nativeDir, "settings.json", `{"env":{"ANTHROPIC_API_KEY":"claude-secret"}}`)
	ui.IsInteractiveResult = true
	choices := []string{"login", "y"}
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		choice := choices[0]
		choices = choices[1:]
		return choice, nil
	}
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config migrate claude-code: %v", err)
	}
	claude, _ := agent.Lookup("claude-code")
	managed := filepath.Join(configpaths.Get().UserAgentConfigDir(claude), "settings-migrated.json")
	data, err := os.ReadFile(managed)
	if err != nil || strings.Contains(string(data), "claude-secret") ||
		strings.Contains(string(data), "ANTHROPIC_API_KEY") {
		t.Fatalf("managed Claude settings = %s, err=%v", data, err)
	}
}

func TestConfigMigrateClaudePromptsForAPIKeyAndPreservesExistingSecrets(t *testing.T) {
	hostHome := t.TempDir()
	t.Setenv("HOME", hostHome)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	cmd, ui := setupCommandFixtures(t, "config", "migrate", "claude-code")
	testutil.WritePath(
		t,
		configpaths.Get().UserEnvSecretYAMLFile(),
		"EXISTING:\n  value: keep\n  hosts: [existing.example]\n",
	)
	ui.IsInteractiveResult = true
	choices := []string{"api-key", "y"}
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		choice := choices[0]
		choices = choices[1:]
		return choice, nil
	}
	ui.InputFn = func(string, string) (string, error) { return "gateway.example.test/v1", nil }
	ui.SecretInputFn = func(string) (string, error) { return "claude-secret", nil }
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config migrate claude-code API key: %v", err)
	}
	secretData, err := os.ReadFile(configpaths.Get().UserEnvSecretYAMLFile())
	if err != nil || !strings.Contains(string(secretData), "EXISTING:") ||
		!strings.Contains(string(secretData), "CLAUDE_ENV_ANTHROPIC_API_KEY:") {
		t.Fatalf("merged Claude secret file = %s, err=%v", secretData, err)
	}
	claude, _ := agent.Lookup("claude-code")
	settingsData, err := os.ReadFile(
		filepath.Join(configpaths.Get().UserAgentConfigDir(claude), "settings-migrated.json"),
	)
	if err != nil || strings.Contains(string(settingsData), "claude-secret") ||
		!strings.Contains(string(settingsData), "$MSB_CLAUDE_ENV_ANTHROPIC_API_KEY") ||
		!strings.Contains(string(settingsData), "gateway.example.test") {
		t.Fatalf("generated Claude API settings = %s, err=%v", settingsData, err)
	}
	configData, err := os.ReadFile(filepath.Join(configpaths.Get().UserConfigDir(), "config.yaml"))
	if err != nil || !strings.Contains(string(configData), "gateway.example.test") ||
		!strings.Contains(string(configData), "api.anthropic.com") {
		t.Fatalf("Claude egress config = %s, err=%v", configData, err)
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
		return nil, configmigration.ErrUnsupported
	}
	cmd, ui = setupCommandFixtures(t, "config", "migrate", "opencode")
	if err := cmd.Execute(); err != nil || !strings.Contains(strings.Join(ui.WarnCalls, "\n"), "not available") {
		t.Fatalf("unsupported result = %v, %v", err, ui.WarnCalls)
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

func TestConfigMigratePrintsNoSupportedCredentialWarning(t *testing.T) {
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
			Warnings:        []string{"native values require review"},
		}, nil
	}
	cmd, ui := setupCommandFixtures(t, "config", "migrate", "opencode")
	if err := cmd.Execute(); err != nil ||
		!strings.Contains(strings.Join(ui.WarnCalls, "\n"), "no supported credential") {
		t.Fatalf("unsupported credential result = %v, %v", err, ui.WarnCalls)
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
