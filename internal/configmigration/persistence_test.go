package configmigration

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configfile"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
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

func TestBuildReportsProviderHostDenyConflict(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	denyAllEgressInLauncherConfig(t)
	hostHome := t.TempDir()
	writeNativeAuth(t, hostHome)
	a, _ := agent.Lookup("opencode")
	plan, err := Build(a, hostHome, testNetworkPolicy())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !slices.Equal(plan.NetworkConflicts, []string{"openrouter.ai"}) {
		t.Errorf("NetworkConflicts = %v, want [openrouter.ai]", plan.NetworkConflicts)
	}
	if len(plan.NetworkAllowHosts) != 0 {
		t.Errorf("NetworkAllowHosts = %v, want the denied host omitted", plan.NetworkAllowHosts)
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
	sanitizeAuthWithSpecFn = func([]byte, agent.ConfigMigrationSpec, map[string]string, map[string]string) ([]byte, map[string]Secret, []string, []LoginProvider, error) {
		return []byte("{}\n"), map[string]Secret{"OPENCODE_SHARED": {Value: "from-auth"}}, nil, nil, nil
	}
	a, _ := agent.Lookup("opencode")
	if _, err := Build(a, hostHome, testNetworkPolicy()); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Errorf("Build error = %v, want a secret name collision", err)
	}
}

func TestBuildStoresEffectiveNetworkPolicy(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}
	policy := network.Policy{
		Profiles:   []network.Profile{network.ProfilePrivate},
		EgressDeny: []string{"example.com"},
	}
	plan, err := Build(a, t.TempDir(), policy)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !slices.Equal(plan.policy.Profiles, policy.Profiles) ||
		!slices.Equal(plan.policy.EgressDeny, policy.EgressDeny) {
		t.Errorf("plan.policy = %+v, want %+v", plan.policy, policy)
	}
}

func TestBuildReportsNetworkDenyConflicts(t *testing.T) {
	tests := map[string]network.Policy{
		"deny all":       {Profiles: []network.Profile{network.ProfilePrivate}, EgressDeny: []string{"*"}},
		"deny by suffix": {Profiles: []network.Profile{network.ProfilePrivate}, EgressDeny: []string{".openrouter.ai"}},
	}
	for name, policy := range tests {
		t.Run(name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			clearOpenCodeXDG(t)
			hostHome := t.TempDir()
			writeNativeAuth(t, hostHome)
			a, _ := agent.Lookup("opencode")
			plan, err := Build(a, hostHome, policy)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if !slices.Equal(plan.NetworkConflicts, []string{"openrouter.ai"}) {
				t.Errorf("NetworkConflicts = %v, want [openrouter.ai]", plan.NetworkConflicts)
			}
			if len(plan.NetworkAllowHosts) != 0 {
				t.Errorf("NetworkAllowHosts = %v, want no shadowed host", plan.NetworkAllowHosts)
			}
			if strings.Contains(string(plan.ConfigData), "openrouter.ai") {
				t.Errorf("launcher config allows a denied host:\n%s", plan.ConfigData)
			}
		})
	}
}

func TestBuildReportsLoginForOAuthOnlyConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	authPath := filepath.Join(hostHome, ".local", "share", "opencode", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, authPath, `{"github-copilot":{"type":"oauth","access":"a","refresh":"r"}}`)
	a, _ := agent.Lookup("opencode")
	plan, err := Build(a, hostHome, testNetworkPolicy())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if plan.HasChanges() {
		t.Errorf("HasChanges = true for an OAuth-only config, want false")
	}
	if !plan.HasLogin() {
		t.Errorf("HasLogin = false, want true: %+v", plan.LoginProviders)
	}
	if !plan.NeedsAttention() {
		t.Errorf("NeedsAttention = false, want true")
	}
	if got := plan.LoginProviders; !slicesEqual(got, []string{"github-copilot"}) {
		t.Errorf("LoginProviders = %v, want [github-copilot]", got)
	}
	if got := plan.LoginHosts; !slicesEqual(got, []string{"api.githubcopilot.com", "github.com"}) {
		t.Errorf("LoginHosts = %v, want [api.githubcopilot.com github.com]", got)
	}
	for _, host := range plan.LoginHosts {
		if !slices.Contains(plan.NetworkHosts, host) {
			t.Errorf("NetworkHosts = %v, want login host %q allowed", plan.NetworkHosts, host)
		}
	}
}

func TestApplyRecordsLoginPendingForMixedConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")
	writeNativeAuthContent(t, hostHome, `{
  "openai": {"type": "api", "key": "sk-static"},
  "github-copilot": {"type": "oauth", "access": "a", "refresh": "r"}
}`)

	plan, err := Build(a, hostHome, testNetworkPolicy())
	if err != nil {
		t.Fatal(err)
	}
	ui := termio.NewTestMock(t)
	if err := Apply(plan, &ui); err != nil {
		t.Fatal(err)
	}

	manifest := migrationManifestFor(a)
	if manifest.State != migrationStateLoginPending {
		t.Fatalf("state = %q, want %q", manifest.State, migrationStateLoginPending)
	}
	if len(manifest.Pending) != 1 || manifest.Pending[0].Provider != "github-copilot" {
		t.Fatalf("pending = %+v, want github-copilot", manifest.Pending)
	}
}

func TestRecordLoginPendingWritesProviders(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a, _ := agent.Lookup("opencode")
	plan := &Plan{
		AgentName:     a.Name(),
		SourceHash:    "oauth-only-hash",
		PendingLogins: []LoginProvider{{Name: "github-copilot", Hosts: []string{"github.com"}}},
	}
	if err := RecordLoginPending(plan); err != nil {
		t.Fatal(err)
	}
	manifest := migrationManifestFor(a)
	if manifest.State != migrationStateLoginPending {
		t.Fatalf("state = %q, want %q", manifest.State, migrationStateLoginPending)
	}
	if len(manifest.Pending) != 1 || manifest.Pending[0].Provider != "github-copilot" {
		t.Fatalf("pending = %+v, want github-copilot", manifest.Pending)
	}
}

func TestGuideLoginPendingVerificationYesCompletes(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")
	writeNativeAuthContent(t, hostHome, `{"github-copilot": {"type": "oauth", "access": "a", "refresh": "r"}}`)

	handoff := termio.NewTestMock(t)
	handoff.IsInteractiveResult = true
	handoff.SelectFn = func(string, []termio.Choice, string) (string, error) {
		return guideLoginChoice, nil
	}
	if err := Guide(a, hostHome, testNetworkPolicy(), false, &handoff); err != nil {
		t.Fatalf("first Guide: %v", err)
	}
	if migrationManifestFor(a).State != migrationStateLoginPending {
		t.Fatalf("after handoff state = %q", migrationManifestFor(a).State)
	}

	verification := termio.NewTestMock(t)
	verification.IsInteractiveResult = true
	verification.SelectFn = func(string, []termio.Choice, string) (string, error) { return "yes", nil }
	if err := Guide(a, hostHome, testNetworkPolicy(), false, &verification); err != nil {
		t.Fatalf("verify Guide: %v", err)
	}
	if got := migrationManifestFor(a).State; got != migrationStateCompleted {
		t.Fatalf("state after confirmation = %q, want completed", got)
	}
}

// loginPendingGuide runs the first interactive Guide over an OAuth-only native
// config and selects the in-sandbox login, recording the login-pending state.
func loginPendingGuide(t *testing.T, a agent.Agent, hostHome string) {
	t.Helper()
	handoff := termio.NewTestMock(t)
	handoff.IsInteractiveResult = true
	handoff.SelectFn = func(string, []termio.Choice, string) (string, error) {
		return guideLoginChoice, nil
	}
	if err := Guide(a, hostHome, testNetworkPolicy(), false, &handoff); err != nil {
		t.Fatalf("login handoff Guide: %v", err)
	}
}

func sequencedChoices(choices ...string) func(string, []termio.Choice, string) (string, error) {
	return func(string, []termio.Choice, string) (string, error) {
		choice := choices[0]
		choices = choices[1:]
		return choice, nil
	}
}

func TestGuideLoginPendingVerificationLaterStaysQuiet(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")
	writeNativeAuthContent(t, hostHome, `{"github-copilot": {"type": "oauth", "access": "a", "refresh": "r"}}`)
	loginPendingGuide(t, a, hostHome)

	manifestPath := migrationManifestPath(a.Name())
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}

	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	prompts := 0
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		prompts++
		return "later", nil
	}
	if err := Guide(a, hostHome, testNetworkPolicy(), false, &ui); err != nil {
		t.Fatalf("later Guide: %v", err)
	}
	if prompts != 1 {
		t.Errorf("later run prompted %d times, want exactly the verification question", prompts)
	}
	if got := migrationManifestFor(a).State; got != migrationStateLoginPending {
		t.Errorf("state = %q, want login-pending", got)
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("later run rewrote the manifest: %s", after)
	}
}

