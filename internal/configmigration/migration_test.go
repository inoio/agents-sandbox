package configmigration

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestPlanOpenCodeAuthReplacesCredentialsWithPlaceholders(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}

	authPath := filepath.Join(hostHome, ".local", "share", "opencode", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, authPath, `{
  "github-copilot": {
    "type": "oauth",
    "access": "access-token",
    "refresh": "refresh-token",
    "expires": 123,
    "accountId": "account"
  },
  "openai": {
    "type": "api",
    "key": "api-token"
  }
}`)

	plan, err := Build(a, hostHome)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	authFile, ok := plan.fileAt(filepath.Join(configpaths.Get().UserAgentConfigDir(a), "auth.json"))
	if !ok {
		t.Fatalf("planned auth file missing: %+v", plan.Files)
	}
	if bytes.Contains(authFile.Data, []byte("access-token")) ||
		bytes.Contains(authFile.Data, []byte("refresh-token")) ||
		bytes.Contains(authFile.Data, []byte("api-token")) {
		t.Fatalf("planned auth file contains a raw credential: %s", authFile.Data)
	}
	for _, placeholder := range []string{
		"$MSB_OPENCODE_GITHUB_COPILOT_ACCESS",
		"$MSB_OPENCODE_GITHUB_COPILOT_REFRESH",
		"$MSB_OPENCODE_OPENAI_KEY",
	} {
		if !bytes.Contains(authFile.Data, []byte(placeholder)) {
			t.Errorf("planned auth file does not contain %q: %s", placeholder, authFile.Data)
		}
	}
	if !bytes.Contains(authFile.Data, []byte(`"expires": 123`)) {
		t.Errorf("planned auth file lost non-secret fields: %s", authFile.Data)
	}

	if got := plan.Secrets["OPENCODE_GITHUB_COPILOT_ACCESS"].Value; got != "access-token" {
		t.Errorf("access secret = %q, want raw source value", got)
	}
	if got := plan.Secrets["OPENCODE_GITHUB_COPILOT_ACCESS"].Hosts; !slicesEqual(
		got,
		[]string{"api.githubcopilot.com"},
	) {
		t.Errorf("access hosts = %v, want known provider host", got)
	}
	if got := plan.Secrets["OPENCODE_OPENAI_KEY"].Hosts; !slicesEqual(got, []string{"api.openai.com"}) {
		t.Errorf("api key hosts = %v, want known provider host", got)
	}

	if !strings.Contains(string(plan.ConfigData), ".local/share/opencode/auth.json") {
		t.Errorf("planned launcher config has no OpenCode auth mapping: %s", plan.ConfigData)
	}
}

func TestPlanPiSettingsAndAuthReplacesCredentialsWithPlaceholders(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	hostHome := t.TempDir()
	a, ok := agent.Lookup("pi")
	if !ok {
		t.Fatal("pi agent not registered")
	}

	nativeDir := filepath.Join(hostHome, ".pi", "agent")
	if err := os.MkdirAll(nativeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, nativeDir, "settings.json", `{"defaultProvider":"anthropic","theme":"dark"}`)
	testutil.WriteFile(t, nativeDir, "auth.json", `{
  "anthropic": {
    "type": "api_key",
    "key": "anthropic-token"
  }
}`)

	plan, err := Build(a, hostHome)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	configFile, ok := plan.fileAt(filepath.Join(configpaths.Get().UserAgentConfigDir(a), "settings-migrated.json"))
	if !ok || !bytes.Contains(configFile.Data, []byte(`"defaultProvider": "anthropic"`)) {
		t.Fatalf("planned pi settings file missing or incomplete: %+v", plan.Files)
	}
	authFile, ok := plan.fileAt(filepath.Join(configpaths.Get().UserAgentConfigDir(a), "auth.json"))
	if !ok {
		t.Fatalf("planned pi auth file missing: %+v", plan.Files)
	}
	if bytes.Contains(authFile.Data, []byte("anthropic-token")) ||
		!bytes.Contains(authFile.Data, []byte(`$MSB_PI_ANTHROPIC_KEY`)) {
		t.Fatalf("planned pi auth file contains an unsafe credential transformation: %s", authFile.Data)
	}
	secret, ok := plan.Secrets["PI_ANTHROPIC_KEY"]
	if !ok || secret.Value != "anthropic-token" || !slicesEqual(secret.Hosts, []string{"api.anthropic.com"}) {
		t.Fatalf("pi secret = %+v, want anthropic host and source value", secret)
	}
	if !bytes.Contains(plan.ConfigData, []byte(`.pi/agent/auth.json`)) {
		t.Fatalf("planned launcher config has no Pi auth mapping: %s", plan.ConfigData)
	}
}

func TestPlanClaudeSettingsMigratesEnvCredentials(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	hostHome := t.TempDir()
	a, ok := agent.Lookup("claude-code")
	if !ok {
		t.Fatal("claude-code agent not registered")
	}
	nativeDir := filepath.Join(hostHome, ".claude")
	if err := os.MkdirAll(nativeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, nativeDir, "settings.json", `{
  "model": "claude-sonnet",
  "env": {
    "ANTHROPIC_API_KEY": "claude-secret",
    "ANTHROPIC_BASE_URL": "https://gateway.example.test/v1"
  }
}`)

	plan, err := Build(a, hostHome)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	settings, ok := plan.fileAt(filepath.Join(configpaths.Get().UserAgentConfigDir(a), "settings-migrated.json"))
	if !ok || bytes.Contains(settings.Data, []byte("claude-secret")) ||
		!bytes.Contains(settings.Data, []byte("$MSB_CLAUDE_ENV_ANTHROPIC_API_KEY")) {
		t.Fatalf("Claude settings were not sanitized: %s", settings.Data)
	}
	secret, ok := plan.Secrets["CLAUDE_ENV_ANTHROPIC_API_KEY"]
	if !ok || secret.Value != "claude-secret" || !slicesEqual(secret.Hosts, []string{"gateway.example.test"}) {
		t.Fatalf("Claude secret = %+v", secret)
	}
	if !bytes.Contains(plan.ConfigData, []byte("gateway.example.test")) {
		t.Fatalf("planned launcher config has no Claude egress host: %s", plan.ConfigData)
	}
}

func TestReviewClaudeLoginDropsEgressHostsOfRemovedSecrets(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	hostHome := t.TempDir()
	a, _ := agent.Lookup("claude-code")
	nativeDir := filepath.Join(hostHome, ".claude")
	if err := os.MkdirAll(nativeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, nativeDir, "settings.json", `{
  "env": {
    "ANTHROPIC_API_KEY": "claude-secret",
    "ANTHROPIC_BASE_URL": "https://proxy.corp.example"
  }
}`)
	userConfigDir := configpaths.Get().UserConfigDir()
	if err := os.MkdirAll(userConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, userConfigDir, "config.yaml", "network:\n  egress-allow:\n    - existing.test\n")
	plan, err := Build(a, hostHome)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return claudeAuthLoginChoice, nil }
	if err := Review(plan, &ui); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if bytes.Contains(plan.ConfigData, []byte("proxy.corp.example")) {
		t.Fatalf("launcher config still allows the endpoint of the removed secret: %s", plan.ConfigData)
	}
	if !bytes.Contains(plan.ConfigData, []byte("existing.test")) {
		t.Fatalf("launcher config lost the pre-existing egress host: %s", plan.ConfigData)
	}
	if slices.Contains(plan.NetworkAllowHosts, "proxy.corp.example") {
		t.Fatalf("NetworkAllowHosts = %v, want no endpoint of the removed secret", plan.NetworkAllowHosts)
	}
}

func TestPlanClaudeRejectsNonStrictSettingsAndCredentialHelpers(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	hostHome := t.TempDir()
	a, _ := agent.Lookup("claude-code")
	nativeDir := filepath.Join(hostHome, ".claude")
	if err := os.MkdirAll(nativeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, nativeDir, "settings.json", `{ // comment
  "model": "claude-sonnet",
}`)
	if _, err := Build(a, hostHome); err == nil {
		t.Fatal("expected strict JSON parse failure")
	}
	testutil.WriteFile(t, nativeDir, "settings.json", `{"apiKeyHelper":"vault read anthropic"}`)
	if _, err := Build(a, hostHome); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("credential helper error = %v", err)
	}
}

func TestPlanClaudeUsesConfigDirOverride(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	hostHome := t.TempDir()
	configDir := filepath.Join(hostHome, "claude-alt")
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	a, _ := agent.Lookup("claude-code")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, configDir, "settings.json", `{"model":"claude-opus"}`)
	plan, err := Build(a, hostHome)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !plan.HasNativeConfig || len(plan.Files) != 1 {
		t.Fatalf("Claude config directory override was not discovered: %+v", plan)
	}
}

func TestPlanClaudeDoesNotTreatCredentialStoreAsNativeConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	hostHome := t.TempDir()
	a, _ := agent.Lookup("claude-code")
	credentialPath := filepath.Join(hostHome, ".claude", ".credentials.json")
	if err := os.MkdirAll(filepath.Dir(credentialPath), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, credentialPath, `{"claudeAiOauth":{"accessToken":"secret"}}`)
	plan, err := Build(a, hostHome)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if plan.HasNativeConfig || len(plan.Files) != 0 {
		t.Fatalf("Claude credential store was treated as settings: %+v", plan)
	}
}

func TestPlanClaudeSetupOnlyIncludesRequiredEgress(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	hostHome := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	a, _ := agent.Lookup("claude-code")
	plan, err := Build(a, hostHome)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !plan.SetupOnly || plan.HasNativeConfig || !slicesEqual(
		plan.RequiredNetworkHosts,
		[]string{"api.anthropic.com", "platform.claude.com", "claude.ai", "claude.com"},
	) {
		t.Fatalf("Claude setup-only plan = %+v", plan)
	}
}

func TestBuildUsesPiAgentDirectoryOverride(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	hostHome := t.TempDir()
	piDir := filepath.Join(hostHome, "custom-pi-agent")
	t.Setenv("PI_CODING_AGENT_DIR", piDir)
	a, _ := agent.Lookup("pi")
	if err := os.MkdirAll(piDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, piDir, "settings.json", `{"defaultModel":"anthropic/claude-sonnet"}`)
	testutil.WriteFile(t, piDir, "auth.json", `{"anthropic":{"type":"api_key","key":"custom-token"}}`)

	plan, err := Build(a, hostHome)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !plan.HasNativeConfig || len(plan.Files) != 2 {
		t.Fatalf("Pi agent directory override was not discovered: %+v", plan)
	}
}

