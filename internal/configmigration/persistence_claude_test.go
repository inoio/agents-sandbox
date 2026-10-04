package configmigration

import (
	"encoding/json"
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

func claudeSettingsPath(t *testing.T) string {
	t.Helper()
	a, _ := agent.Lookup("claude-code")
	return filepath.Join(configpaths.Get().UserAgentConfigDir(a), "settings-migrated.json")
}

func newClaudePlan(t *testing.T, files ...File) *Plan {
	t.Helper()
	return &Plan{
		AgentName:  "claude-code",
		Secrets:    map[string]Secret{},
		SecretPath: filepath.Join(t.TempDir(), "env.secret.yaml"),
		Files:      files,
	}
}

// apiKeyUI answers the Claude authentication prompts with the API key choice
// and the given endpoint and key.
func apiKeyUI(t *testing.T, endpoint, apiKey string) *termio.Mock {
	t.Helper()
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return claudeAuthAPIKeyChoice, nil }
	ui.InputFn = func(string, string) (string, error) { return endpoint, nil }
	ui.SecretInputFn = func(string) (string, error) { return apiKey, nil }
	return &ui
}

func decodeClaudeSettingsEnv(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("generated settings are not valid JSON: %v\n%s", err, data)
	}
	env, _ := settings["env"].(map[string]any)
	return env
}

func TestBuildFailsWhenClaudeLoginHostsAreDenied(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	denyAllEgressInLauncherConfig(t)
	a, _ := agent.Lookup("claude-code")
	if _, err := Build(a, t.TempDir()); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Errorf("Build error = %v, want a denied host error", err)
	}
}

func TestApplyRequiredNetworkPlanReportsUnreadableLauncherConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	makeUnreadableLauncherConfig(t)
	if err := applyRequiredNetworkPlan(&Plan{RequiredNetworkHosts: []string{"claude.ai"}}); err == nil {
		t.Error("applyRequiredNetworkPlan succeeded with an unreadable launcher config, want an error")
	}
}

func TestUpdateLauncherNetworkRejectsMalformedConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if _, _, _, err := updateLauncherNetwork("config.yaml", []byte("network: ["), []string{"claude.ai"}); err == nil {
		t.Error("updateLauncherNetwork succeeded with malformed YAML, want an error")
	}
}

func TestReviewSkipsClaudeAuthenticationChoiceWhenNotInteractive(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		t.Fatal("unexpected authentication prompt in a non-interactive session")
		return "", nil
	}
	if err := Review(newClaudePlan(t), &ui); err != nil {
		t.Errorf("Review: %v", err)
	}
}

func TestReviewStoresClaudeAPIKeyAsSecret(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	settingsPath := claudeSettingsPath(t)
	plan := newClaudePlan(
		t,
		File{Path: filepath.Join(t.TempDir(), "other.json"), Data: []byte("{}\n")},
		File{
			Path: settingsPath,
			Data: []byte(
				`{"model":"opus","env":{"ANTHROPIC_AUTH_TOKEN":"$MSB_CLAUDE_ENV_ANTHROPIC_AUTH_TOKEN","KEEP":"x"}}`,
			),
		},
	)
	plan.Secrets["CLAUDE_ENV_ANTHROPIC_AUTH_TOKEN"] = Secret{Value: "old-token", Hosts: []string{"api.anthropic.com"}}
	if err := Review(plan, apiKeyUI(t, " https://gateway.example.test/v1 ", " sk-ant-key ")); err != nil {
		t.Fatalf("Review: %v", err)
	}
	secret := plan.Secrets["CLAUDE_ENV_ANTHROPIC_API_KEY"]
	if secret.Value != "sk-ant-key" || !slicesEqual(secret.Hosts, []string{"gateway.example.test"}) {
		t.Errorf("API key secret = %+v, want trimmed key for gateway.example.test", secret)
	}
	if _, ok := plan.Secrets["CLAUDE_ENV_ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Error("previous Claude credential secret was not removed")
	}
	settings, _ := plan.fileAt(settingsPath)
	env := decodeClaudeSettingsEnv(t, settings.Data)
	want := map[string]any{
		"ANTHROPIC_API_KEY":  "$MSB_CLAUDE_ENV_ANTHROPIC_API_KEY",
		"ANTHROPIC_BASE_URL": "https://gateway.example.test/v1",
		"KEEP":               "x",
	}
	if len(env) != len(want) {
		t.Errorf("settings env = %v, want %v", env, want)
	}
	for key, value := range want {
		if env[key] != value {
			t.Errorf("settings env[%s] = %v, want %v", key, env[key], value)
		}
	}
	for _, host := range []string{"gateway.example.test", claudeAnthropicAPIHost} {
		if !strings.Contains(string(plan.ConfigData), host) {
			t.Errorf("launcher config does not allow %s:\n%s", host, plan.ConfigData)
		}
	}
}