func TestGuideLoginPendingVerificationNoReentersHandoff(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")
	writeNativeAuthContent(t, hostHome, `{"github-copilot": {"type": "oauth", "access": "a", "refresh": "r"}}`)
	loginPendingGuide(t, a, hostHome)

	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	ui.SelectFn = sequencedChoices("no", guideLoginChoice)
	if err := Guide(a, hostHome, testNetworkPolicy(), false, &ui); err != nil {
		t.Fatalf("no Guide: %v", err)
	}
	if got := migrationManifestFor(a).State; got != migrationStateLoginPending {
		t.Errorf("state = %q, want login-pending after re-handoff", got)
	}
}

func TestGuideLoginPendingVerificationAfterMixedMigration(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")
	writeNativeAuthContent(t, hostHome, `{
  "openai": {"type": "api", "key": "sk-static"},
  "github-copilot": {"type": "oauth", "access": "a", "refresh": "r"}
}`)

	first := termio.NewTestMock(t)
	first.IsInteractiveResult = true
	first.SelectFn = sequencedChoices(guideMigrationChoice, "y")
	if err := Guide(a, hostHome, testNetworkPolicy(), false, &first); err != nil {
		t.Fatalf("mixed migration Guide: %v", err)
	}
	if got := migrationManifestFor(a).State; got != migrationStateLoginPending {
		t.Fatalf("state = %q, want login-pending", got)
	}

	verification := termio.NewTestMock(t)
	verification.IsInteractiveResult = true
	verification.SelectFn = func(string, []termio.Choice, string) (string, error) { return "yes", nil }
	if err := Guide(a, hostHome, testNetworkPolicy(), false, &verification); err != nil {
		t.Fatalf("verify Guide: %v", err)
	}
	if got := migrationManifestFor(a).State; got != migrationStateCompleted {
		t.Errorf("state = %q, want completed despite managed config", got)
	}
}

func TestGuideLoginPendingRearmedBySourceHashChange(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")
	writeNativeAuthContent(t, hostHome, `{"github-copilot": {"type": "oauth", "access": "a", "refresh": "r"}}`)
	loginPendingGuide(t, a, hostHome)

	writeNativeAuthContent(t, hostHome, `{"github-copilot": {"type": "oauth", "access": "b", "refresh": "s"}}`)
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	offered := recordOfferedChoices(&ui, guideLoginChoice)
	if err := Guide(a, hostHome, testNetworkPolicy(), false, &ui); err != nil {
		t.Fatalf("re-armed Guide: %v", err)
	}
	if !slices.Contains(*offered, guideLoginChoice) {
		t.Errorf("offered choices = %v, want the login hand-off after a source change", *offered)
	}
}

func TestGuideNonInteractiveWarnsPendingLoginOnce(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")
	writeNativeAuthContent(t, hostHome, `{"github-copilot": {"type": "oauth", "access": "a", "refresh": "r"}}`)
	loginPendingGuide(t, a, hostHome)

	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = false
	if err := Guide(a, hostHome, testNetworkPolicy(), false, &ui); err != nil {
		t.Fatalf("non-interactive Guide: %v", err)
	}
	if len(ui.WarnCalls) != 1 {
		t.Errorf("warnings = %v, want exactly one pending-login reminder", ui.WarnCalls)
	}
	if !strings.Contains(ui.WarnCalls[0], "still pending") {
		t.Errorf("warning = %q, want the pending-login reminder", ui.WarnCalls[0])
	}
}