func TestBuildUsesPiTildeAgentDirectoryOverride(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	hostHome := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", "~/.pi-alt")
	a, _ := agent.Lookup("pi")
	piDir := filepath.Join(hostHome, ".pi-alt")
	if err := os.MkdirAll(piDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, piDir, "settings.json", `{"theme":"dark"}`)
	plan, err := Build(a, hostHome)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !plan.HasNativeConfig || len(plan.Files) != 1 {
		t.Fatalf("Pi tilde agent directory override was not discovered: %+v", plan)
	}
}

func TestPlanPiMigratesOAuthCredentials(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	hostHome := t.TempDir()
	a, _ := agent.Lookup("pi")
	nativeDir := filepath.Join(hostHome, ".pi", "agent")
	if err := os.MkdirAll(nativeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, nativeDir, "auth.json", `{
  "anthropic": {
    "type": "oauth",
    "access": "access-token",
    "refresh": "refresh-token",
    "expires": 123
  }
}`)

	plan, err := Build(a, hostHome)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	authFile, ok := plan.fileAt(filepath.Join(configpaths.Get().UserAgentConfigDir(a), "auth.json"))
	if !ok || bytes.Contains(authFile.Data, []byte("access-token")) ||
		bytes.Contains(authFile.Data, []byte("refresh-token")) {
		t.Fatalf("Pi OAuth credentials were not sanitized: %+v", plan)
	}
	for _, placeholder := range []string{"$MSB_PI_ANTHROPIC_ACCESS", "$MSB_PI_ANTHROPIC_REFRESH"} {
		if !bytes.Contains(authFile.Data, []byte(placeholder)) {
			t.Errorf("Pi OAuth placeholder %q missing from %s", placeholder, authFile.Data)
		}
	}
}

func TestPlanPiMigratesModelsAndCustomProviderEndpoint(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	hostHome := t.TempDir()
	a, _ := agent.Lookup("pi")
	nativeDir := filepath.Join(hostHome, ".pi", "agent")
	if err := os.MkdirAll(nativeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, nativeDir, "models.json", `{
  "providers": {
    "gateway": {
      "baseUrl": "https://gateway.example.test/v1",
      "apiKey": "gateway-token",
      "models": [{"id": "custom-model"}]
    }
  }
}`)

	plan, err := Build(a, hostHome)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	modelsFile, ok := plan.fileAt(filepath.Join(configpaths.Get().UserAgentConfigDir(a), "models.json"))
	if !ok || bytes.Contains(modelsFile.Data, []byte("gateway-token")) ||
		!bytes.Contains(modelsFile.Data, []byte("$MSB_PI_CONFIG_PROVIDERS_GATEWAY_APIKEY")) {
		t.Fatalf("planned Pi models file was not sanitized: %s", modelsFile.Data)
	}
	secret, ok := plan.Secrets["PI_CONFIG_PROVIDERS_GATEWAY_APIKEY"]
	if !ok || secret.Value != "gateway-token" || !slicesEqual(secret.Hosts, []string{"gateway.example.test"}) {
		t.Fatalf("custom Pi provider secret = %+v", secret)
	}
	if !bytes.Contains(plan.ConfigData, []byte(`.pi/agent/models.json`)) {
		t.Fatalf("planned launcher config has no Pi models mapping: %s", plan.ConfigData)
	}
}