func TestReviewAddsClaudeSettingsForAPIKeyWithoutNativeSettings(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	plan := newClaudePlan(t)
	if err := Review(plan, apiKeyUI(t, "", "sk-ant-key")); err != nil {
		t.Fatalf("Review: %v", err)
	}
	settings, ok := plan.fileAt(claudeSettingsPath(t))
	if !ok {
		t.Fatalf("no generated Claude settings in %v", plan.Files)
	}
	if env := decodeClaudeSettingsEnv(
		t,
		settings.Data,
	); env["ANTHROPIC_BASE_URL"] != "https://"+claudeAnthropicAPIHost {
		t.Errorf("ANTHROPIC_BASE_URL = %v, want the default Anthropic endpoint", env["ANTHROPIC_BASE_URL"])
	}
}

func TestReviewKeepsClaudeSettingsWithoutEnv(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	settingsPath := claudeSettingsPath(t)
	plan := newClaudePlan(t, File{Path: settingsPath, Data: []byte(`{"model":"opus"}`)})
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return claudeAuthLoginChoice, nil }
	if err := Review(plan, &ui); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if settings, _ := plan.fileAt(settingsPath); string(settings.Data) != `{"model":"opus"}` {
		t.Errorf("settings without env were rewritten: %s", settings.Data)
	}
}

func TestReviewClaudeAuthenticationErrors(t *testing.T) {
	malformedSettings := func(t *testing.T) *Plan {
		t.Helper()
		return newClaudePlan(t, File{Path: claudeSettingsPath(t), Data: []byte("{")})
	}
	tests := map[string]struct {
		plan      func(t *testing.T) *Plan
		choice    string
		configure func(ui *termio.Mock)
	}{
		"select error": {
			configure: func(ui *termio.Mock) {
				ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return "", errInjected }
			},
		},
		"unknown choice": {
			configure: func(ui *termio.Mock) {
				ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return "unknown", nil }
			},
		},
		"malformed settings on login":   {plan: malformedSettings, choice: claudeAuthLoginChoice},
		"malformed settings on API key": {plan: malformedSettings},
		"endpoint input error": {
			configure: func(ui *termio.Mock) {
				ui.InputFn = func(string, string) (string, error) { return "", errInjected }
			},
		},
		"invalid endpoint": {
			configure: func(ui *termio.Mock) {
				ui.InputFn = func(string, string) (string, error) { return "ftp://gateway.example.test", nil }
			},
		},
		"API key input error": {
			configure: func(ui *termio.Mock) {
				ui.SecretInputFn = func(string) (string, error) { return "", errInjected }
			},
		},
		"empty API key": {
			configure: func(ui *termio.Mock) {
				ui.SecretInputFn = func(string) (string, error) { return "  ", nil }
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			ui := apiKeyUI(t, "gateway.example.test", "sk-ant-key")
			if test.choice != "" {
				ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return test.choice, nil }
			}
			if test.configure != nil {
				test.configure(ui)
			}
			plan := newClaudePlan(t)
			if test.plan != nil {
				plan = test.plan(t)
			}
			if err := Review(plan, ui); err == nil {
				t.Error("Review succeeded, want an error")
			}
		})
	}
}

func TestReviewClaudeAuthenticationRejectsUnknownAgent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	plan := newClaudePlan(t)
	plan.AgentName = "unknown"
	if err := reviewClaudeAuthentication(plan, apiKeyUI(t, "", "sk-ant-key")); err == nil {
		t.Error("reviewClaudeAuthentication succeeded for an unknown agent, want an error")
	}
}