func TestLoginProviderNames(t *testing.T) {
	got := loginProviderNames([]LoginProvider{{Name: "github-copilot"}, {Name: "openai"}})
	if got != "github-copilot, openai" {
		t.Errorf("loginProviderNames = %q, want %q", got, "github-copilot, openai")
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

func TestUpdateLauncherNetworkReportsParseError(t *testing.T) {
	if _, _, _, _, err := updateLauncherNetwork(
		"config.yaml",
		[]byte("- not a mapping\n"),
		[]string{"a.test"},
		nil,
	); err == nil {
		t.Error("updateLauncherNetwork succeeded for an invalid config, want an error")
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
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	if err := Review(nil, &ui); err == nil {
		t.Error("Review(nil) succeeded, want an error")
	}
}

func TestReviewSkipsSecretHostDenyConflict(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	denyAllEgressInLauncherConfig(t)
	plan := &Plan{
		AgentName:  "opencode",
		SecretPath: filepath.Join(t.TempDir(), "env.secret.yaml"),
		Secrets:    map[string]Secret{"OPENCODE_KEY": {Value: "secret", Hosts: []string{"a.test"}}},
	}
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return "skip", nil }
	if err := Review(plan, &ui); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if len(plan.NetworkConflicts) != 0 || slices.Contains(plan.NetworkAllowHosts, "a.test") {
		t.Errorf(
			"NetworkConflicts = %v, NetworkAllowHosts = %v, want the conflict skipped",
			plan.NetworkConflicts,
			plan.NetworkAllowHosts,
		)
	}
}

const networkConflictDenyConfig = "network:\n  egress-deny:\n    - a.test\n    - keep.test\n"

func newNetworkConflictPlan(t *testing.T, configData string) *Plan {
	t.Helper()
	writeUserLauncherConfig(t, "config.yaml", configData)
	return &Plan{
		AgentName:            "opencode",
		ConfigPath:           filepath.Join(configpaths.Get().UserConfigDir(), "config.yaml"),
		ConfigData:           []byte(configData),
		SecretPath:           filepath.Join(t.TempDir(), "env.secret.yaml"),
		RequiredNetworkHosts: []string{"a.test"},
	}
}

func interactiveNetworkUI(t *testing.T, choice string) termio.Mock {
	t.Helper()
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return choice, nil }
	return ui
}

func TestReviewResolveNetworkConflictsSkip(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	plan := newNetworkConflictPlan(t, networkConflictDenyConfig)
	ui := interactiveNetworkUI(t, "skip")
	if err := Review(plan, &ui); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if slices.Contains(plan.NetworkHosts, "a.test") || slices.Contains(plan.NetworkAllowHosts, "a.test") {
		t.Errorf(
			"skipped host still present: NetworkHosts = %v, NetworkAllowHosts = %v",
			plan.NetworkHosts,
			plan.NetworkAllowHosts,
		)
	}
	if len(plan.NetworkConflicts) != 0 {
		t.Errorf("NetworkConflicts = %v, want none after resolution", plan.NetworkConflicts)
	}
}

func TestReviewResolveNetworkConflictsAllowAnyway(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	plan := newNetworkConflictPlan(t, networkConflictDenyConfig)
	ui := interactiveNetworkUI(t, "allow-anyway")
	if err := Review(plan, &ui); err != nil {
		t.Fatalf("Review: %v", err)
	}
	config, err := configfile.Parse(plan.ConfigPath, plan.ConfigData)
	if err != nil {
		t.Fatalf("parse updated launcher config: %v", err)
	}
	networkValues, _ := config.Values()["network"].(map[string]any)
	deny := stringList(networkValues["egress-deny"])
	if slices.Contains(deny, "a.test") {
		t.Errorf("egress-deny = %v, want a.test removed", deny)
	}
	if !slices.Contains(deny, "keep.test") {
		t.Errorf("egress-deny = %v, want keep.test preserved", deny)
	}
	if !slices.Contains(plan.NetworkAllowHosts, "a.test") {
		t.Errorf("NetworkAllowHosts = %v, want a.test allowed", plan.NetworkAllowHosts)
	}
	if len(plan.NetworkConflicts) != 0 {
		t.Errorf("NetworkConflicts = %v, want none after resolution", plan.NetworkConflicts)
	}
}

func TestReviewResolveNetworkConflictsAllowAnywayRemovesScalarDeny(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const scalarDeny = "network:\n  egress-deny: \"a.test, keep.test\"\n"
	plan := newNetworkConflictPlan(t, scalarDeny)
	ui := interactiveNetworkUI(t, "allow-anyway")
	if err := Review(plan, &ui); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if strings.Contains(string(plan.ConfigData), "a.test, keep.test") {
		t.Errorf("scalar egress-deny was not rewritten:\n%s", plan.ConfigData)
	}
	config, err := configfile.Parse(plan.ConfigPath, plan.ConfigData)
	if err != nil {
		t.Fatalf("parse updated launcher config: %v", err)
	}
	networkValues, _ := config.Values()["network"].(map[string]any)
	deny := stringList(networkValues["egress-deny"])
	if slices.Contains(deny, "a.test") || !slices.Contains(deny, "keep.test") {
		t.Errorf("egress-deny = %v, want a.test removed and keep.test kept", deny)
	}
	if !slices.Contains(plan.NetworkAllowHosts, "a.test") || len(plan.NetworkConflicts) != 0 {
		t.Errorf(
			"NetworkAllowHosts = %v, NetworkConflicts = %v, want a.test allowed and no conflict",
			plan.NetworkAllowHosts,
			plan.NetworkConflicts,
		)
	}
}

func TestReviewResolveNetworkConflictsAllowAnywayDisclosesBroadRule(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	plan := newNetworkConflictPlan(t, "network:\n  egress-deny:\n    - \"*\"\n")
	ui := interactiveNetworkUI(t, "allow-anyway")
	if err := Review(plan, &ui); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, "\n"), "broader egress-deny rules") {
		t.Errorf("warnings = %v, want disclosure of the removed broad rule", ui.WarnCalls)
	}
}