func TestPlanPiUsesModelsEndpointForAuthHost(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	hostHome := t.TempDir()
	a, _ := agent.Lookup("pi")
	nativeDir := filepath.Join(hostHome, ".pi", "agent")
	if err := os.MkdirAll(nativeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(
		t,
		nativeDir,
		"models.json",
		`{"providers":{"gateway":{"baseUrl":"https://gateway.example.test/v1"}}}`,
	)
	testutil.WriteFile(t, nativeDir, "auth.json", `{"gateway":{"type":"api_key","key":"gateway-token"}}`)

	plan, err := Build(a, hostHome)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	secret, ok := plan.Secrets["PI_GATEWAY_KEY"]
	if !ok || !slicesEqual(secret.Hosts, []string{"gateway.example.test"}) {
		t.Fatalf("Pi auth host inference = %+v", secret)
	}
}

func TestPlanPiModelsRejectsEmbeddedEndpointCredentials(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	hostHome := t.TempDir()
	a, _ := agent.Lookup("pi")
	nativeDir := filepath.Join(hostHome, ".pi", "agent")
	if err := os.MkdirAll(nativeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(
		t,
		nativeDir,
		"models.json",
		`{"providers":{"gateway":{"baseUrl":"https://user:pass@gateway.example.test/v1"}}}`,
	)

	if _, err := Build(a, hostHome); err == nil || !strings.Contains(err.Error(), "URL credentials") {
		t.Fatalf("embedded endpoint credential error = %v", err)
	}
}

func TestPlanPiRejectsUnresolvedCredentialReferences(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	hostHome := t.TempDir()
	a, _ := agent.Lookup("pi")
	nativeDir := filepath.Join(hostHome, ".pi", "agent")
	if err := os.MkdirAll(nativeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, nativeDir, "auth.json", `{"openai":{"type":"api_key","key":"$OPENAI_API_KEY"}}`)
	if _, err := Build(a, hostHome); err == nil || !strings.Contains(err.Error(), "unresolved") {
		t.Fatalf("unresolved Pi auth reference error = %v", err)
	}
	testutil.WriteFile(t, nativeDir, "auth.json", `{"openai":{"type":"api_key","key":"!security-tool"}}`)
	if _, err := Build(a, hostHome); err == nil || !strings.Contains(err.Error(), "unresolved") {
		t.Fatalf("command Pi auth reference error = %v", err)
	}
}

func TestPlanPiRejectsAuthEnvironmentOverrides(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	hostHome := t.TempDir()
	a, _ := agent.Lookup("pi")
	nativeDir := filepath.Join(hostHome, ".pi", "agent")
	if err := os.MkdirAll(nativeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(
		t,
		nativeDir,
		"auth.json",
		`{"openai":{"type":"api_key","key":"token","env":{"SOURCE":"$OTHER"}}}`,
	)
	if _, err := Build(a, hostHome); err == nil || !strings.Contains(err.Error(), "unresolved") {
		t.Fatalf("unresolved Pi auth environment error = %v", err)
	}
	testutil.WriteFile(
		t,
		nativeDir,
		"auth.json",
		`{"openai":{"type":"api_key","key":"token","env":{"SOURCE":"literal"}}}`,
	)
	if _, err := Build(a, hostHome); err == nil || !strings.Contains(err.Error(), "unsupported environment") {
		t.Fatalf("raw Pi auth environment error = %v", err)
	}
}

func TestPlanPiRejectsCredentialHeadersAndQueryParameters(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	hostHome := t.TempDir()
	a, _ := agent.Lookup("pi")
	nativeDir := filepath.Join(hostHome, ".pi", "agent")
	if err := os.MkdirAll(nativeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(
		t,
		nativeDir,
		"models.json",
		`{"providers":{"gateway":{"baseUrl":"https://gateway.example.test/v1?api-key=secret"}}}`,
	)
	if _, err := Build(a, hostHome); err == nil || !strings.Contains(err.Error(), "credential query") {
		t.Fatalf("query credential error = %v", err)
	}
	testutil.WriteFile(
		t,
		nativeDir,
		"models.json",
		`{"providers":{"gateway":{"headers":{"X-Workspace-Token":"secret"}}}}`,
	)
	if _, err := Build(a, hostHome); err == nil || !strings.Contains(err.Error(), "header override") {
		t.Fatalf("header credential error = %v", err)
	}
}

func TestSupplementalMigrationBranches(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	hostHome := t.TempDir()
	a, _ := agent.Lookup("pi")
	spec := a.(agent.MigrationSpecProvider).MigrationSpec()
	nativeDir := filepath.Join(hostHome, ".pi", "agent")
	if err := os.MkdirAll(nativeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	modelsPath := filepath.Join(nativeDir, "models.json")
	testutil.WritePath(t, modelsPath, "{")
	files, _, warnings, _, err := migrateNativeSupplementalConfig(hostHome, spec)
	if err != nil || len(files) != 0 || len(warnings) != 1 {
		t.Fatalf("malformed supplemental migration = files=%v warnings=%v err=%v", files, warnings, err)
	}
	if err := os.Remove(modelsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(modelsPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := migrateNativeSupplementalConfig(hostHome, spec); err == nil {
		t.Fatal("expected supplemental read error")
	}
	if err := os.Remove(modelsPath); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, modelsPath, `{"providers":{"gateway":{"apiKey":"secret"}}}`)
	withMigrationSeams(t)
	marshalErr := errors.New("supplemental marshal failed")
	marshalJSONIndentFn = func(any, string, string) ([]byte, error) { return nil, marshalErr }
	if _, _, _, _, err := migrateNativeSupplementalConfig(hostHome, spec); !errors.Is(err, marshalErr) {
		t.Fatalf("supplemental marshal error = %v", err)
	}
}

func TestSupplementalMigrationSecretCollision(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	pi, _ := agent.Lookup("pi")
	spec := pi.(agent.MigrationSpecProvider).MigrationSpec()
	spec.NativeSupplementalFiles = []string{"models.json", "models-extra.json"}
	spec.ManagedSupplementalFiles = []string{"models.json", "models-extra.json"}
	hostHome := t.TempDir()
	nativeDir := filepath.Join(hostHome, ".pi", "agent")
	if err := os.MkdirAll(nativeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, nativeDir, "models.json", `{"providers":{"gateway":{"apiKey":"one"}}}`)
	testutil.WriteFile(t, nativeDir, "models-extra.json", `{"providers":{"gateway":{"apiKey":"two"}}}`)
	if _, _, _, _, err := migrateNativeSupplementalConfig(
		hostHome,
		spec,
	); err == nil ||
		!strings.Contains(err.Error(), "collision") {
		t.Fatalf("supplemental secret collision error = %v", err)
	}
}

func TestClaudeSecretFieldAndEndpointTraversalBranches(t *testing.T) {
	claude, _ := agent.Lookup("claude-code")
	spec := claude.(agent.MigrationSpecProvider).MigrationSpec()
	secrets := make(map[string]Secret)
	values := map[string]any{"ANTHROPIC_API_KEY": "one"}
	if err := migrateClaudeSecretField(
		values,
		"ANTHROPIC_API_KEY",
		"one",
		"",
		spec.Auth,
		spec.KnownProviderHosts,
		nil,
		secrets,
	); err != nil {
		t.Fatal(err)
	}
	if err := migrateClaudeSecretField(
		values,
		"ANTHROPIC_API_KEY",
		"two",
		"",
		spec.Auth,
		spec.KnownProviderHosts,
		nil,
		secrets,
	); err == nil {
		t.Fatal("expected Claude secret collision")
	}
	endpointValues := map[string]any{"ANTHROPIC_BASE_URL": "https://gateway.example.test/v1"}
	if err := migrateClaudeSecretField(
		endpointValues,
		"ANTHROPIC_BASE_URL",
		"https://gateway.example.test/v1",
		"",
		spec.Auth,
		spec.KnownProviderHosts,
		nil,
		make(map[string]Secret),
	); err != nil {
		t.Fatal(err)
	}
	if got := findEndpointHost(
		map[string]any{"items": []any{map[string]any{"baseUrl": "https://array.example.test"}}},
		spec.Auth.EndpointFields,
	); got != "array.example.test" {
		t.Fatalf("array endpoint host = %q", got)
	}
}

func TestNormalizeClaudeEndpoint(t *testing.T) {
	tests := []struct {
		name, input, wantHost, wantURL, wantError string
	}{
		{name: "default", input: "", wantHost: "api.anthropic.com", wantURL: "https://api.anthropic.com"},
		{
			name:     "host",
			input:    "gateway.example.test/v1",
			wantHost: "gateway.example.test",
			wantURL:  "https://gateway.example.test/v1",
		},
		{
			name:     "url",
			input:    "http://gateway.example.test/api",
			wantHost: "gateway.example.test",
			wantURL:  "http://gateway.example.test/api",
		},
		{name: "userinfo", input: "https://user:pass@gateway.example.test", wantError: "URL credentials"},
		{name: "query", input: "https://gateway.example.test?api-key=secret", wantError: "query parameters"},
		{name: "scheme", input: "ftp://gateway.example.test", wantError: "http(s)"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			host, endpoint, err := normalizeClaudeEndpoint(testCase.input)
			if testCase.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
					t.Fatalf("error = %v, want %q", err, testCase.wantError)
				}
				return
			}
			if err != nil || host != testCase.wantHost || endpoint != testCase.wantURL {
				t.Fatalf("normalize = %q, %q, %v", host, endpoint, err)
			}
		})
	}
}

func TestSanitizeClaudeSettingBranches(t *testing.T) {
	claude, _ := agent.Lookup("claude-code")
	spec := claude.(agent.MigrationSpecProvider).MigrationSpec()
	known := spec.KnownProviderHosts
	for _, testCase := range []struct {
		name string
		data map[string]any
		want string
	}{
		{name: "unresolved", data: map[string]any{"value": "$OTHER"}, want: "unresolved"},
		{name: "endpoint credentials", data: map[string]any{"ANTHROPIC_BASE_URL": "https://user:pass@example.test"}, want: "URL credentials"},
		{name: "helper", data: map[string]any{"apiKeyHelper": "vault read key"}, want: "credential helper"},
		{name: "header", data: map[string]any{"env": map[string]any{"CUSTOM_HEADER": "literal"}}, want: "header override"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if err := sanitizeConfigMapWithSpec(
				testCase.data,
				"",
				"",
				spec.Auth,
				known,
				nil,
				map[string]Secret{},
			); err == nil ||
				!strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestSanitizeClaudeNestedEnvSecretCollision(t *testing.T) {
	claude, _ := agent.Lookup("claude-code")
	spec := claude.(agent.MigrationSpecProvider).MigrationSpec()
	values := map[string]any{
		"first":  map[string]any{"ANTHROPIC_API_KEY": "one"},
		"second": map[string]any{"ANTHROPIC_API_KEY": "two"},
	}
	if err := sanitizeConfigMapWithSpec(
		values,
		"",
		"",
		spec.Auth,
		spec.KnownProviderHosts,
		nil,
		map[string]Secret{},
	); err == nil ||
		!strings.Contains(err.Error(), "collision") {
		t.Fatalf("nested Claude secret collision error = %v", err)
	}
}

func TestConfigSecretHostsUsesGlobalEndpointFallback(t *testing.T) {
	if got := configSecretHosts(
		"",
		map[string]any{},
		nil,
		map[string]string{"": "gateway.example.test"},
	); !slicesEqual(
		got,
		[]string{"gateway.example.test"},
	) {
		t.Fatalf("global endpoint host = %v", got)
	}
}

func TestPiModelsAreNotCopiedByNativeProvisioning(t *testing.T) {
	a, _ := agent.Lookup("pi")
	provisioner, ok := agent.AsProvisioner(a)
	if !ok {
		t.Fatal("pi should implement Provisioner")
	}
	patterns := provisioner.ProvisionRules()[0].Patterns
	for _, excluded := range []string{"!auth.json", "!models.json"} {
		if !slices.Contains(patterns, excluded) {
			t.Errorf("Pi provision patterns = %v, missing %q", patterns, excluded)
		}
	}
}

func TestBuildUsesOpenCodeXDGPaths(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	hostHome := t.TempDir()
	configHome := filepath.Join(hostHome, "xdg-config")
	dataHome := filepath.Join(hostHome, "xdg-data")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_DATA_HOME", dataHome)
	a, _ := agent.Lookup("opencode")

	configDir := filepath.Join(configHome, "opencode")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, configDir, "opencode.json", `{"model":"openai/gpt-5"}`)
	authDir := filepath.Join(dataHome, "opencode")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, authDir, "auth.json", `{"openai":{"type":"api","key":"xdg-token"}}`)

	plan, err := Build(a, hostHome)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !plan.HasNativeConfig || len(plan.Files) != 2 {
		t.Fatalf("XDG native config was not fully discovered: %+v", plan)
	}
}

func TestPlanDerivesEnterpriseAndCustomHosts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")
	authPath := filepath.Join(hostHome, ".local", "share", "opencode", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, authPath, `{
  "github-copilot": {
    "type": "oauth",
    "access": "enterprise-access",
    "refresh": "enterprise-refresh",
    "expires": 0,
    "enterpriseUrl": "https://ghe.example.test"
  }
}`)
	configDir := filepath.Join(hostHome, ".config", "opencode")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, configDir, "opencode.json", `{
  "provider": {
    "custom": {
      "options": {
        "baseURL": "https://gateway.example.test/v1",
        "apiKey": "custom-key"
      }
    }
  }
}`)

	plan, err := Build(a, hostHome)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := plan.Secrets["OPENCODE_GITHUB_COPILOT_ACCESS"].Hosts; !slicesEqual(
		got,
		[]string{"copilot-api.ghe.example.test"},
	) {
		t.Errorf("enterprise host = %v", got)
	}
	if got := plan.Secrets["OPENCODE_CONFIG_PROVIDER_CUSTOM_OPTIONS_APIKEY"].Hosts; !slicesEqual(
		got,
		[]string{"gateway.example.test"},
	) {
		t.Errorf("custom endpoint host = %v", got)
	}
}

func TestPlanReportsUnsupportedAuthFields(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")
	authPath := filepath.Join(hostHome, ".local", "share", "opencode", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, authPath, `{"openai":{"type":"api","key":"secret","customCredential":"review-me"}}`)
	plan, err := Build(a, hostHome)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.ReviewWarnings) == 0 || !strings.Contains(plan.ReviewWarnings[0], "customCredential") {
		t.Fatalf("review warnings = %v", plan.ReviewWarnings)
	}
}

func TestUnsupportedAuthFieldWarningBranches(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	spec := a.(agent.MigrationSpecProvider).MigrationSpec()
	warnings := unsupportedAuthFieldWarnings(
		map[string]json.RawMessage{
			"metadata": json.RawMessage(`{"custom":"value"}`),
			"numeric":  json.RawMessage(`123`),
		},
		"provider",
		"",
		spec.Auth,
	)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "metadata.custom") {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestPlanDoesNotMigrateUnsupportedOnlyAuth(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")
	authPath := filepath.Join(hostHome, ".local", "share", "opencode", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, authPath, `{"custom":{"type":"unsupported","token":"raw-token"}}`)

	plan, err := Build(a, hostHome)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Files) != 0 || plan.Handled || plan.AlreadyConfigured {
		t.Fatalf("unsupported auth was treated as migrated: %+v", plan)
	}
}

func TestPlanPreservesWellKnownAuthKey(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")
	authPath := filepath.Join(hostHome, ".local", "share", "opencode", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(
		t,
		authPath,
		`{"https://provider.example":{"type":"wellknown","key":"TOKEN_NAME","token":"token-value"}}`,
	)

	plan, err := Build(a, hostHome)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	authFile, ok := plan.fileAt(filepath.Join(configpaths.Get().UserAgentConfigDir(a), "auth.json"))
	if !ok {
		t.Fatal("missing migrated auth file")
	}
	if !bytes.Contains(authFile.Data, []byte(`"key": "TOKEN_NAME"`)) ||
		bytes.Contains(authFile.Data, []byte("token-value")) {
		t.Fatalf("well-known auth fields were handled incorrectly: %s", authFile.Data)
	}
	if _, ok := plan.Secrets["OPENCODE_HTTPS_PROVIDER_EXAMPLE_TOKEN"]; !ok {
		t.Fatalf("well-known token was not migrated: %+v", plan.Secrets)
	}
}

func TestPlanRedactsCredentialLikeConfigFields(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")
	configDir := filepath.Join(hostHome, ".config", "opencode")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, configDir, "opencode.json", `{
  "provider": {
    "custom": {
      "options": {
        "clientSecret": "client-secret",
        "headers": {"x-api-key": "header-secret"}
      }
    }
  }
}`)

	plan, err := Build(a, hostHome)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	data := string(plan.Files[0].Data)
	if strings.Contains(data, "client-secret") || strings.Contains(data, "header-secret") {
		t.Fatalf("credential-like config value leaked: %s", data)
	}
}

func TestApplyWritesUserSecretFileAndHomeMapping(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")
	authPath := filepath.Join(hostHome, ".local", "share", "opencode", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, authPath, `{"openrouter":{"type":"api","key":"secret-key"}}`)

	plan, err := Build(a, hostHome)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	ui := termio.NewTestMock(t)
	if err := Apply(plan, &ui); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	generatedAuth := filepath.Join(configpaths.Get().UserAgentConfigDir(a), "auth.json")
	data, err := os.ReadFile(generatedAuth)
	if err != nil {
		t.Fatalf("read generated auth: %v", err)
	}
	if bytes.Contains(data, []byte("secret-key")) {
		t.Fatalf("generated auth contains raw secret: %s", data)
	}
	secretData, err := os.ReadFile(configpaths.Get().UserEnvSecretYAMLFile())
	if err != nil {
		t.Fatalf("read generated secrets: %v", err)
	}
	if !bytes.Contains(secretData, []byte("value: secret-key")) {
		t.Fatalf("generated secret file has no raw value: %s", secretData)
	}
	configData, err := os.ReadFile(filepath.Join(configpaths.Get().UserConfigDir(), "config.yaml"))
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	if !bytes.Contains(configData, []byte(".local/share/opencode/auth.json")) {
		t.Fatalf("generated config has no auth mapping: %s", configData)
	}

	mode, err := os.Stat(configpaths.Get().UserEnvSecretYAMLFile())
	if err != nil {
		t.Fatal(err)
	}
	if got := mode.Mode().Perm(); got != 0o600 {
		t.Errorf("secret file mode = %04o, want 0600", got)
	}
}

func TestApplyRejectsUnknownSecretHostInNonInteractiveMode(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")
	authPath := filepath.Join(hostHome, ".local", "share", "opencode", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, authPath, `{"custom":{"type":"api","key":"secret-key"}}`)

	plan, err := Build(a, hostHome)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	ui := termio.NewTestMock(t)
	if err := Apply(plan, &ui); err == nil || !strings.Contains(err.Error(), "allowed host") {
		t.Fatalf("Apply error = %v, want missing allowed host", err)
	}
	if _, err := os.Stat(configpaths.Get().UserEnvSecretYAMLFile()); !os.IsNotExist(err) {
		t.Fatalf("secret file was written after rejected migration: %v", err)
	}
}

func TestPlanDetectsNoNativeConfiguration(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	a, _ := agent.Lookup("opencode")
	plan, err := Build(a, t.TempDir())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.HasChanges() {
		t.Fatalf("empty native configuration produced changes: %+v", plan)
	}
}

func TestPlanMigratesNativeOpenCodeConfigAsManagedSnippet(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")
	configDir := filepath.Join(hostHome, ".config", "opencode")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(
		t,
		configDir,
		"opencode.json",
		`{"model":"openai/gpt-5","provider":{"openai":{"options":{"baseURL":"https://api.openai.com/v1"}}}}`,
	)

	plan, err := Build(a, hostHome)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Files) != 1 {
		t.Fatalf("planned files = %d, want one managed snippet", len(plan.Files))
	}
	if !strings.HasSuffix(plan.Files[0].Path, filepath.Join("opencode", "opencode-migrated.jsonc")) {
		t.Errorf("managed config path = %q", plan.Files[0].Path)
	}
	if !bytes.Contains(plan.Files[0].Data, []byte(`"model": "openai/gpt-5"`)) {
		t.Errorf("managed snippet lost config: %s", plan.Files[0].Data)
	}
}