func TestClaudeSettingsReportMarshalErrors(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMigrationSeams(t)
	marshalJSONIndentFn = func(any, string, string) ([]byte, error) { return nil, errInjected }
	withEnv := newClaudePlan(t, File{Path: claudeSettingsPath(t), Data: []byte(`{"env":{"KEEP":"x"}}`)})
	if err := removeClaudeAuthentication(withEnv); !errors.Is(err, errInjected) {
		t.Errorf("removeClaudeAuthentication error = %v, want %v", err, errInjected)
	}
	if err := reviewClaudeAuthentication(
		newClaudePlan(t),
		apiKeyUI(t, "", "sk-ant-key"),
	); !errors.Is(
		err,
		errInjected,
	) {
		t.Errorf("reviewClaudeAuthentication error = %v, want %v", err, errInjected)
	}
}

func TestAppendClaudeSettingsEnvRejectsMalformedSettings(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a, _ := agent.Lookup("claude-code")
	files := []File{{Path: claudeSettingsPath(t), Data: []byte("{")}}
	if _, err := appendClaudeSettingsEnv(
		files,
		a,
		"CLAUDE_ENV_ANTHROPIC_API_KEY",
		"https://api.anthropic.com",
	); err == nil {
		t.Error("appendClaudeSettingsEnv succeeded with malformed settings, want an error")
	}
}

func TestAppendUniqueHostsSkipsEmptyAndDuplicateHosts(t *testing.T) {
	got := appendUniqueHosts([]string{"b.test", ""}, "a.test", "b.test")
	if want := []string{"a.test", "b.test"}; !slicesEqual(got, want) {
		t.Errorf("appendUniqueHosts = %v, want %v", got, want)
	}
}

func freshClaudeGuideUI(t *testing.T, choices ...string) *termio.Mock {
	t.Helper()
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		if len(choices) == 0 {
			return "", errInjected
		}
		choice := choices[0]
		choices = choices[1:]
		return choice, nil
	}
	return &ui
}

func TestGuideSetupForFreshClaudeCodeUser(t *testing.T) {
	tests := map[string]struct {
		choices []string
		wantErr error
	}{
		"setup select error":   {choices: nil, wantErr: errInjected},
		"unknown setup choice": {choices: []string{"unknown"}},
		"start select error":   {choices: []string{guideSetupChoice, claudeAuthLoginChoice}, wantErr: errInjected},
		"unknown start choice": {choices: []string{guideSetupChoice, claudeAuthLoginChoice, "unknown"}},
		"quit after setup": {
			choices: []string{guideSetupChoice, claudeAuthLoginChoice, "n"},
			wantErr: ErrStartDeferred,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			a, _ := agent.Lookup("claude-code")
			err := Guide(a, t.TempDir(), false, freshClaudeGuideUI(t, test.choices...))
			if err == nil || (test.wantErr != nil && !errors.Is(err, test.wantErr)) {
				t.Errorf("Guide error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestGuideSetupReportsApplyError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	withMigrationSeams(t)
	atomicWriteFn = func(string, []byte, os.FileMode) error { return errInjected }
	a, _ := agent.Lookup("claude-code")
	err := Guide(a, t.TempDir(), false, freshClaudeGuideUI(t, guideSetupChoice, claudeAuthLoginChoice))
	if !errors.Is(err, errInjected) {
		t.Errorf("Guide error = %v, want %v", err, errInjected)
	}
}

func TestNativeConfigExistsIsFalseWithoutClaudeSettings(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	hostHome := t.TempDir()
	claudeDir := filepath.Join(hostHome, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, claudeDir, ".credentials.json", "{}")
	a, _ := agent.Lookup("claude-code")
	if nativeConfigExists(a, hostHome) {
		t.Error("nativeConfigExists = true with only Claude login state, want false")
	}
}

func TestHasManagedConfigFindsClaudeSnippetWithoutCredential(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a, _ := agent.Lookup("claude-code")
	spec := a.(agent.MigrationSpecProvider).MigrationSpec()
	testutil.WriteFile(t, configpaths.Get().UserAgentConfigDir(a), spec.ManagedSnippet, "{}")
	if !hasManagedConfig(a, spec) {
		t.Error("hasManagedConfig = false with a managed snippet and no managed credential")
	}
}

func TestEnableNativeProvisioningRejectsMalformedLauncherConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeUserLauncherConfig(t, "config.yaml", "network: [")
	if err := EnableNativeProvisioning(); err == nil {
		t.Error("EnableNativeProvisioning succeeded with malformed YAML, want an error")
	}
}