func TestReviewResolveNetworkConflictsAllowAnywayClearsPolicyDeny(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	plan := &Plan{
		AgentName:            "opencode",
		SecretPath:           filepath.Join(t.TempDir(), "env.secret.yaml"),
		RequiredNetworkHosts: []string{"a.test"},
		policy:               network.Policy{EgressDeny: []string{"a.test"}},
	}
	ui := interactiveNetworkUI(t, "allow-anyway")
	if err := Review(plan, &ui); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !slices.Contains(plan.NetworkAllowHosts, "a.test") || len(plan.NetworkConflicts) != 0 {
		t.Errorf(
			"NetworkAllowHosts = %v, NetworkConflicts = %v, want a.test allowed",
			plan.NetworkAllowHosts,
			plan.NetworkConflicts,
		)
	}
}

func TestReviewResolveNetworkConflictsPublic(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	plan := newNetworkConflictPlan(t, networkConflictDenyConfig)
	ui := interactiveNetworkUI(t, "public")
	if err := Review(plan, &ui); err != nil {
		t.Fatalf("Review: %v", err)
	}
	config, err := configfile.Parse(plan.ConfigPath, plan.ConfigData)
	if err != nil {
		t.Fatalf("parse updated launcher config: %v", err)
	}
	networkValues, _ := config.Values()["network"].(map[string]any)
	if networkValues["profile"] != "public" {
		t.Errorf("network.profile = %v, want public", networkValues["profile"])
	}
	deny := stringList(networkValues["egress-deny"])
	if hostDeniedByAny(deny, "a.test") {
		t.Errorf("egress-deny = %v, still shadows a.test after choosing public", deny)
	}
	if !slices.Contains(deny, "keep.test") {
		t.Errorf("egress-deny = %v, want keep.test preserved", deny)
	}
	if len(plan.NetworkConflicts) != 0 {
		t.Errorf("NetworkConflicts = %v, want none after choosing public", plan.NetworkConflicts)
	}
	if !slices.Contains(plan.NetworkHosts, "a.test") || !slices.Contains(plan.NetworkAllowHosts, "a.test") {
		t.Errorf(
			"NetworkHosts = %v, NetworkAllowHosts = %v, want a.test reachable",
			plan.NetworkHosts,
			plan.NetworkAllowHosts,
		)
	}
}

func TestReviewResolveNetworkConflictsPublicClearsDenyAllRule(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	plan := newNetworkConflictPlan(t, "network:\n  egress-deny:\n    - \"*\"\n")
	ui := interactiveNetworkUI(t, "public")
	if err := Review(plan, &ui); err != nil {
		t.Fatalf("Review: %v", err)
	}
	config, err := configfile.Parse(plan.ConfigPath, plan.ConfigData)
	if err != nil {
		t.Fatalf("parse updated launcher config: %v", err)
	}
	networkValues, _ := config.Values()["network"].(map[string]any)
	deny := stringList(networkValues["egress-deny"])
	if hostDeniedByAny(deny, "a.test") {
		t.Errorf("egress-deny = %v, still shadows a.test after choosing public", deny)
	}
	if len(plan.NetworkConflicts) != 0 {
		t.Errorf("NetworkConflicts = %v, want none after choosing public", plan.NetworkConflicts)
	}
}

func TestReviewResolveNetworkConflictsAbort(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	plan := newNetworkConflictPlan(t, networkConflictDenyConfig)
	ui := interactiveNetworkUI(t, "abort")
	if err := Review(plan, &ui); err == nil || !strings.Contains(err.Error(), "a.test") {
		t.Fatalf("Review error = %v, want abort naming a.test", err)
	}
}

func TestReviewResolveNetworkConflictsNonInteractive(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	plan := newNetworkConflictPlan(t, networkConflictDenyConfig)
	ui := termio.NewTestMock(t)
	if err := Review(plan, &ui); err == nil || !strings.Contains(err.Error(), "a.test") {
		t.Fatalf("Review error = %v, want the conflicting host named", err)
	}
}

func TestEnsureNetworkConflictsResolved(t *testing.T) {
	if err := ensureNetworkConflictsResolved(&Plan{}); err != nil {
		t.Errorf("no conflicts: %v", err)
	}
	err := ensureNetworkConflictsResolved(&Plan{NetworkConflicts: []string{"a.test", "b.test"}})
	if err == nil || !strings.Contains(err.Error(), "a.test") || !strings.Contains(err.Error(), "b.test") {
		t.Errorf("error = %v, want the surviving hosts named", err)
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
}

func TestRefreshNetworkPlanRecordsDenyConflicts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	denyAllEgressInLauncherConfig(t)
	plan := &Plan{RequiredNetworkHosts: []string{"a.test"}}
	if err := refreshNetworkPlan(plan); err != nil {
		t.Fatalf("refreshNetworkPlan: %v", err)
	}
	if !slices.Equal(plan.NetworkConflicts, []string{"a.test"}) {
		t.Errorf("NetworkConflicts = %v, want [a.test]", plan.NetworkConflicts)
	}
}