func TestGuideNonInteractiveDoesNotWrite(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")
	authPath := filepath.Join(hostHome, ".local", "share", "opencode", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, authPath, `{"openrouter":{"type":"api","key":"secret-key"}}`)

	ui := termio.NewTestMock(t)
	if err := Guide(a, hostHome, false, &ui); err != nil {
		t.Fatalf("Guide: %v", err)
	}
	if _, err := os.Stat(configpaths.Get().UserEnvSecretYAMLFile()); !os.IsNotExist(err) {
		t.Fatalf("non-interactive guide wrote secrets: %v", err)
	}
	if len(ui.WarnCalls) != 1 || !strings.Contains(ui.WarnCalls[0], "config migrate") {
		t.Errorf("unexpected warnings: %v", ui.WarnCalls)
	}
}

func TestGuideNativeProvisioningChoiceRequiresRerun(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")
	authPath := filepath.Join(hostHome, ".local", "share", "opencode", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, authPath, `{"openrouter":{"type":"api","key":"secret-key"}}`)
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return "p", nil }

	err := Guide(a, hostHome, false, &ui)
	if !errors.Is(err, ErrRerunRequired) {
		t.Fatalf("Guide error = %v, want ErrRerunRequired", err)
	}
	configData, readErr := os.ReadFile(filepath.Join(configpaths.Get().UserConfigDir(), "config.yaml"))
	if readErr != nil {
		t.Fatalf("read enabled config: %v", readErr)
	}
	if !bytes.Contains(configData, []byte("provision-host-config: true")) {
		t.Fatalf("native provisioning was not enabled: %s", configData)
	}
}

func TestDismissRecordsAndDetectsDismissedMigration(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	path := filepath.Join(hostHome, ".local", "share", "opencode", "auth.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, path, `{"custom":{"type":"api","key":"secret"}}`)
	a, _ := agent.Lookup("opencode")
	plan, err := Build(a, hostHome)
	if err != nil {
		t.Fatal(err)
	}
	if err := Dismiss(plan); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}
	updated, err := Build(a, hostHome)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Dismissed || updated.HasChanges() {
		t.Fatalf("dismissed migration state = %+v", updated)
	}
}

func TestGuideDismissChoiceContinues(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	path := filepath.Join(hostHome, ".local", "share", "opencode", "auth.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, path, `{"custom":{"type":"api","key":"secret"}}`)
	a, _ := agent.Lookup("opencode")
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return "d", nil }
	if err := Guide(a, hostHome, false, &ui); err != nil {
		t.Fatalf("Guide: %v", err)
	}
	if len(ui.InfoCalls) == 0 || !strings.Contains(ui.InfoCalls[0], "without migrated") {
		t.Errorf("dismiss output = %v", ui.InfoCalls)
	}
}

func TestGuideSafeMigrationChoiceApplies(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	writeNativeAuth(t, hostHome)
	a, _ := agent.Lookup("opencode")
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	choices := []string{"m", "y"}
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		choice := choices[0]
		choices = choices[1:]
		return choice, nil
	}
	if err := Guide(a, hostHome, false, &ui); err != nil {
		t.Fatalf("Guide: %v", err)
	}
	if len(ui.InfoCalls) < 2 || !strings.Contains(ui.InfoCalls[0], "migration completed") || ui.InfoCalls[1] != "" {
		t.Errorf("migration output = %v", ui.InfoCalls)
	}
}

func TestBuildRecognizesAppliedMigration(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	writeNativeAuth(t, hostHome)
	a, _ := agent.Lookup("opencode")
	plan, err := Build(a, hostHome)
	if err != nil {
		t.Fatal(err)
	}
	ui := termio.NewTestMock(t)
	if err := Apply(plan, &ui); err != nil {
		t.Fatal(err)
	}
	updated, err := Build(a, hostHome)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.AlreadyConfigured || updated.HasChanges() {
		t.Fatalf("applied migration state = %+v", updated)
	}
}

func TestBuildLauncherConfigFormatsAndConflicts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	a, _ := agent.Lookup("opencode")
	testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "config.json", `{"network":{"profile":"none"}}`)
	path, data, err := buildLauncherConfig(a, a.(interface {
		MigrationSpec() agent.ConfigMigrationSpec
	}).MigrationSpec())
	if err != nil || filepath.Ext(path) != ".json" || !bytes.Contains(data, []byte("auth.json")) {
		t.Fatalf("JSON launcher config = %q, %s, %v", path, data, err)
	}
	testutil.WriteFile(
		t,
		configpaths.Get().UserConfigDir(),
		"config.yaml",
		"home:\n  .local/share/opencode/auth.json: other\n",
	)
	if _, _, err := buildLauncherConfig(a, a.(interface {
		MigrationSpec() agent.ConfigMigrationSpec
	}).MigrationSpec()); err == nil {
		t.Fatal("expected conflicting home mapping error")
	}
}

func TestApplyErrorBranches(t *testing.T) {
	ui := termio.NewTestMock(t)
	if err := Apply(nil, &ui); err == nil {
		t.Fatal("expected nil plan error")
	}
	if err := Dismiss(nil); err == nil {
		t.Fatal("expected nil dismiss plan error")
	}
	ui = termio.NewTestMock(t)
	ui.InputFn = func(string, string) (string, error) { return "", errors.New("input failed") }
	plan := &Plan{Secrets: map[string]Secret{"TOKEN": {Value: "value"}}}
	if err := Apply(plan, &ui); err == nil || !strings.Contains(err.Error(), "input failed") {
		t.Fatalf("Apply input error = %v", err)
	}
}

func TestGuideEarlyAndUnknownChoices(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	a, _ := agent.Lookup("opencode")
	ui := termio.NewTestMock(t)
	if err := Guide(a, t.TempDir(), true, &ui); err != nil {
		t.Fatal(err)
	}
	unsupported, _ := agent.Lookup("pi")
	ui = termio.NewTestMock(t)
	if err := Guide(unsupported, t.TempDir(), false, &ui); err != nil {
		t.Fatal(err)
	}
	hostHome := t.TempDir()
	writeNativeAuth(t, hostHome)
	ui = termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return "x", nil }
	if err := Guide(a, hostHome, false, &ui); err == nil || !strings.Contains(err.Error(), "unknown migration choice") {
		t.Fatalf("unknown choice error = %v", err)
	}
}

func TestSanitizeAuthRejectsMalformedAndUnsupportedValues(t *testing.T) {
	if _, _, _, err := sanitizeAuth([]byte("{"), nil, nil); err == nil {
		t.Fatal("expected malformed auth error")
	}
	if generated, _, warnings, err := sanitizeAuth(
		[]byte(`{"bad":1}`),
		nil,
		nil,
	); err != nil || generated != nil ||
		len(warnings) == 0 {
		t.Fatalf("unsupported auth result = %q, %v, %v", generated, warnings, err)
	}
	if _, _, _, err := sanitizeAuth([]byte(`{"openai":{"type":"api","key":1}}`), nil, nil); err == nil {
		t.Fatal("expected non-string auth field error")
	}
}

func TestSanitizeConfigRejectsEndpointCredentialsAndArrays(t *testing.T) {
	values := map[string]any{"provider": map[string]any{"custom": map[string]any{
		"options": map[string]any{"baseURL": "https://user:pass@example.test"},
	}}}
	if err := sanitizeConfigMap(values, "", "", nil, nil, map[string]Secret{}); err == nil {
		t.Fatal("expected embedded endpoint credential error")
	}
	values = map[string]any{"items": []any{map[string]any{"clientSecret": "secret"}}}
	secrets := map[string]Secret{}
	if err := sanitizeConfigMap(values, "", "", nil, nil, secrets); err != nil || len(secrets) != 1 {
		t.Fatalf("array sanitization = %v, %+v", err, values)
	}
}

func TestSanitizeConfigFallbacksAndRecursiveErrors(t *testing.T) {
	values := map[string]any{
		"items": []any{map[string]any{
			"endpoint": "https://user:pass@example.test",
		}},
	}
	if err := sanitizeConfigMap(values, "", "", nil, nil, map[string]Secret{}); err == nil {
		t.Fatal("expected embedded endpoint credential error in array")
	}

	values = map[string]any{"api-key": "one", "api_key": "two"}
	secrets := map[string]Secret{}
	if err := sanitizeConfigMap(values, "", "", nil, nil, secrets); err != nil || len(secrets) != 2 {
		t.Fatalf("config secret collision handling = %v, %+v", err, secrets)
	}
	if got := configSecretHosts(
		"custom",
		map[string]any{},
		map[string]string{"custom": "known.test"},
		nil,
	); !slicesEqual(
		got,
		[]string{"known.test"},
	) {
		t.Fatalf("known config host = %v", got)
	}
	if got := configSecretHosts(
		"custom",
		map[string]any{},
		nil,
		map[string]string{"custom": "provider.test"},
	); !slicesEqual(
		got,
		[]string{"provider.test"},
	) {
		t.Fatalf("provider config host = %v", got)
	}
}

func TestSanitizeAuthVariants(t *testing.T) {
	tests := []struct {
		name      string
		data      string
		wantError string
		wantWarn  bool
		wantFile  bool
	}{
		{name: "missing type", data: `{"provider":{}}`, wantWarn: true},
		{name: "unsupported type", data: `{"provider":{"type":"other"}}`, wantWarn: true},
		{name: "non string field", data: `{"provider":{"type":"api","key":1}}`, wantError: "not a string"},
		{name: "placeholder", data: `{"provider":{"type":"api","key":"Bearer {env:TOKEN}"}}`, wantFile: true},
		{
			name:     "dummy oauth",
			data:     `{"provider":{"type":"oauth","access":"opencode-oauth-dummy-key","refresh":"$MSB_REFRESH"}}`,
			wantFile: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			generated, _, warnings, err := sanitizeAuth([]byte(tc.data), nil, nil)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error = %v, want %q", err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("sanitizeAuth: %v", err)
			}
			if tc.wantWarn != (len(warnings) > 0) {
				t.Errorf("warnings = %v, want warning=%v", warnings, tc.wantWarn)
			}
			if tc.wantFile != (len(generated) > 0) {
				t.Errorf("generated file length = %d, want file=%v", len(generated), tc.wantFile)
			}
		})
	}
	if _, _, _, err := sanitizeAuth([]byte(`{"provider":{"type":"api"}}`), nil, nil); err != nil {
		t.Fatalf("missing auth field should be ignored: %v", err)
	}
	if _, _, _, err := sanitizeAuth(
		[]byte(`{"provider":{"type":"api","key":"one","metadata":{"client-secret":"two","client_secret":"three"}}}`),
		nil,
		nil,
	); err == nil {
		t.Fatal("expected recursive auth collision")
	}

	if _, _, _, err := sanitizeAuth(
		[]byte(`{"a-b":{"type":"api","key":"one"},"a_b":{"type":"api","key":"two"}}`),
		nil,
		nil,
	); err == nil {
		t.Fatal("expected auth secret collision")
	}
}

func TestMigrateNativeConfigReadAndSanitizeErrors(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	a, _ := agent.Lookup("opencode")
	spec := a.(interface {
		MigrationSpec() agent.ConfigMigrationSpec
	}).MigrationSpec()
	hostHome := t.TempDir()
	configDir := filepath.Join(hostHome, ".config", "opencode")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "opencode.json")
	if err := os.Mkdir(configPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := migrateNativeConfig(hostHome, spec); err == nil {
		t.Fatal("expected native config read error")
	}
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(
		t,
		configPath,
		`{"provider":{"custom":{"options":{"baseURL":"https://user:pass@example.test","apiKey":"secret"}}}}`,
	)
	if _, _, _, _, err := migrateNativeConfig(hostHome, spec); err == nil {
		t.Fatal("expected native config sanitization error")
	}
}

func TestSanitizeAuthMapNestedAndCollision(t *testing.T) {
	values := map[string]json.RawMessage{
		"metadata": json.RawMessage(`{"clientSecret":"nested"}`),
	}
	secrets := map[string]Secret{}
	if err := sanitizeAuthMap(values, "provider", "", nil, nil, secrets); err != nil {
		t.Fatalf("sanitizeAuthMap: %v", err)
	}
	if len(secrets) != 1 || !bytes.Contains(values["metadata"], []byte("$MSB_")) {
		t.Fatalf("nested auth result = %s, %+v", values["metadata"], secrets)
	}
	collision := map[string]json.RawMessage{"clientSecret": json.RawMessage(`"two"`)}
	prior := map[string]Secret{"OPENCODE_PROVIDER_CLIENTSECRET": {Value: "one"}}
	if err := sanitizeAuthMap(collision, "provider", "", nil, nil, prior); err == nil {
		t.Fatal("expected nested auth secret collision")
	}
	nestedCollision := map[string]json.RawMessage{
		"metadata": json.RawMessage(`{"clientSecret":"two"}`),
	}
	prior = map[string]Secret{"OPENCODE_PROVIDER_METADATA_CLIENTSECRET": {Value: "one"}}
	if err := sanitizeAuthMap(nestedCollision, "provider", "", nil, nil, prior); err == nil {
		t.Fatal("expected recursive auth secret collision")
	}
}

func TestParseNativeConfigAndMergeConfigMaps(t *testing.T) {
	if parsed, err := parseNativeConfig(
		"settings.yaml",
		[]byte("model: test\n"),
	); err != nil ||
		parsed["model"] != "test" {
		t.Errorf("YAML parse = %#v, %v", parsed, err)
	}
	if _, err := parseNativeConfig("settings.json", []byte("{")); err == nil {
		t.Error("expected malformed JSON error")
	}
	if _, err := parseNativeConfig("settings.yaml", []byte("model: [")); err == nil {
		t.Error("expected malformed YAML error")
	}
	merged := mergeConfigMaps(
		map[string]any{"nested": map[string]any{"a": 1}},
		map[string]any{"nested": map[string]any{"b": 2}},
	)
	if got := merged["nested"].(map[string]any); got["a"] != 1 || got["b"] != 2 {
		t.Errorf("merged config = %#v", merged)
	}
}

func TestLauncherConfigAndSecretFileBranches(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	a, _ := agent.Lookup("opencode")
	spec := a.(interface {
		MigrationSpec() agent.ConfigMigrationSpec
	}).MigrationSpec()
	testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "config.json", `{}`)
	path, data, err := buildLauncherConfig(a, spec)
	if err != nil || filepath.Ext(path) != ".json" || !bytes.Contains(data, []byte("auth.json")) {
		t.Fatalf("JSON launcher config = %q, %s, %v", path, data, err)
	}
	testutil.WriteFile(
		t,
		configpaths.Get().UserConfigDir(),
		"config.yaml",
		"home:\n  .local/share/opencode/auth.json: other\n",
	)
	if _, _, err := buildLauncherConfig(a, spec); err == nil {
		t.Fatal("expected conflicting mapping error")
	}
	secretPath := filepath.Join(configpaths.Get().UserConfigDir(), "secrets.yaml")
	mergedSecrets, err := migrationMergeSecretFile(secretPath, map[string]Secret{"TOKEN": {Value: "one"}})
	if err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, secretPath, string(mergedSecrets))
	if _, err := migrationMergeSecretFile(secretPath, map[string]Secret{"TOKEN": {Value: "two"}}); err == nil {
		t.Fatal("expected secret conflict")
	}
	testutil.WritePath(t, secretPath, "not: [valid")
	if _, err := migrationMergeSecretFile(secretPath, nil); err == nil {
		t.Fatal("expected malformed secret file error")
	}
}

func TestUpdateLauncherNetworkAndConflict(t *testing.T) {
	path, data, added, err := updateLauncherNetwork(
		"config.yaml",
		[]byte("network:\n  egress-allow:\n    - existing.test\n"),
		[]string{"api.example.test"},
	)
	if err != nil || path != "config.yaml" || len(added) != 1 || !bytes.Contains(data, []byte("api.example.test")) {
		t.Fatalf("network update = %q, %s, %v", path, data, err)
	}
	if _, _, _, err := updateLauncherNetwork(
		"config.yaml",
		[]byte("network:\n  egress-deny:\n    - api.example.test\n"),
		[]string{"api.example.test"},
	); err == nil {
		t.Fatal("expected network deny conflict")
	}
}

func TestUpdateLauncherNetworkRejectsHostDeniedInCommaSeparatedString(t *testing.T) {
	_, _, _, err := updateLauncherNetwork(
		"config.yaml",
		[]byte("network:\n  egress-deny: \"evil.test, api.example.test\"\n"),
		[]string{"api.example.test"},
	)
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("updateLauncherNetwork error = %v, want deny conflict", err)
	}
}

func TestStringList(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  []string
	}{
		{name: "nil", value: nil, want: nil},
		{name: "empty string", value: "", want: nil},
		{
			name:  "comma-separated string",
			value: "a.test, b.test ,,c.test",
			want:  []string{"a.test", "b.test", "c.test"},
		},
		{name: "string slice", value: []string{" a.test", "", "b.test "}, want: []string{"a.test", "b.test"}},
		{name: "any slice", value: []any{" a.test", 1, "", "b.test"}, want: []string{"a.test", "b.test"}},
		{name: "unsupported type", value: 42, want: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := stringList(test.value); !slicesEqual(got, test.want) {
				t.Fatalf("stringList(%#v) = %#v, want %#v", test.value, got, test.want)
			}
		})
	}
}

func TestReportListsGeneratedFilesWithoutSecretValues(t *testing.T) {
	plan := &Plan{
		Files:      []File{{Path: "/tmp/auth.json", Data: []byte(`{"key":"$MSB_KEY"}`)}},
		ConfigPath: "/tmp/config.yaml",
		ConfigData: []byte("network:\n  egress-allow: [api.example.test]\n"),
		SecretPath: "/tmp/env.secret.yaml",
	}
	ui := termio.NewTestMock(t)
	Report(plan, &ui)
	output := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(output, "/tmp/auth.json") || !strings.Contains(output, "/tmp/env.secret.yaml") {
		t.Fatalf("report = %s", output)
	}
	if strings.Contains(output, "secret-value") {
		t.Fatalf("report leaked secret value: %s", output)
	}
}

func TestHasManagedConfigIgnoresUnrelatedFiles(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a, _ := agent.Lookup("opencode")
	testutil.WriteFile(t, configpaths.Get().UserAgentConfigDir(a), "theme.json", "{}")
	spec := a.(agent.MigrationSpecProvider).MigrationSpec()
	if hasManagedConfig(a, spec) {
		t.Fatal("unrelated managed file should not suppress migration")
	}
}

func TestEnableNativeProvisioningJSONAndStatusBranches(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "config.json", `{}`)
	if err := EnableNativeProvisioning(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(configpaths.Get().UserConfigDir(), "config.json"))
	if err != nil || !bytes.Contains(data, []byte(`"provision-host-config": true`)) {
		t.Fatalf("enabled config = %s, %v", data, err)
	}
	a, _ := agent.Lookup("opencode")
	if _, ok := migrationStatusFor(a, "missing"); ok {
		t.Fatal("unexpected migration status")
	}
}

type unsupportedMigrationAgent struct{}

type migrationStatusAgent struct{ name string }