func TestRefreshNetworkPlanKeepsLoginHosts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeUserLauncherConfig(t, "config.yaml", "agent: opencode\n")
	plan := &Plan{
		RequiredNetworkHosts: []string{"openrouter.ai"},
		LoginHosts:           []string{"github.com"},
	}
	if err := refreshNetworkPlan(plan); err != nil {
		t.Fatalf("refreshNetworkPlan: %v", err)
	}
	if !slices.Contains(plan.NetworkAllowHosts, "github.com") {
		t.Errorf("NetworkAllowHosts = %v, want login host github.com retained", plan.NetworkAllowHosts)
	}
	if !strings.Contains(string(plan.ConfigData), "github.com") {
		t.Errorf("written config does not allow the login host:\n%s", plan.ConfigData)
	}
}

func TestReviewDisclosesDroppedLoginProviderForMixedConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	authPath := filepath.Join(hostHome, ".local", "share", "opencode", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(
		t,
		authPath,
		`{"openrouter":{"type":"api","key":"secret-key"},"github-copilot":{"type":"oauth","access":"a","refresh":"r"}}`,
	)
	a, _ := agent.Lookup("opencode")
	plan, err := Build(a, hostHome, testNetworkPolicy())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Files) == 0 {
		t.Fatalf("mixed config produced no generated files: %+v", plan)
	}
	ui := termio.NewTestMock(t)
	if err := Review(plan, &ui); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, "\n"), "github-copilot") {
		t.Errorf("warnings = %v, want the dropped login provider named", ui.WarnCalls)
	}
	if !strings.Contains(string(plan.ConfigData), "github.com") {
		t.Errorf("written config does not allow the login host:\n%s", plan.ConfigData)
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
	if err := Guide(unsupportedMigrationAgent{}, t.TempDir(), testNetworkPolicy(), false, &ui); err != nil {
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
	if err := Guide(a, hostHome, testNetworkPolicy(), false, &ui); err == nil {
		t.Error("Guide succeeded with an unreadable native auth file, want an error")
	}
}

func recordOfferedChoices(ui *termio.Mock, choose string) *[]string {
	offered := new([]string)
	ui.SelectFn = func(_ string, choices []termio.Choice, _ string) (string, error) {
		for _, choice := range choices {
			*offered = append(*offered, choice.Key)
		}
		return choose, nil
	}
	return offered
}

func TestGuideSurfacesWarningsWhenNothingToMigrate(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	configDir := filepath.Join(hostHome, ".config", "opencode")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, configDir, "config.json", "{{{")
	a, _ := agent.Lookup("opencode")
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	offered := recordOfferedChoices(&ui, guideStartChoice)

	if err := Guide(a, hostHome, testNetworkPolicy(), false, &ui); err != nil {
		t.Fatalf("Guide: %v", err)
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, "\n"), "skipped malformed native config") {
		t.Errorf("warnings = %v, want the malformed-config warning surfaced", ui.WarnCalls)
	}
	if !slices.Contains(*offered, guideHostFirstChoice) {
		t.Errorf("offered choices = %v, want an explicit host-first hand-off", *offered)
	}
}

func TestGuideReviewHandoffSurfacesReviewWarnings(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	offered := recordOfferedChoices(&ui, guideStartChoice)
	warning := "review required: auth field custom.note is not recognized"
	plan := &Plan{
		AgentName:      "opencode",
		Warnings:       []string{warning},
		ReviewWarnings: []string{warning},
	}

	if err := guideReviewHandoff(plan, &ui); err != nil {
		t.Fatalf("guideReviewHandoff: %v", err)
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, "\n"), "review required") {
		t.Errorf("warnings = %v, want the review-required warning surfaced", ui.WarnCalls)
	}
	if !slices.Contains(*offered, guideHostFirstChoice) || !slices.Contains(*offered, guideManualChoice) {
		t.Errorf("offered choices = %v, want host-first and manual outcomes", *offered)
	}
}

func TestGuideOffersHostFirstWhenNoNativeConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	a, _ := agent.Lookup("pi")
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	offered := recordOfferedChoices(&ui, guideStartChoice)

	if err := Guide(a, t.TempDir(), testNetworkPolicy(), false, &ui); err != nil {
		t.Fatalf("Guide: %v", err)
	}
	if !slices.Contains(*offered, guideHostFirstChoice) {
		t.Errorf("offered choices = %v, want an explicit host-first hand-off", *offered)
	}
}

func TestGuideOffersLoginHandoffForOAuthOnlyConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	authDir := filepath.Join(hostHome, ".local", "share", "opencode")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(
		t,
		authDir,
		"auth.json",
		`{"github-copilot":{"type":"oauth","access":"a","refresh":"r"}}`,
	)
	a, _ := agent.Lookup("opencode")
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	offered := recordOfferedChoices(&ui, guideManualChoice)

	if err := Guide(a, hostHome, testNetworkPolicy(), false, &ui); err != nil {
		t.Fatalf("Guide: %v", err)
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, "\n"), "github-copilot") {
		t.Errorf("warnings = %v, want the login provider surfaced", ui.WarnCalls)
	}
	if !slices.Contains(*offered, guideLoginChoice) {
		t.Errorf("offered choices = %v, want an explicit login hand-off", *offered)
	}
}

func TestGuideHostFirstManualIsRemembered(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")

	first := termio.NewTestMock(t)
	first.IsInteractiveResult = true
	first.SelectFn = func(string, []termio.Choice, string) (string, error) {
		return guideManualChoice, nil
	}
	if err := Guide(a, hostHome, testNetworkPolicy(), false, &first); err != nil {
		t.Fatalf("host-first Guide: %v", err)
	}
	if got := migrationManifestFor(a).State; got != migrationStateManual {
		t.Fatalf("state = %q, want manual", got)
	}

	second := termio.NewTestMock(t)
	second.IsInteractiveResult = true
	prompts := 0
	second.SelectFn = func(string, []termio.Choice, string) (string, error) {
		prompts++
		return guideManualChoice, nil
	}
	if err := Guide(a, hostHome, testNetworkPolicy(), false, &second); err != nil {
		t.Fatalf("second Guide: %v", err)
	}
	if prompts != 0 {
		t.Fatalf("second run prompted %d times, want quiet", prompts)
	}
}

func TestGuideStartAnywayIsRemembered(t *testing.T) {
	hostFirst := func(t *testing.T) (agent.Agent, string) {
		t.Helper()
		configpaths.WithMockConfigPaths(t)
		clearOpenCodeXDG(t)
		a, _ := agent.Lookup("opencode")
		return a, t.TempDir()
	}
	review := func(t *testing.T) (agent.Agent, string) {
		t.Helper()
		configpaths.WithMockConfigPaths(t)
		clearOpenCodeXDG(t)
		hostHome := t.TempDir()
		configDir := filepath.Join(hostHome, ".config", "opencode")
		if err := os.MkdirAll(configDir, 0o700); err != nil {
			t.Fatal(err)
		}
		testutil.WriteFile(t, configDir, "config.json", "{{{")
		a, _ := agent.Lookup("opencode")
		return a, hostHome
	}
	tests := map[string]func(*testing.T) (agent.Agent, string){
		"host first":     hostFirst,
		"review handoff": review,
	}
	for name, newCase := range tests {
		t.Run(name, func(t *testing.T) {
			a, hostHome := newCase(t)

			first := termio.NewTestMock(t)
			first.IsInteractiveResult = true
			first.SelectFn = func(string, []termio.Choice, string) (string, error) {
				return guideStartChoice, nil
			}
			if err := Guide(a, hostHome, testNetworkPolicy(), false, &first); err != nil {
				t.Fatalf("start-anyway Guide: %v", err)
			}
			if got := migrationManifestFor(a).State; got != migrationStateManual {
				t.Fatalf("state = %q, want manual", got)
			}

			second := termio.NewTestMock(t)
			second.IsInteractiveResult = true
			selectCalls := 0
			second.SelectFn = func(string, []termio.Choice, string) (string, error) {
				selectCalls++
				return guideStartChoice, nil
			}
			if err := Guide(a, hostHome, testNetworkPolicy(), false, &second); err != nil {
				t.Fatalf("second Guide: %v", err)
			}
			if selectCalls != 0 {
				t.Fatalf("second run prompted %d times, want quiet", selectCalls)
			}
		})
	}
}