func (a migrationStatusAgent) Name() string { return a.name }

func (unsupportedMigrationAgent) Name() string               { return "unsupported" }
func (unsupportedMigrationAgent) ConfigDirName() string      { return "unsupported" }
func (unsupportedMigrationAgent) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }

func TestBuildAndNativeReadErrors(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	if _, err := Build(unsupportedMigrationAgent{}, t.TempDir()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported agent error = %v", err)
	}
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")
	configPath := filepath.Join(hostHome, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(configPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(a, hostHome); err == nil {
		t.Fatal("expected native config directory read error")
	}
	if err := os.RemoveAll(configPath); err != nil {
		t.Fatal(err)
	}
	writeNativeAuth(t, hostHome)
	authPath := filepath.Join(hostHome, ".local", "share", "opencode", "auth.json")
	if err := os.Remove(authPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(authPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(a, hostHome); err == nil {
		t.Fatal("expected auth file read error")
	}
}

func TestBuildConfigAndAuthErrors(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")
	writeNativeAuth(t, hostHome)
	if err := os.MkdirAll(filepath.Join(hostHome, ".config", "opencode"), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, filepath.Join(hostHome, ".config", "opencode"), "opencode.json", `{`)
	plan, err := Build(a, hostHome)
	if err != nil || plan == nil || len(plan.Files) != 1 {
		t.Fatalf("malformed native config result = %+v, %v", plan, err)
	}
	if err := os.Remove(filepath.Join(hostHome, ".config", "opencode", "opencode.json")); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configpaths.Get().UserConfigDir(), "config.yaml")
	testutil.WritePath(t, configFile, "home: [")
	if _, err := Build(a, hostHome); err == nil {
		t.Fatal("expected malformed launcher config error")
	}
	_ = os.Remove(configFile)
	writeNativeAuth(t, hostHome)
	if _, _, _, err := sanitizeAuth([]byte(`{"provider":{"type":"api","key":1}}`), nil, nil); err == nil {
		t.Fatal("expected non-string auth error")
	}
}

func TestApplyWriteAndSecretValidationErrors(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	temp := t.TempDir()
	parent := filepath.Join(temp, "parent")
	if err := os.WriteFile(parent, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := &Plan{Files: []File{{Path: filepath.Join(parent, "child"), Data: []byte("x"), Mode: 0o600}}}
	ui := termio.NewTestMock(t)
	if err := Apply(plan, &ui); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("file write error = %v", err)
	}
	ui = termio.NewTestMock(t)
	ui.InputFn = func(string, string) (string, error) { return " ", nil }
	plan = &Plan{Secrets: map[string]Secret{"TOKEN": {Value: "secret"}}}
	if err := Apply(plan, &ui); err == nil || !strings.Contains(err.Error(), "non-empty") {
		t.Fatalf("invalid host error = %v", err)
	}
	ui = termio.NewTestMock(t)
	ui.InputFn = func(string, string) (string, error) { return "example.test", nil }
	secretDir := filepath.Join(temp, "secret-dir")
	if err := os.MkdirAll(secretDir, 0o700); err != nil {
		t.Fatal(err)
	}
	plan = &Plan{
		SecretPath: filepath.Join(secretDir, "nested", "secrets.yaml"),
		Secrets:    map[string]Secret{"TOKEN": {Value: "secret"}},
	}
	// The nested parent is created by atomicWrite, so a valid plan should apply.
	if err := Apply(plan, &ui); err != nil {
		t.Fatalf("secret write: %v", err)
	}
}

func TestBuildRejectsUnsupportedAndUnreadableSources(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	if _, err := Build(unsupportedMigrationAgent{}, t.TempDir()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported error = %v", err)
	}
	a, _ := agent.Lookup("opencode")
	hostHome := t.TempDir()
	configDir := filepath.Join(hostHome, ".config", "opencode")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "opencode.json"), []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(configDir, "opencode.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(configDir, "opencode.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(a, hostHome); err == nil {
		t.Fatal("expected source inspection error")
	}
}

func TestApplyWriteFailureBranches(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "parent")
	if err := os.WriteFile(parent, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	ui := termio.NewTestMock(t)
	secretPlan := &Plan{
		SecretPath: filepath.Join(parent, "secrets.yaml"),
		Secrets:    map[string]Secret{"TOKEN": {Value: "value", Hosts: []string{"example.test"}}},
	}
	if err := Apply(secretPlan, &ui); err == nil || !strings.Contains(err.Error(), "read secret file") {
		t.Fatalf("secret read error = %v", err)
	}
	configPlan := &Plan{
		ConfigPath: filepath.Join(parent, "config.yaml"),
		ConfigData: []byte("x: y\n"),
	}
	if err := Apply(configPlan, &ui); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("config write error = %v", err)
	}
}

func TestGuidePromptAndApplyErrors(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	authPath := filepath.Join(hostHome, ".local", "share", "opencode", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, authPath, `{"custom":{"type":"api","key":"secret"}}`)
	a, _ := agent.Lookup("opencode")
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return "m", nil }
	ui.InputFn = func(string, string) (string, error) { return "", errors.New("host input failed") }
	if err := Guide(a, hostHome, false, &ui); err == nil || !strings.Contains(err.Error(), "host input failed") {
		t.Fatalf("Guide apply error = %v", err)
	}
	ui = termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return "q", errors.New("selection failed") }
	if err := Guide(a, hostHome, false, &ui); err == nil || !strings.Contains(err.Error(), "selection failed") {
		t.Fatalf("Guide selection error = %v", err)
	}
}

func TestLauncherConfigErrorBranches(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	a, _ := agent.Lookup("opencode")
	spec := a.(interface {
		MigrationSpec() agent.ConfigMigrationSpec
	}).MigrationSpec()
	configPath := filepath.Join(configpaths.Get().UserConfigDir(), "config.yaml")
	testutil.WritePath(t, configPath, "home: [")
	if _, _, err := buildLauncherConfig(a, spec); err == nil {
		t.Fatal("expected malformed YAML error")
	}
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(configPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := buildLauncherConfig(a, spec); err == nil {
		t.Fatal("expected launcher config read error")
	}
}

func TestMigrationStatusAndHomeMappingBranches(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a, _ := agent.Lookup("opencode")
	if _, ok := migrationHomeMapping(map[string]any{}, "target"); ok {
		t.Fatal("empty home mapping should not be present")
	}
	if got, ok := migrationHomeMapping(
		map[string]any{"home": map[string]any{"target": map[string]any{"source": "source"}}},
		"target",
	); !ok ||
		got != "source" {
		t.Fatalf("structured home mapping = %q, %v", got, ok)
	}
	if got, ok := migrationHomeMapping(
		map[string]any{"home": map[string]any{"target": 123}},
		"target",
	); !ok ||
		got != "" {
		t.Fatalf("invalid home mapping = %q, %v", got, ok)
	}
	path := filepath.Join(configpaths.Get().UserStateDir(), a.Name(), statusFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, path, "not: [valid")
	if handled, dismissed := migrationStatusFor(a, "x"); handled || dismissed {
		t.Fatalf("malformed migration status = %v, %v", handled, dismissed)
	}
	testutil.WritePath(t, path, "source_hash: other\nhandled: true\ndismissed: true\n")
	if handled, dismissed := migrationStatusFor(a, "x"); handled || dismissed {
		t.Fatalf("mismatched migration status = %v, %v", handled, dismissed)
	}
}

func TestMigrationPersistenceStatusBranches(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a, _ := agent.Lookup("opencode")
	withMigrationSeams(t)
	writeErr := errors.New("status write failed")
	atomicWriteFn = func(string, []byte, os.FileMode) error { return writeErr }
	if err := migrationWriteStatus(a.Name(), "hash", false); !errors.Is(err, writeErr) {
		t.Fatalf("status write error = %v", err)
	}
	// Exercise the status reader's malformed, mismatched, and valid branches.
	path := filepath.Join(configpaths.Get().UserStateDir(), a.Name(), statusFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, path, "source_hash: hash\nhandled: true\ndismissed: false\n")
	if handled, dismissed := migrationStatusFor(a, "hash"); !handled || dismissed {
		t.Fatalf("valid status = %v, %v", handled, dismissed)
	}
}

func TestBuildAlreadyConfiguredWithoutAuthFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	writeNativeAuth(t, hostHome)
	a, _ := agent.Lookup("opencode")
	plan, err := Build(a, hostHome)
	if err != nil {
		t.Fatal(err)
	}
	ui := termio.NewTestMock(t)
	if err := Apply(plan, &ui); err != nil {
		t.Fatal(err)
	}
	updated, err := Build(a, hostHome)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.AlreadyConfigured {
		t.Fatalf("migration should be recognized as applied: %+v", updated)
	}
}

func TestHostAndEndpointFallbackBranches(t *testing.T) {
	if got := authHosts(
		"custom",
		map[string]json.RawMessage{},
		map[string]string{"custom": "known.test"},
		nil,
	); !slicesEqual(
		got,
		[]string{"known.test"},
	) {
		t.Errorf("known auth host = %v", got)
	}
	if got := authHosts(
		"custom",
		map[string]json.RawMessage{},
		nil,
		map[string]string{"custom": "provider.test"},
	); !slicesEqual(
		got,
		[]string{"provider.test"},
	) {
		t.Errorf("provider auth host = %v", got)
	}
	if err := rejectEmbeddedEndpointCredential("http://[bad"); err == nil {
		t.Fatal("expected invalid endpoint error")
	}
	if err := rejectEmbeddedEndpointCredential("https://example.test?token=value"); err == nil {
		t.Fatal("expected credential query error")
	}
	if got := shortHash("secret"); len(got) != 8 {
		t.Errorf("short hash = %q", got)
	}
}