func TestGuideManualIsRememberedForReviewHandoff(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	configDir := filepath.Join(hostHome, ".config", "opencode")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, configDir, "config.json", "{{{")
	a, _ := agent.Lookup("opencode")

	first := termio.NewTestMock(t)
	first.IsInteractiveResult = true
	first.SelectFn = func(string, []termio.Choice, string) (string, error) {
		return guideManualChoice, nil
	}
	if err := Guide(a, hostHome, testNetworkPolicy(), false, &first); err != nil {
		t.Fatalf("review handoff Guide: %v", err)
	}
	if got := migrationManifestFor(a).State; got != migrationStateManual {
		t.Fatalf("state = %q, want manual", got)
	}

	second := termio.NewTestMock(t)
	second.IsInteractiveResult = true
	prompts := 0
	second.SelectFn = func(string, []termio.Choice, string) (string, error) {
		prompts++
		return guideStartChoice, nil
	}
	if err := Guide(a, hostHome, testNetworkPolicy(), false, &second); err != nil {
		t.Fatalf("second Guide: %v", err)
	}
	if prompts != 0 {
		t.Fatalf("second run prompted %d times, want quiet", prompts)
	}
}

func TestGuideManualIsRememberedForLoginHandoff(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")
	writeNativeAuthContent(t, hostHome, `{"github-copilot": {"type": "oauth", "access": "a", "refresh": "r"}}`)

	first := termio.NewTestMock(t)
	first.IsInteractiveResult = true
	first.SelectFn = func(string, []termio.Choice, string) (string, error) {
		return guideManualChoice, nil
	}
	if err := Guide(a, hostHome, testNetworkPolicy(), false, &first); err != nil {
		t.Fatalf("login handoff Guide: %v", err)
	}
	if got := migrationManifestFor(a).State; got != migrationStateManual {
		t.Fatalf("state = %q, want manual", got)
	}

	second := termio.NewTestMock(t)
	second.IsInteractiveResult = true
	prompts := 0
	second.SelectFn = func(string, []termio.Choice, string) (string, error) {
		prompts++
		return guideLoginChoice, nil
	}
	if err := Guide(a, hostHome, testNetworkPolicy(), false, &second); err != nil {
		t.Fatalf("second Guide: %v", err)
	}
	if prompts != 0 {
		t.Fatalf("second run prompted %d times, want quiet", prompts)
	}
}

func TestGuideManualRearmedBySourceHashChange(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")
	writeNativeAuthContent(t, hostHome, `{"github-copilot": {"type": "oauth", "access": "a", "refresh": "r"}}`)

	first := termio.NewTestMock(t)
	first.IsInteractiveResult = true
	first.SelectFn = func(string, []termio.Choice, string) (string, error) {
		return guideManualChoice, nil
	}
	if err := Guide(a, hostHome, testNetworkPolicy(), false, &first); err != nil {
		t.Fatalf("manual Guide: %v", err)
	}

	writeNativeAuthContent(t, hostHome, `{"github-copilot": {"type": "oauth", "access": "b", "refresh": "s"}}`)
	second := termio.NewTestMock(t)
	second.IsInteractiveResult = true
	offered := recordOfferedChoices(&second, guideManualChoice)
	if err := Guide(a, hostHome, testNetworkPolicy(), false, &second); err != nil {
		t.Fatalf("re-armed Guide: %v", err)
	}
	if !slices.Contains(*offered, guideLoginChoice) {
		t.Errorf("offered choices = %v, want the flow re-armed after a source change", *offered)
	}
}

func TestGuideSkipsLoginHandoffWhenManagedConfigPresent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clearOpenCodeXDG(t)
	hostHome := t.TempDir()
	authDir := filepath.Join(hostHome, ".local", "share", "opencode")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, authDir, "auth.json", `{"github-copilot":{"type":"oauth","access":"a","refresh":"r"}}`)
	a, _ := agent.Lookup("opencode")
	testutil.WriteFile(t, configpaths.MustDir(configpaths.Get().UserAgentConfigDir(a)), "auth.json", "{}")

	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	prompts := 0
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		prompts++
		return guideStartChoice, nil
	}
	if err := Guide(a, hostHome, testNetworkPolicy(), false, &ui); err != nil {
		t.Fatalf("Guide: %v", err)
	}
	if prompts != 0 {
		t.Errorf("managed config plan prompted %d times, want completed without a login hand-off", prompts)
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
	atomicWriteFn = func(string, []byte, os.FileMode) error { return errInjected }
	if err := EnableNativeProvisioning(); !errors.Is(err, errInjected) {
		t.Errorf("write error = %v, want %v", err, errInjected)
	}
}

func TestHasManagedConfigIsFalseWithoutSnippetMerge(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	spec := agent.ConfigMigrationSpec{ManagedSnippet: "snippet.json"}
	if hasManagedConfig(specOnlyMigrationAgent{spec: spec}, spec) {
		t.Error("hasManagedConfig = true for an agent without snippet merge")
	}
}