func TestMigrationHelperFallbacks(t *testing.T) {
	if err := mergePlannedSecrets(
		map[string]Secret{"TOKEN": {Value: "one"}},
		map[string]Secret{"TOKEN": {Value: "two"}},
	); err == nil {
		t.Fatal("expected planned secret collision")
	}
	if got := configSecretHosts("unknown", map[string]any{}, nil, nil); got != nil {
		t.Errorf("unknown config host = %v", got)
	}
	if got := providerEndpointHosts(map[string]any{"provider": map[string]any{"bad": "value"}}); len(got) != 0 {
		t.Errorf("invalid provider config hosts = %v", got)
	}
	if got := providerEndpointHosts(
		map[string]any{"provider": map[string]any{"bad": map[string]any{"options": "value"}}},
	); len(
		got,
	) != 0 {
		t.Errorf("invalid provider options hosts = %v", got)
	}
	if got := providerEndpointHosts(
		map[string]any{"provider": map[string]any{"bad": map[string]any{"options": map[string]any{"baseURL": 1}}}},
	); len(
		got,
	) != 0 {
		t.Errorf("invalid endpoint value hosts = %v", got)
	}
	if got, ok := migrationHomeMapping(
		map[string]any{"home": map[string]any{"target": []any{}}},
		"target",
	); !ok ||
		got != "" {
		t.Errorf("unsupported home mapping = %q, %v", got, ok)
	}
	if got := providerEndpointHostsWithSpec(
		map[string]any{
			"provider": map[string]any{
				"custom": map[string]any{"options": map[string]any{"endpoint": "https://custom.test"}},
			},
		},
		[]string{"endpoint"},
	); got["custom"] != "custom.test" {
		t.Fatalf("custom endpoint hosts = %v", got)
	}
	if !isEndpointField("endpoint", []string{"endpoint"}) || isEndpointField("endpoint", []string{"url"}) {
		t.Fatal("endpoint field detection is incorrect")
	}
	if !isSensitiveConfigKey("api-key") || isSensitiveConfigKey("description") {
		t.Fatal("sensitive config helper is incorrect")
	}
	if !isPlaceholder("$MSB_KEY") || isPlaceholder("literal") {
		t.Fatal("placeholder helper is incorrect")
	}
	if isEndpointKey("not-an-endpoint") || !isEndpointKey("endpoint") {
		t.Fatal("endpoint compatibility helper is incorrect")
	}
	if authFields("unknown") != nil || len(authFields("api")) != 1 {
		t.Fatal("auth field fallback helper is incorrect")
	}
	if len(authFields("oauth")) != 2 || len(authFields(authTypeWellKnown)) != 1 {
		t.Fatal("auth field type helper is incomplete")
	}
	if !isSafeField("type", []string{"type"}) || isSafeField("other", []string{"type"}) {
		t.Fatal("safe field helper is incorrect")
	}
	if got := isSafeField("é", []string{"type"}); got {
		t.Fatal("non-ASCII unknown field should not be safe")
	}
	if isSafeField("!", []string{"type"}) {
		t.Fatal("punctuation field should not be safe")
	}
	if !isPlaceholderWithSpec("$MSB_KEY", defaultMigrationSpec()) || defaultMigrationSpec().Auth.SecretPrefix == "" {
		t.Fatal("default migration spec fallback is incorrect")
	}
}

func TestDefaultMigrationSpecFallback(t *testing.T) {
	original := migrationSpecProviderFn
	t.Cleanup(func() { migrationSpecProviderFn = original })
	migrationSpecProviderFn = func() (agent.ConfigMigrationSpec, bool) {
		return agent.ConfigMigrationSpec{}, false
	}
	spec := defaultMigrationSpec()
	if spec.Auth.SecretPrefix != "OPENCODE" {
		t.Fatalf("fallback spec = %+v", spec)
	}

	spec = agent.ConfigMigrationSpec{Auth: agent.AuthMigrationSpec{
		SecretPrefix:            "TEST",
		AuthPlaceholderPrefix:   "$TEST_",
		ConfigPlaceholderPrefix: "{test:",
	}}
	if !isPlaceholderWithSpec("$TEST_KEY", spec) || !isPlaceholderWithSpec("{test:KEY}", spec) {
		t.Fatal("placeholder spec helper did not recognize configured prefixes")
	}
	if _, ok := lookupMigrationSpec(func(string) (agent.Agent, bool) { return nil, false }); ok {
		t.Fatal("missing agent should not provide a migration spec")
	}
	if _, ok := lookupMigrationSpec(func(string) (agent.Agent, bool) { return unsupportedMigrationAgent{}, true }); ok {
		t.Fatal("agent without migration capability should not provide a migration spec")
	}
}

func TestMigrationAlreadyConfiguredMalformedStateBranches(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	a, _ := agent.Lookup("opencode")
	spec := a.(interface {
		MigrationSpec() agent.ConfigMigrationSpec
	}).MigrationSpec()
	if migrationAlreadyConfigured(a, spec, "/missing/auth", "/missing/config", "/missing/launcher", true, false, nil) {
		t.Fatal("missing migration should not be configured")
	}
	configPath := filepath.Join(configpaths.Get().UserConfigDir(), "config.yaml")
	testutil.WritePath(t, configPath, "home: [")
	if migrationAlreadyConfigured(a, spec, "/missing/auth", "/missing/config", configPath, true, false, nil) {
		t.Fatal("malformed launcher config should not be configured")
	}
}

func TestBuildLauncherConfigExistingAndMalformedFormats(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	a, _ := agent.Lookup("opencode")
	spec := a.(interface {
		MigrationSpec() agent.ConfigMigrationSpec
	}).MigrationSpec()
	testutil.WriteFile(
		t,
		configpaths.Get().UserConfigDir(),
		"config.json",
		`{home:{".local/share/opencode/auth.json":"opencode/auth.json"}}`,
	)
	if path, data, err := buildLauncherConfig(a, spec); err != nil || data != nil || filepath.Ext(path) != ".json" {
		t.Fatalf("existing mapping = %q, %s, %v", path, data, err)
	}
	testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "config.json", "{")
	if _, _, err := buildLauncherConfig(a, spec); err == nil {
		t.Fatal("expected malformed JSON launcher config")
	}
	testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "config.json", `{}`)
	if _, data, err := buildLauncherConfig(a, spec); err != nil || !bytes.Contains(data, []byte("auth.json")) {
		t.Fatalf("empty JSON config = %s, %v", data, err)
	}
}

func TestBuildLauncherConfigEmptyYAML(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	a, _ := agent.Lookup("opencode")
	spec := a.(interface {
		MigrationSpec() agent.ConfigMigrationSpec
	}).MigrationSpec()
	testutil.WritePath(t, filepath.Join(configpaths.Get().UserConfigDir(), "config.yaml"), "")
	if _, data, err := buildLauncherConfig(a, spec); err != nil || !bytes.Contains(data, []byte("auth.json")) {
		t.Fatalf("empty YAML launcher config = %s, %v", data, err)
	}
}

func TestMigrationMarshalErrors(t *testing.T) {
	withMigrationSeams(t)
	marshalErr := errors.New("marshal failed")
	marshalJSONFn = func(any) ([]byte, error) { return nil, marshalErr }
	if _, _, _, err := sanitizeAuth(
		[]byte(`{"provider":{"type":"api","key":"secret"}}`),
		nil,
		nil,
	); !errors.Is(
		err,
		marshalErr,
	) {
		t.Fatalf("auth entry marshal error = %v", err)
	}
	if err := sanitizeAuthMap(
		map[string]json.RawMessage{"metadata": json.RawMessage(`{"clientSecret":"secret"}`)},
		"provider",
		"",
		nil,
		nil,
		map[string]Secret{},
	); !errors.Is(err, marshalErr) {
		t.Fatalf("nested auth marshal error = %v", err)
	}
	if err := sanitizeAuthMap(
		map[string]json.RawMessage{"metadata": json.RawMessage(`{"ignored":"value"}`)},
		"provider",
		"",
		nil,
		nil,
		map[string]Secret{},
	); !errors.Is(err, marshalErr) {
		t.Fatalf("nested object marshal error = %v", err)
	}
	if err := sanitizeAuthMap(
		map[string]json.RawMessage{"clientSecret": json.RawMessage(`"secret"`)},
		"provider",
		"",
		nil,
		nil,
		map[string]Secret{},
	); !errors.Is(err, marshalErr) {
		t.Fatalf("auth placeholder marshal error = %v", err)
	}

	marshalJSONFn = json.Marshal
	marshalJSONIndentFn = func(any, string, string) ([]byte, error) { return nil, marshalErr }
	if _, _, _, err := sanitizeAuth(
		[]byte(`{"provider":{"type":"api","key":"secret"}}`),
		nil,
		nil,
	); !errors.Is(
		err,
		marshalErr,
	) {
		t.Fatalf("migrated auth marshal error = %v", err)
	}

	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	a, _ := agent.Lookup("opencode")
	spec := a.(interface {
		MigrationSpec() agent.ConfigMigrationSpec
	}).MigrationSpec()
	hostHome := t.TempDir()
	configDir := filepath.Join(hostHome, ".config", "opencode")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, configDir, "opencode.json", `{"model":"openai/gpt-5"}`)
	if _, _, _, _, err := migrateNativeConfig(hostHome, spec); !errors.Is(err, marshalErr) {
		t.Fatalf("migrated config marshal error = %v", err)
	}

	marshalJSONIndentFn = json.MarshalIndent
	marshalYAMLFn = func(any) ([]byte, error) { return nil, marshalErr }
	testutil.WritePath(t, filepath.Join(configpaths.Get().UserConfigDir(), "config.yaml"), "")
	if _, _, err := buildLauncherConfig(a, spec); !errors.Is(err, marshalErr) {
		t.Fatalf("launcher config marshal error = %v", err)
	}
}

func TestBuildSeamErrors(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	withMigrationSeams(t)
	a, _ := agent.Lookup("opencode")
	hostHome := t.TempDir()
	writeNativeAuth(t, hostHome)
	configErr := errors.New("config planning failed")
	migrateNativeConfigFn = func(string, agent.ConfigMigrationSpec) ([]byte, map[string]Secret, []string, map[string]string, error) {
		return nil, nil, nil, nil, configErr
	}
	if _, err := Build(a, hostHome); !errors.Is(err, configErr) {
		t.Fatalf("config planning error = %v", err)
	}
	migrateNativeConfigFn = migrateNativeConfig
	authErr := errors.New("auth transform failed")
	sanitizeAuthWithSpecFn = func([]byte, agent.ConfigMigrationSpec, map[string]string, map[string]string) ([]byte, map[string]Secret, []string, error) {
		return nil, nil, nil, authErr
	}
	if _, err := Build(a, hostHome); !errors.Is(err, authErr) {
		t.Fatalf("auth transform error = %v", err)
	}
	sanitizeAuthWithSpecFn = sanitizeAuthWithSpec
	launcherErr := errors.New("launcher config failed")
	buildLauncherConfigFn = func(agent.Agent, agent.ConfigMigrationSpec) (string, []byte, error) {
		return "", nil, launcherErr
	}
	if _, err := Build(a, hostHome); !errors.Is(err, launcherErr) {
		t.Fatalf("launcher config error = %v", err)
	}
}

func TestApplyAndGuideSeamErrors(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	withMigrationSeams(t)
	a, _ := agent.Lookup("opencode")
	hostHome := t.TempDir()
	writeNativeAuth(t, hostHome)
	writeErr := errors.New("atomic write failed")
	atomicWriteFn = func(string, []byte, os.FileMode) error { return writeErr }
	ui := termio.NewTestMock(t)
	plan := &Plan{Files: []File{{Path: filepath.Join(t.TempDir(), "config"), Data: []byte("x")}}}
	if err := Apply(plan, &ui); !errors.Is(err, writeErr) {
		t.Fatalf("file write error = %v", err)
	}
	atomicWriteFn = migrationAtomicWrite
	enableErr := errors.New("enable failed")
	enableNativeProvisioningFn = func() error { return enableErr }
	ui.IsInteractiveResult = true
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return "p", nil }
	if err := Guide(a, hostHome, false, &ui); !errors.Is(err, enableErr) {
		t.Fatalf("enable error = %v", err)
	}
	dismissErr := errors.New("dismiss failed")
	dismissMigrationFn = func(*Plan) error { return dismissErr }
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return "d", nil }
	enableNativeProvisioningFn = EnableNativeProvisioning
	if err := Guide(a, hostHome, false, &ui); !errors.Is(err, dismissErr) {
		t.Fatalf("dismiss error = %v", err)
	}
}

func TestApplySecretAndConfigWriteFailures(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMigrationSeams(t)
	writeErr := errors.New("secret write failed")
	atomicWriteFn = func(path string, _ []byte, _ os.FileMode) error {
		if strings.Contains(path, "secrets-output") {
			return writeErr
		}
		return nil
	}
	ui := termio.NewTestMock(t)
	plan := &Plan{
		SecretPath: filepath.Join(t.TempDir(), "secrets-output.yaml"),
		Secrets:    map[string]Secret{"TOKEN": {Value: "x", Hosts: []string{"example.test"}}},
	}
	if err := Apply(plan, &ui); !errors.Is(err, writeErr) {
		t.Fatalf("secret write error = %v", err)
	}
	configErr := errors.New("config write failed")
	atomicWriteFn = func(path string, _ []byte, _ os.FileMode) error {
		if strings.HasSuffix(path, "config.yaml") {
			return configErr
		}
		return nil
	}
	plan = &Plan{ConfigPath: filepath.Join(t.TempDir(), "config.yaml"), ConfigData: []byte("x: y\n")}
	if err := Apply(plan, &ui); !errors.Is(err, configErr) {
		t.Fatalf("config write error = %v", err)
	}
}

func TestApplyWritesRetryableManifestAndSkipsIdenticalOutputs(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMigrationSeams(t)
	statusDir := filepath.Join(configpaths.Get().UserStateDir(), "opencode")
	if err := os.MkdirAll(statusDir, 0o700); err != nil {
		t.Fatal(err)
	}
	plan := &Plan{
		AgentName:  "opencode",
		SourceHash: "hash",
		SecretPath: filepath.Join(t.TempDir(), "secret.yaml"),
		Files: []File{
			{Path: filepath.Join(t.TempDir(), "auth.json"), Data: []byte(`{"key":"$MSB_KEY"}`), Mode: 0o600},
		},
		Secrets: map[string]Secret{"KEY": {Value: "secret", Hosts: []string{"api.example.test"}}},
	}
	ui := termio.NewTestMock(t)
	if err := Apply(plan, &ui); err != nil {
		t.Fatal(err)
	}
	manifest := migrationManifestFor(migrationStatusAgent{name: "opencode"})
	if manifest.State != migrationStateCompleted || manifest.SourceHash != "hash" {
		t.Fatalf("manifest = %+v", manifest)
	}
	if err := Apply(plan, &ui); err != nil {
		t.Fatalf("retry apply: %v", err)
	}
}

func TestReviewRejectsUnsupportedFieldsNonInteractive(t *testing.T) {
	plan := &Plan{ReviewWarnings: []string{"review required: auth field openai.custom"}}
	ui := termio.NewTestMock(t)
	if err := Review(plan, &ui); err == nil || !strings.Contains(err.Error(), "interactive review") {
		t.Fatalf("review error = %v", err)
	}
}

func TestReviewAddsInteractiveSecretHostToVMNetworkAllowlist(t *testing.T) {
	plan := &Plan{
		ConfigPath: "config.yaml",
		ConfigData: []byte("network:\n  egress-allow:\n    - api.githubcopilot.com\n"),
		SecretPath: "/tmp/env.secret.yaml",
		Secrets: map[string]Secret{
			"COPILOT": {Value: "copilot", Hosts: []string{"api.githubcopilot.com"}},
			"KEY":     {Value: "secret"},
		},
		NetworkAllowHosts: []string{"api.githubcopilot.com"},
	}
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	ui.InputFn = func(string, string) (string, error) { return "litellm.inoio.de", nil }
	ui.SelectFn = func(prompt string, _ []termio.Choice, _ string) (string, error) {
		if strings.Contains(prompt, "VM egress") {
			return "y", nil
		}
		return "y", nil
	}
	if err := Review(plan, &ui); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(plan.ConfigData, []byte("litellm.inoio.de")) {
		t.Fatalf("updated launcher config omitted interactive host: %s", plan.ConfigData)
	}
	if !slicesEqual(plan.NetworkAllowHosts, []string{"api.githubcopilot.com", "litellm.inoio.de"}) {
		t.Fatalf("new network hosts = %v", plan.NetworkAllowHosts)
	}
	if !strings.Contains(strings.Join(ui.OutCalls, "\n"), "api.githubcopilot.com, litellm.inoio.de") {
		t.Fatalf("all VM egress hosts were not reported: %v", ui.OutCalls)
	}
}

func TestReviewPrintsFinalConfigAfterVMNetworkConfirmation(t *testing.T) {
	plan := &Plan{
		ConfigPath: "config.yaml",
		ConfigData: []byte("network:\n  egress-allow:\n    - existing.test\n"),
		SecretPath: "/tmp/env.secret.yaml",
		Secrets:    map[string]Secret{"KEY": {Value: "secret", Hosts: []string{"new.example.test"}}},
	}
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	var sequence []string
	ui.SelectFn = func(prompt string, _ []termio.Choice, _ string) (string, error) {
		sequence = append(sequence, prompt)
		return "y", nil
	}
	if err := Review(plan, &ui); err != nil {
		t.Fatal(err)
	}
	if len(sequence) != 0 {
		t.Fatalf("confirmation sequence = %v", sequence)
	}
	if len(ui.OutCalls) == 0 || !strings.Contains(strings.Join(ui.OutCalls, "\n"), "new.example.test") {
		t.Fatalf("final report = %v", ui.OutCalls)
	}
	if strings.Count(strings.Join(ui.OutCalls, "\n"), "Generated migration files") != 1 {
		t.Fatalf("generated file report printed more than once: %v", ui.OutCalls)
	}
}

func TestGuideDefersStartAfterMigrationReview(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	writeNativeAuth(t, hostHome)
	a, _ := agent.Lookup("opencode")
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	choices := []string{"m", "n"}
	var startPrompt string
	var startChoices []termio.Choice
	ui.SelectFn = func(prompt string, available []termio.Choice, _ string) (string, error) {
		choice := choices[0]
		choices = choices[1:]
		if len(choices) == 0 {
			startPrompt = prompt
			startChoices = available
		}
		return choice, nil
	}
	if err := Guide(a, hostHome, false, &ui); !errors.Is(err, ErrStartDeferred) {
		t.Fatalf("Guide error = %v, want start deferred", err)
	}
	if !strings.Contains(startPrompt, "already written") || len(startChoices) != 2 || startChoices[1].Key != "n" {
		t.Fatalf("start prompt = %q, choices = %+v", startPrompt, startChoices)
	}
}

func TestGuideOffersSetupForFreshClaudeCodeUser(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	a, _ := agent.Lookup("claude-code")
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	choices := []string{guideSetupChoice, claudeAuthLoginChoice, "y"}
	var setupChoices []termio.Choice
	ui.SelectFn = func(_ string, available []termio.Choice, _ string) (string, error) {
		if setupChoices == nil {
			setupChoices = available
		}
		choice := choices[0]
		choices = choices[1:]
		return choice, nil
	}
	if err := Guide(a, t.TempDir(), false, &ui); err != nil {
		t.Fatalf("Guide: %v", err)
	}
	if len(choices) != 0 {
		t.Fatalf("Guide skipped prompts, remaining choices = %v", choices)
	}
	for _, choice := range setupChoices {
		if choice.Key == "p" {
			t.Fatalf("setup offered native host config without native config: %+v", setupChoices)
		}
	}
	launcherConfig, err := os.ReadFile(filepath.Join(configpaths.Get().UserConfigDir(), "config.yaml"))
	if err != nil || !bytes.Contains(launcherConfig, []byte("platform.claude.com")) {
		t.Fatalf("launcher config = %s, %v, want Claude login egress", launcherConfig, err)
	}
}

func TestGuideRemembersDismissedClaudeCodeSetup(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	hostHome := t.TempDir()
	a, _ := agent.Lookup("claude-code")
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	selectCalls := 0
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		selectCalls++
		return "d", nil
	}
	if err := Guide(a, hostHome, false, &ui); err != nil {
		t.Fatalf("first Guide: %v", err)
	}
	if err := Guide(a, hostHome, false, &ui); err != nil {
		t.Fatalf("second Guide: %v", err)
	}
	if selectCalls != 1 {
		t.Fatalf("Select calls = %d, want the dismissed setup not to be offered again", selectCalls)
	}
}

func writeNativeAuth(t *testing.T, hostHome string) {
	t.Helper()
	path := filepath.Join(hostHome, ".local", "share", "opencode", "auth.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, path, `{"openrouter":{"type":"api","key":"secret-key"}}`)
}

func clearOpenCodeXDG(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
}

func withMigrationSeams(t *testing.T) {
	t.Helper()
	originalNative := migrateNativeConfigFn
	originalAuth := sanitizeAuthFn
	originalAuthWithSpec := sanitizeAuthWithSpecFn
	originalLauncher := buildLauncherConfigFn
	originalWrite := atomicWriteFn
	originalEnable := enableNativeProvisioningFn
	originalDismiss := dismissMigrationFn
	originalJSON := marshalJSONFn
	originalJSONIndent := marshalJSONIndentFn
	originalYAML := marshalYAMLFn
	t.Cleanup(func() {
		migrateNativeConfigFn = originalNative
		sanitizeAuthFn = originalAuth
		sanitizeAuthWithSpecFn = originalAuthWithSpec
		buildLauncherConfigFn = originalLauncher
		atomicWriteFn = originalWrite
		enableNativeProvisioningFn = originalEnable
		dismissMigrationFn = originalDismiss
		marshalJSONFn = originalJSON
		marshalJSONIndentFn = originalJSONIndent
		marshalYAMLFn = originalYAML
	})
}

func (p *Plan) fileAt(path string) (File, bool) {
	for _, file := range p.Files {
		if file.Path == path {
			return file, true
		}
	}
	return File{}, false
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
