package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/channel"
	"github.com/inoio/agents-sandbox/internal/sandbox/learn"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
	"github.com/inoio/agents-sandbox/internal/testutil"
	launcherconfig "github.com/inoio/agents-sandbox/internal/viperconfig"
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
	snippetPath := filepath.Join(
		configpathstest.CreateDir(filepath.Join(configpaths.Get().UserConfigDir(), mustOpencode(t).ConfigDirName())),
		"opencode-x.json5",
	)
	testutil.WritePath(t, snippetPath, `{"model":"x"}`)

	// A host opencode.jsonc plus a non-config file exercise both statuses.
	// The host home comes from the config-paths seam so the write stays in the
	// per-test temp tree instead of the developer's real ~/.config/opencode.
	hostHome, err := configpaths.Get().UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	hostOcDir := filepath.Join(hostHome, ".config", "opencode")
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
	if !strings.Contains(joined, "agent: opencode") {
		t.Errorf("expected the agent header line, got:\n%s", joined)
	}
	sawMerged := false
	for _, line := range ui.OutCalls {
		if strings.HasPrefix(line, "  merged ") {
			sawMerged = true
		}
	}
	if !sawMerged {
		t.Errorf("expected a 'merged' host-file status, got:\n%s", joined)
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
	for _, line := range ui.OutCalls {
		parts := strings.SplitN(line, "  <-  ", 2)
		if len(parts) == 2 && (parts[0] == "" || parts[1] == "" || parts[0] == parts[1]) {
			t.Errorf("mapping line must join two distinct values, got %q", line)
		}
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
	if err := os.MkdirAll(
		configpathstest.CreateDir(filepath.Join(configpaths.Get().UserConfigDir(), a.ConfigDirName())),
		0o755,
	); err != nil {
		t.Fatal(err)
	}

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config agent: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(joined, "No snippet files found") {
		t.Errorf("expected no-snippets message, got:\n%s", joined)
	}
	if strings.Contains(joined, "mirror files:") {
		t.Errorf("did not expect a mirror files section with no mirror files:\n%s", joined)
	}
}

// TestConfigAgentErrorsAboutUnparsableSnippet pins that a snippet which cannot
// be parsed is named in an error (not a warning) and the report does not claim
// no snippet exists.
func TestConfigAgentErrorsAboutUnparsableSnippet(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "config", "agent", "opencode")
	a, _ := agent.Lookup("opencode")
	snippetPath := filepath.Join(
		configpathstest.CreateDir(filepath.Join(configpaths.Get().UserConfigDir(), a.ConfigDirName())),
		"opencode-bad.json5",
	)
	testutil.WritePath(t, snippetPath, "{ this is not valid json5")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config agent: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if strings.Contains(joined, "No snippet files found") {
		t.Errorf("report claims no snippet exists though one was found and skipped:\n%s", joined)
	}
	if !strings.Contains(joined, "No snippet file could be merged") {
		t.Errorf("expected the all-skipped message, got:\n%s", joined)
	}
	if !strings.Contains(joined, "see the errors above") {
		t.Errorf("expected the all-skipped message to point at the errors, got:\n%s", joined)
	}
	if !slices.ContainsFunc(ui.ErrorCalls, func(c termiotest.ErrorCall) bool {
		return strings.Contains(c.Msg, snippetPath)
	}) {
		t.Errorf("ErrorCalls = %v, want an error naming %s", ui.ErrorCalls, snippetPath)
	}
	if strings.Contains(strings.Join(ui.WarnCalls, "\n"), snippetPath) {
		t.Errorf("WarnCalls = %v, want no warning naming %s", ui.WarnCalls, snippetPath)
	}
}

// TestConfigAgentPrintsMergedDespiteSkippedSnippet pins the mixed case behind
// the docs claim: when one matching snippet parses and another does not, the
// unparsable one is reported as an error and skipped, and the merged config is
// still printed rather than reported as no snippet merged.
func TestConfigAgentPrintsMergedDespiteSkippedSnippet(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "config", "agent", "opencode")
	snippetDir := configpathstest.CreateDir(
		filepath.Join(configpaths.Get().UserConfigDir(), mustOpencode(t).ConfigDirName()),
	)
	testutil.WriteFile(t, snippetDir, "opencode-model.json5", `{"model":"x"}`)
	badSnippet := filepath.Join(snippetDir, "opencode-bad.json5")
	testutil.WritePath(t, badSnippet, "{ this is not valid json5")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config agent: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(joined, `"model": "x"`) {
		t.Errorf("expected the merged config of the valid snippet, got:\n%s", joined)
	}
	if strings.Contains(joined, "No snippet file could be merged") {
		t.Errorf("report claims no snippet merged though one did:\n%s", joined)
	}
	if !slices.ContainsFunc(ui.ErrorCalls, func(c termiotest.ErrorCall) bool {
		return strings.Contains(c.Msg, badSnippet)
	}) {
		t.Errorf("ErrorCalls = %v, want an error naming %s", ui.ErrorCalls, badSnippet)
	}
}

// TestConfigAgentRejectsTwoArgs pins the config-agent arity bound with two
// otherwise-valid agent names, so the error can only be the arity check.
func TestConfigAgentRejectsTwoArgs(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "config", "agent", "pi", "claude-code")
	if err := cmd.Execute(); err == nil {
		t.Error("expected an error for two positional args")
	}
}

// TestConfigAgentSameFlagAndPositionalNotAmbiguous pins that an identical
// --agent and positional argument is not treated as a conflict.
func TestConfigAgentSameFlagAndPositionalNotAmbiguous(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "config", "agent", "--agent", "pi", "pi")
	if err := cmd.Execute(); err != nil {
		t.Errorf("identical --agent and positional must not be ambiguous, got %v", err)
	}
}

// TestProvisionHostConfigEnabledReadsResolver pins that a resolver's setting is
// returned rather than a hard-coded false.
func TestProvisionHostConfigEnabledReadsResolver(t *testing.T) {
	r := launcherconfig.NewResolverWithConfig(launcherconfig.Config{ProvisionHostConfig: true})
	cmd := &cobra.Command{}
	cmd.SetContext(context.WithValue(context.Background(), (*launcherConfigKey)(nil), r))
	if !provisionHostConfigEnabled(cmd) {
		t.Error("provisionHostConfigEnabled() = false, want the resolver's true")
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

func TestConfigAgentCodexPrintsMergedTOML(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "config", "agent", "codex")
	a, _ := agent.Lookup("codex")
	testutil.WriteFile(
		t,
		configpathstest.CreateDir(filepath.Join(configpaths.Get().UserConfigDir(), a.ConfigDirName())),
		"config-model.toml",
		"model = \"gpt-5\"\n",
	)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config agent codex: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	for _, want := range []string{"config-model.toml", "model = 'gpt-5'", "merged agent config:"} {
		if !strings.Contains(joined, want) {
			t.Errorf("output missing %q, got:\n%s", want, joined)
		}
	}
}

// TestConfigAgentOhMyPiMergesBothYAMLExtensions verifies that oh-my-pi
// snippets under either YAML extension merge into one YAML document.
func TestConfigAgentOhMyPiMergesBothYAMLExtensions(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "config", "agent", "oh-my-pi")
	a, _ := agent.Lookup("oh-my-pi")
	dir := configpathstest.CreateDir(filepath.Join(configpaths.Get().UserConfigDir(), a.ConfigDirName()))
	testutil.WriteFile(t, dir, "config-model.yml", "modelRoles:\n  default: anthropic/claude\n")
	testutil.WriteFile(t, dir, "config-startup.yaml", "startup:\n  quiet: true\n")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config agent oh-my-pi: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	for _, want := range []string{
		"config-model.yml", "config-startup.yaml", "merged agent config:",
		"default: anthropic/claude", "quiet: true",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("output missing %q, got:\n%s", want, joined)
		}
	}
}

// TestConfigAgentKimiCodeMergesTOMLSnippets verifies that kimi-code snippets
// merge into one TOML document.
func TestConfigAgentKimiCodeMergesTOMLSnippets(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "config", "agent", "kimi-code")
	a, _ := agent.Lookup("kimi-code")
	dir := configpathstest.CreateDir(filepath.Join(configpaths.Get().UserConfigDir(), a.ConfigDirName()))
	testutil.WriteFile(t, dir, "config-model.toml", "default_model = \"kimi-code/k3\"\n")
	testutil.WriteFile(t, dir, "config-provider.toml",
		"[providers.moonshot]\ntype = \"kimi\"\napi_key_env = \"MOONSHOT_API_KEY\"\n")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config agent kimi-code: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	for _, want := range []string{
		"config-model.toml", "config-provider.toml", "merged agent config:",
		"default_model = 'kimi-code/k3'", "[providers.moonshot]", "api_key_env = 'MOONSHOT_API_KEY'",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("output missing %q, got:\n%s", want, joined)
		}
	}
}

func TestConfigAgentAmbiguousFlagAndPositional(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "config", "agent", "--agent", "pi", "claude-code")
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected ambiguous error, got %v", err)
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
	agentDir := configpathstest.CreateDir(
		filepath.Join(configpaths.Get().UserConfigDir(), mustOpencode(t).ConfigDirName()),
	)
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

func TestProvisionHostConfigEnabledDefaultsTrueWithoutResolver(t *testing.T) {
	if !provisionHostConfigEnabled(&cobra.Command{}) {
		t.Error("provisionHostConfigEnabled() = false, want true without a resolver")
	}
}

// mustOpencode2 returns the built-in opencode2 agent profile, failing the test
// if it is not registered.
func mustOpencode2(t *testing.T) agent.Agent {
	t.Helper()
	a, ok := agent.Lookup("opencode2")
	if !ok {
		t.Fatal("opencode2 agent not registered")
	}
	return a
}

func TestConfigAgentOpencode2ShowsLegacyFallback(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "config", "agent", "--agent", "opencode2")
	legacy := filepath.Join(configpaths.Get().UserConfigDir(), "opencode")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, legacy, "opencode-model.json", `{"model":"legacy"}`)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config agent --agent opencode2: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(joined, "legacy fallback") {
		t.Errorf("expected the config-agent output to flag the legacy fallback, got:\n%s", joined)
	}
}

func TestConfigAgentOpencode2UsesOwnDir(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "config", "agent", "--agent", "opencode2")
	srcDir := configpathstest.CreateDir(
		filepath.Join(configpaths.Get().UserConfigDir(), mustOpencode2(t).ConfigDirName()),
	)
	testutil.WriteFile(t, srcDir, "opencode-model.json", `{"model":"v2"}`)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config agent --agent opencode2: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(joined, `"model": "v2"`) {
		t.Errorf("opencode2 config agent did not read its own dir, got:\n%s", joined)
	}
	if !strings.Contains(joined, srcDir) {
		t.Errorf("expected the opencode2 source dir %q in output, got:\n%s", srcDir, joined)
	}
	projDir := filepath.Join(configpaths.Get().ProjectConfigDirAbs(), "opencode2")
	if !strings.Contains(joined, projDir) {
		t.Errorf("expected the absolute opencode2 project dir %q in output, got:\n%s", projDir, joined)
	}
}

// TestConfigSlugFromContextReturnsResolverSlug pins the populated branch: a
// context carrying a resolver yields exactly the resolver's effective per-slug
// config slug, so the run boundary forwards the slug the resolver read.
func TestConfigSlugFromContextReturnsResolverSlug(t *testing.T) {
	r := launcherconfig.NewResolverWithConfig(launcherconfig.Config{ConfigSlug: "widget-bbb"})
	ctx := context.WithValue(context.Background(), (*launcherConfigKey)(nil), r)

	if got := configSlugFromContext(ctx); got != "widget-bbb" {
		t.Errorf("configSlugFromContext = %q, want %q", got, "widget-bbb")
	}
}

// TestConfigSlugFromContextEmptyWithoutResolver covers the no-resolver fallback:
// a context that never carried a resolver yields an empty slug rather than
// panicking, so a caller can treat "no resolver" as "no per-slug scope".
func TestConfigSlugFromContextEmptyWithoutResolver(t *testing.T) {
	if got := configSlugFromContext(context.Background()); got != "" {
		t.Errorf("configSlugFromContext(no resolver) = %q, want empty", got)
	}
}

func TestConfigAgentMarksMissingConfigDirs(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "config", "agent", "--agent", "opencode2")
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config agent --agent opencode2: %v", err)
	}
	cp := configpaths.Get()
	userDir := filepath.Join(cp.UserConfigDir(), "opencode2")
	projectDir := filepath.Join(cp.ProjectConfigDirAbs(), "opencode2")
	want := []string{
		"config dirs (primary):",
		fmt.Sprintf("  user: %q (missing)", userDir),
		fmt.Sprintf("  project: %q (missing)", projectDir),
	}
	for _, line := range want {
		if !slices.Contains(ui.OutCalls, line) {
			t.Errorf("expected line %q, got:\n%s", line, strings.Join(ui.OutCalls, "\n"))
		}
	}
}

func TestConfigAgentDoesNotMarkExistingConfigDir(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "config", "agent", "--agent", "opencode2")
	userDir := configpathstest.CreateDir(
		filepath.Join(configpaths.Get().UserConfigDir(), mustOpencode2(t).ConfigDirName()),
	)
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config agent --agent opencode2: %v", err)
	}
	projectDir := filepath.Join(configpaths.Get().ProjectConfigDirAbs(), "opencode2")
	want := []string{
		"config dirs (primary):",
		fmt.Sprintf("  user: %q", userDir),
		fmt.Sprintf("  project: %q (missing)", projectDir),
	}
	for _, line := range want {
		if !slices.Contains(ui.OutCalls, line) {
			t.Errorf("expected line %q, got:\n%s", line, strings.Join(ui.OutCalls, "\n"))
		}
	}
}

// TestResolveConfigAgentName pins that `config agent` without an argument
// shows the configured agent, and the default agent when none is configured.
func TestResolveConfigAgentName(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config launcherconfig.Config
		want   string
	}{
		{name: "configured agent", config: launcherconfig.Config{Agent: "pi"}, want: "pi"},
		{name: "no agent configured", want: defaultAgentName},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := buildConfigAgentCmd(&termiotest.Mock{})
			cmd.SetContext(context.WithValue(context.Background(), (*launcherConfigKey)(nil),
				launcherconfig.NewResolverWithConfig(tc.config)))

			name, err := resolveConfigAgentName(cmd, nil)
			if err != nil || name != tc.want {
				t.Errorf("resolveConfigAgentName = %q, %v; want %q", name, err, tc.want)
			}
		})
	}
}

// TestReservedHomeConfigTargets pins the home-relative path an agent's merged
// config occupies, which home provisioning must not overwrite: none for an
// agent without merged config, none when the path does not relate to the home.
func TestReservedHomeConfigTargets(t *testing.T) {
	opencode, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}
	for _, tc := range []struct {
		name  string
		agent agent.Agent
		home  string
		want  []string
	}{
		{name: "merged config", agent: opencode, home: "/home/dev", want: []string{".config/opencode/opencode.jsonc"}},
		{name: "no merged config", agent: noDaemonAgent{name: "nod"}, home: "/home/dev"},
		{name: "path not under the home", agent: relErrConfigMerger{noDaemonAgent{name: "x"}}, home: "/different/home"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reservedHomeConfigTargets(tc.agent, tc.home); !slices.Equal(got, tc.want) {
				t.Errorf("reservedHomeConfigTargets = %v, want %v", got, tc.want)
			}
		})
	}
}

// relErrConfigMerger is an Agent that implements ConfigMerger but whose
// VMConfigPath returns a relative path, so filepath.Rel fails against an
// absolute home.
type relErrConfigMerger struct{ noDaemonAgent }

func (relErrConfigMerger) SnippetPattern() string { return "" }

func (relErrConfigMerger) VMConfigPath(_ string) string { return "relative/path" }

func (relErrConfigMerger) ConfigFileNames() []string { return nil }

// TestConfigAgentRejectsAHomeTargetOutsideTheHome pins that `config agent`
// refuses a home manifest entry that would escape the home directory.
func TestConfigAgentRejectsAHomeTargetOutsideTheHome(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "config", "agent", "opencode")
	testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "config.yaml", "home:\n  ../escape:\n")

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `home manifest target "../escape" escapes the home directory`) {
		t.Fatalf("error = %v, want the escaping target refused", err)
	}
}

// stubPrepareMSBRuntime replaces the runtime preparer, so a command the preflight
// treats as runtime-needing (config learn inspects a volume) does not install the
// runtime in a test.
func stubPrepareMSBRuntime(t *testing.T) {
	t.Helper()
	old := prepareMSBRuntime
	prepareMSBRuntime = func(context.Context, termio.Logger, string) error { return nil }
	t.Cleanup(func() { prepareMSBRuntime = old })
}

// learnTestKey returns the state key config learn resolves for this test's
// project.
func learnTestKey(t *testing.T) state.Key {
	t.Helper()
	identity, err := git.ResolveIdentity(".")
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	return state.Key{Slug: identity.CheckoutSlug, Agent: defaultAgentName}
}

// seedLearnState writes the current home volume and, on top of it, a recorded
// learning session.
func seedLearnState(t *testing.T, key state.Key, record state.LearnSession) {
	t.Helper()
	if err := state.WithHomeState(key, func(cur *state.HomeState, _ bool) (bool, error) {
		cur.HomeVolume = record.HomeVolume
		return true, nil
	}); err != nil {
		t.Fatalf("seed home volume: %v", err)
	}
	if err := state.RecordLearnSession(key, record); err != nil {
		t.Fatalf("record learn session: %v", err)
	}
}

// stubLearnHomeVolume points config learn's home-volume lookup at a fixed path.
func stubLearnHomeVolume(t *testing.T, path string) {
	t.Helper()
	orig := learnHomeVolume
	learnHomeVolume = func(context.Context, state.Key) (string, error) { return path, nil }
	t.Cleanup(func() { learnHomeVolume = orig })
}

// analyseCapture records whether config learn ran the analysis and with what.
type analyseCapture struct {
	analysis learn.Analysis
	called   bool
}

// stubLearnAnalyse replaces the analysis seam, so the command's wiring can be
// asserted without applying anything.
func stubLearnAnalyse(t *testing.T) *analyseCapture {
	t.Helper()
	captured := &analyseCapture{}
	orig := learnAnalyse
	learnAnalyse = func(
		_ context.Context,
		a learn.Analysis,
		_ termio.UI,
		_ options.Mode,
		_ func(string) bool,
	) (learn.Summary, error) {
		captured.analysis = a
		captured.called = true
		return learn.Summary{}, nil
	}
	t.Cleanup(func() { learnAnalyse = orig })
	return captured
}

func TestConfigLearnNoRecordedSession(t *testing.T) {
	initTestProjectDir(t)
	cmd, ui := setupCommandFixtures(t, cmdConfig, cmdLearn)
	stubPrepareMSBRuntime(t)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config learn: %v", err)
	}
	if !slices.Contains(ui.InfoCalls, "No learning session to review.") {
		t.Errorf("InfoCalls = %v, want the no-session line", ui.InfoCalls)
	}
}

func TestConfigLearnRecreatedVolumeIsDiscarded(t *testing.T) {
	initTestProjectDir(t)
	cmd, ui := setupCommandFixtures(t, cmdConfig, cmdLearn)
	stubPrepareMSBRuntime(t)
	key := learnTestKey(t)
	seedLearnState(t, key, state.LearnSession{HomeVolume: "vol-old", ArtefactPath: "/nonexistent"})
	// The current state records a different volume than the session did.
	if err := state.WithHomeState(key, func(cur *state.HomeState, _ bool) (bool, error) {
		cur.HomeVolume = "vol-live"
		return true, nil
	}); err != nil {
		t.Fatalf("seed current volume: %v", err)
	}
	captured := stubLearnAnalyse(t)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config learn: %v", err)
	}
	if !strings.Contains(strings.Join(ui.InfoCalls, "\n"), "recreated") {
		t.Errorf("InfoCalls = %v, want a recreated-volume report", ui.InfoCalls)
	}
	if _, ok, err := state.ReadLearnSession(key); err != nil || ok {
		t.Errorf("ReadLearnSession = ok=%v err=%v, want the record cleared", ok, err)
	}
	if captured.called {
		t.Error("config learn analysed a session whose volume was recreated")
	}
}

func TestConfigLearnReplaysTheRecordedSession(t *testing.T) {
	initTestProjectDir(t)
	cmd, _ := setupCommandFixtures(t, cmdConfig, cmdLearn)
	stubPrepareMSBRuntime(t)
	key := learnTestKey(t)

	artefactPath, err := channel.Save(key.Slug, 1763458200, channel.Observation{
		Destinations: []channel.Destination{{Host: "api.example.com", Protocol: "https", Success: true, Count: 2}},
	})
	if err != nil {
		t.Fatalf("save artefact: %v", err)
	}
	seedLearnState(t, key, state.LearnSession{
		HomeVolume:   "vol-1",
		ArtefactPath: artefactPath,
		Baseline:     []state.LearnManifestEntry{{Key: ".config/opencode/opencode.jsonc", Hash: "h1"}},
	})
	stubLearnHomeVolume(t, "/home/vol")
	captured := stubLearnAnalyse(t)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config learn: %v", err)
	}
	if !captured.called {
		t.Fatal("config learn did not run the analysis")
	}
	if captured.analysis.Home != "/home/vol" {
		t.Errorf("analysis.Home = %q, want /home/vol", captured.analysis.Home)
	}
	if len(captured.analysis.Baseline.Entries) != 1 ||
		captured.analysis.Baseline.Entries[0].Key != ".config/opencode/opencode.jsonc" {
		t.Errorf("analysis.Baseline = %+v, want the recorded entry", captured.analysis.Baseline)
	}
	if len(captured.analysis.Observation.Destinations) != 1 ||
		captured.analysis.Observation.Destinations[0].Host != "api.example.com" {
		t.Errorf("analysis.Observation = %+v, want the loaded artefact", captured.analysis.Observation)
	}
}

// TestConfigLearnConsumesTheRecord pins that a successful replay clears the
// session record and deletes the capture, so a second `config learn` does not
// re-prompt and the artefact does not linger.
func TestConfigLearnConsumesTheRecord(t *testing.T) {
	initTestProjectDir(t)
	cmd, _ := setupCommandFixtures(t, cmdConfig, cmdLearn)
	stubPrepareMSBRuntime(t)
	key := learnTestKey(t)

	artefactPath, err := channel.Save(key.Slug, 1763458200, channel.Observation{
		Destinations: []channel.Destination{{Host: "api.example.com", Protocol: "https", Success: true, Count: 1}},
	})
	if err != nil {
		t.Fatalf("save artefact: %v", err)
	}
	seedLearnState(t, key, state.LearnSession{HomeVolume: "vol-1", ArtefactPath: artefactPath})
	stubLearnHomeVolume(t, "/home/vol")
	stubLearnAnalyse(t)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config learn: %v", err)
	}
	if _, ok, err := state.ReadLearnSession(key); err != nil || ok {
		t.Errorf("ReadLearnSession = ok=%v err=%v, want the record cleared", ok, err)
	}
	if _, err := os.Stat(artefactPath); !os.IsNotExist(err) {
		t.Errorf("artefact still present after config learn (err = %v)", err)
	}
}

func TestConfigLearnNonInteractiveWritesNothing(t *testing.T) {
	initTestProjectDir(t)
	cmd, ui := setupCommandFixtures(t, cmdConfig, cmdLearn)
	stubPrepareMSBRuntime(t)
	key := learnTestKey(t)
	seedLearnState(t, key, state.LearnSession{HomeVolume: "vol-1"})
	stubLearnHomeVolume(t, "/home/vol")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config learn: %v", err)
	}
	if len(ui.AcknowledgeCalls) != 1 {
		t.Fatalf("AcknowledgeCalls = %v, want the one-line notice", ui.AcknowledgeCalls)
	}
	if !strings.Contains(ui.AcknowledgeCalls[0], "run --learn") {
		t.Errorf("notice %q, want it to name `run --learn`", ui.AcknowledgeCalls[0])
	}
}

// learnBareCmd returns a bare command with a background context, so
// resolverFromContext yields nil (the no-config path).
func learnBareCmd() *cobra.Command {
	return &cobra.Command{}
}

// learnCmdWithResolver returns a bare command whose context carries a resolver
// built from cfg, as the root command installs it.
func learnCmdWithResolver(cfg launcherconfig.Config) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(context.WithValue(
		context.Background(),
		(*launcherConfigKey)(nil),
		launcherconfig.NewResolverWithConfig(cfg),
	))
	return cmd
}

// learnCheckoutSlug returns the checkout slug config learn resolves for this
// test's project.
func learnCheckoutSlug(t *testing.T) string {
	t.Helper()
	identity, err := git.ResolveIdentity(".")
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	return identity.CheckoutSlug
}

// TestConfigLearnResolveIdentityFailure pins that an unresolvable project
// directory is reported rather than deriving a degraded slug.
func TestConfigLearnResolveIdentityFailure(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove cwd: %v", err)
	}

	if err := runConfigLearn(learnBareCmd(), &termiotest.Mock{}); err == nil {
		t.Fatal("runConfigLearn over a removed cwd = nil error, want the identity failure")
	}
}

// TestConfigLearnUsesResolverAgentAndPolicy pins that the resolver supplies the
// agent key and the pre-overlay current policy.
func TestConfigLearnUsesResolverAgentAndPolicy(t *testing.T) {
	initTestProjectDir(t)
	configpathstest.WithMockConfigPaths(t)
	stubLearnHomeVolume(t, "/home/vol")

	key := state.Key{Slug: learnCheckoutSlug(t), Agent: "pi"}
	seedLearnState(t, key, state.LearnSession{HomeVolume: "vol-1"})
	captured := stubLearnAnalyse(t)

	cfg := launcherconfig.Config{
		Agent:   "pi",
		Network: network.Policy{Profiles: []network.Profile{network.ProfileNone}},
	}
	if err := runConfigLearn(learnCmdWithResolver(cfg), &termiotest.Mock{}); err != nil {
		t.Fatalf("config learn: %v", err)
	}
	if !captured.called {
		t.Fatal("config learn did not run the analysis")
	}
	if captured.analysis.Agent != "pi" {
		t.Errorf("analysis.Agent = %q, want pi from the resolver", captured.analysis.Agent)
	}
	if captured.analysis.Key.Agent != "pi" {
		t.Errorf("analysis.Key.Agent = %q, want pi from the resolver", captured.analysis.Key.Agent)
	}
	if captured.analysis.Current.Fingerprint() != cfg.Network.Effective().Fingerprint() {
		t.Errorf("analysis.Current = %q, want the resolver's effective policy",
			captured.analysis.Current.Fingerprint())
	}
}

// TestConfigLearnReadSessionFailure pins that a session-read failure is returned.
func TestConfigLearnReadSessionFailure(t *testing.T) {
	initTestProjectDir(t)
	configpathstest.WithMockConfigPaths(t)
	orig := learnReadSession
	learnReadSession = func(state.Key) (state.LearnSession, bool, error) {
		return state.LearnSession{}, false, errors.New("read session boom")
	}
	t.Cleanup(func() { learnReadSession = orig })

	err := runConfigLearn(learnBareCmd(), &termiotest.Mock{})
	if err == nil || !strings.Contains(err.Error(), "read session boom") {
		t.Fatalf("runConfigLearn error = %v, want the session-read failure", err)
	}
}

// TestConfigLearnStateReadFailure pins that a state read failure is reported,
// naming the key.
func TestConfigLearnStateReadFailure(t *testing.T) {
	initTestProjectDir(t)
	configpathstest.WithMockConfigPaths(t)
	orig := learnReadSession
	learnReadSession = func(state.Key) (state.LearnSession, bool, error) {
		return state.LearnSession{HomeVolume: "vol-1"}, true, nil
	}
	t.Cleanup(func() { learnReadSession = orig })

	err := runConfigLearn(learnBareCmd(), &termiotest.Mock{})
	if err == nil || !strings.Contains(err.Error(), "read state for") {
		t.Fatalf("runConfigLearn error = %v, want the wrapped state read failure", err)
	}
}

// TestConfigLearnHomeVolumeFailure pins that a home-volume resolution failure is
// returned rather than analysing against a missing home.
func TestConfigLearnHomeVolumeFailure(t *testing.T) {
	initTestProjectDir(t)
	configpathstest.WithMockConfigPaths(t)
	key := learnTestKey(t)
	seedLearnState(t, key, state.LearnSession{HomeVolume: "vol-1"})

	orig := learnHomeVolume
	learnHomeVolume = func(context.Context, state.Key) (string, error) {
		return "", errors.New("home boom")
	}
	t.Cleanup(func() { learnHomeVolume = orig })

	err := runConfigLearn(learnBareCmd(), &termiotest.Mock{})
	if err == nil || !strings.Contains(err.Error(), "home boom") {
		t.Fatalf("runConfigLearn error = %v, want the home-volume failure", err)
	}
}

// TestConfigLearnLoadCaptureFailure pins that a recorded capture that cannot be
// loaded is reported rather than analysed as an empty session.
func TestConfigLearnLoadCaptureFailure(t *testing.T) {
	initTestProjectDir(t)
	configpathstest.WithMockConfigPaths(t)
	key := learnTestKey(t)
	seedLearnState(t, key, state.LearnSession{
		HomeVolume:   "vol-1",
		ArtefactPath: filepath.Join(t.TempDir(), "absent.json"),
	})
	stubLearnHomeVolume(t, "/home/vol-learn")

	err := runConfigLearn(learnBareCmd(), &termiotest.Mock{})
	if err == nil || !strings.Contains(err.Error(), "read observation") {
		t.Fatalf("runConfigLearn error = %v, want the capture load failure", err)
	}
}

// TestConfigLearnAnalyseFailure pins that a failed analysis is returned.
func TestConfigLearnAnalyseFailure(t *testing.T) {
	initTestProjectDir(t)
	configpathstest.WithMockConfigPaths(t)
	key := learnTestKey(t)
	seedLearnState(t, key, state.LearnSession{HomeVolume: "vol-1"})
	stubLearnHomeVolume(t, "/home/vol")

	orig := learnAnalyse
	learnAnalyse = func(context.Context, learn.Analysis, termio.UI, options.Mode, func(string) bool) (learn.Summary, error) {
		return learn.Summary{}, errors.New("analyse boom")
	}
	t.Cleanup(func() { learnAnalyse = orig })

	err := runConfigLearn(learnBareCmd(), &termiotest.Mock{})
	if err == nil || !strings.Contains(err.Error(), "analyse boom") {
		t.Fatalf("runConfigLearn error = %v, want the analysis failure", err)
	}
}

// TestConfigLearnWarnsWhenClearingTheRecordFails pins that a failed clear is a
// warning, not a failed command.
func TestConfigLearnWarnsWhenClearingTheRecordFails(t *testing.T) {
	initTestProjectDir(t)
	configpathstest.WithMockConfigPaths(t)
	key := learnTestKey(t)
	seedLearnState(t, key, state.LearnSession{HomeVolume: "vol-1"})
	stubLearnHomeVolume(t, "/home/vol")
	stubLearnAnalyse(t)

	orig := learnClearSession
	learnClearSession = func(state.Key) error { return errors.New("clear boom") }
	t.Cleanup(func() { learnClearSession = orig })

	ui := &termiotest.Mock{}
	if err := runConfigLearn(learnBareCmd(), ui); err != nil {
		t.Fatalf("config learn: %v", err)
	}
	if !warnsAbout(ui, "clearing the session record failed") {
		t.Errorf("WarnCalls = %v, want the clear-failure warning", ui.WarnCalls)
	}
}

// TestConfigLearnWarnsWhenDeletingTheCaptureFails pins that a failed capture
// delete is a warning, not a failed command.
func TestConfigLearnWarnsWhenDeletingTheCaptureFails(t *testing.T) {
	initTestProjectDir(t)
	configpathstest.WithMockConfigPaths(t)
	key := learnTestKey(t)
	artefactPath, err := channel.Save(key.Slug, 1763458200, channel.Observation{})
	if err != nil {
		t.Fatalf("save artefact: %v", err)
	}
	seedLearnState(t, key, state.LearnSession{HomeVolume: "vol-1", ArtefactPath: artefactPath})
	stubLearnHomeVolume(t, "/home/vol")
	stubLearnAnalyse(t)

	orig := learnDeleteArtefact
	learnDeleteArtefact = func(string) error { return errors.New("delete boom") }
	t.Cleanup(func() { learnDeleteArtefact = orig })

	ui := &termiotest.Mock{}
	if err := runConfigLearn(learnBareCmd(), ui); err != nil {
		t.Fatalf("config learn: %v", err)
	}
	if !warnsAbout(ui, "deleting the capture failed") {
		t.Errorf("WarnCalls = %v, want the delete-failure warning", ui.WarnCalls)
	}
}

// warnsAbout reports whether any warning contains want.
func warnsAbout(ui *termiotest.Mock, want string) bool {
	for _, warning := range ui.WarnCalls {
		if strings.Contains(warning, want) {
			return true
		}
	}
	return false
}

// hardenSlug returns the effective config slug config harden resolves for this
// test's project, as the launcher-config resolver exposes it.
func hardenSlug(t *testing.T, cmd *cobra.Command) string {
	t.Helper()
	if slug := configSlugFromContext(cmd.Context()); slug != "" {
		return slug
	}
	identity, err := git.ResolveIdentity(".")
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	return effectiveConfigSlug(identity)
}

// hardenPerSlugConfigFile returns the per-slug user config file a config-key
// finding writes, so a test asserts nothing was written.
func hardenPerSlugConfigFile(t *testing.T, slug string) string {
	t.Helper()
	return filepath.Join(configpaths.Get().UserSlugConfigDir(slug), "config.yaml")
}

// TestConfigHardenNonInteractiveWritesNothing pins that a config harden run on a
// non-interactive terminal reaches the applier, prints its one-line notice
// naming `run --learn`, and writes no file.
func TestConfigHardenNonInteractiveWritesNothing(t *testing.T) {
	initTestProjectDir(t)
	// The default config has provision-host-config true, so the finder yields a
	// finding and Apply is reached.
	cmd, ui := setupCommandFixtures(t, cmdConfig, cmdHarden)
	stubPrepareMSBRuntime(t)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config harden: %v", err)
	}
	if len(ui.AcknowledgeCalls) != 1 {
		t.Fatalf("AcknowledgeCalls = %v, want the one-line notice", ui.AcknowledgeCalls)
	}
	if !strings.Contains(ui.AcknowledgeCalls[0], "run --learn") {
		t.Errorf("notice %q, want it to name `run --learn`", ui.AcknowledgeCalls[0])
	}
	configFile := hardenPerSlugConfigFile(t, hardenSlug(t, cmd))
	if _, err := os.Stat(configFile); !os.IsNotExist(err) {
		t.Errorf("config harden wrote %s (stat err = %v), want nothing written", configFile, err)
	}
}

// TestConfigHardenDryRunWritesNothing pins that --dry-run makes no change and
// asks nothing, even where a finding exists.
func TestConfigHardenDryRunWritesNothing(t *testing.T) {
	initTestProjectDir(t)
	cmd, ui := setupCommandFixtures(t, cmdConfig, cmdHarden, "--"+flagDryRun)
	stubPrepareMSBRuntime(t)
	selects := 0
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		selects++
		return "", nil
	}

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config harden --dry-run: %v", err)
	}
	if selects != 0 {
		t.Errorf("Select calls = %d, want none under --dry-run", selects)
	}
	configFile := hardenPerSlugConfigFile(t, hardenSlug(t, cmd))
	if _, err := os.Stat(configFile); !os.IsNotExist(err) {
		t.Errorf("config harden --dry-run wrote %s (stat err = %v), want nothing written", configFile, err)
	}
}

// TestConfigHardenNoFindingsIsQuiet pins that a clean config yields zero
// findings, so the command prints nothing and never reaches the applier.
func TestConfigHardenNoFindingsIsQuiet(t *testing.T) {
	initTestProjectDir(t)
	cmd, ui := setupCommandFixtures(t, cmdConfig, cmdHarden)
	stubPrepareMSBRuntime(t)
	// A clean posture: the terminal none profile with no egress allow list and
	// host-config provisioning off. The public profile would report (C3) even
	// with no allow list.
	testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "config.yaml",
		"network:\n  profile: none\nprovision-host-config: false\n")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config harden: %v", err)
	}
	if len(ui.AcknowledgeCalls) != 0 {
		t.Errorf("AcknowledgeCalls = %v, want silence with no findings", ui.AcknowledgeCalls)
	}
	if joined := strings.Join(ui.InfoCalls, "\n"); joined != "" {
		t.Errorf("InfoCalls = %v, want silence with no findings", ui.InfoCalls)
	}
	if len(ui.OutCalls) != 0 {
		t.Errorf("OutCalls = %v, want silence with no findings", ui.OutCalls)
	}
}

// TestConfigHardenHelpAndTree pins that config harden is present in the config
// help and in `tree`.
func TestConfigHardenHelpAndTree(t *testing.T) {
	out := commandOut(t, cmdConfig, cmdHarden, "--help")
	if !strings.Contains(out, cmdHarden) {
		t.Errorf("config harden help does not name the command:\n%s", out)
	}

	treeOut := strings.Join(buildTree(t).InfoCalls, "\n")
	row := "── " + cmdHarden + "  "
	if !strings.Contains(treeOut, row) {
		t.Fatalf("expected a %q row in tree output:\n%s", row, treeOut)
	}
}

// acceptEveryFinding makes the mock UI interactive and answers every finding
// prompt with "Apply".
func acceptEveryFinding(ui *termiotest.Mock) {
	ui.IsInteractiveResult = true
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		return "a", nil
	}
}

// TestConfigHardenAppliesAFindingInteractively pins that an accepted finding is
// written: the host-config provisioning finding turns the key off at the
// per-slug user scope.
func TestConfigHardenAppliesAFindingInteractively(t *testing.T) {
	initTestProjectDir(t)
	cmd, ui := setupCommandFixtures(t, cmdConfig, cmdHarden)
	stubPrepareMSBRuntime(t)
	acceptEveryFinding(ui)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config harden: %v", err)
	}
	configFile := hardenPerSlugConfigFile(t, hardenSlug(t, cmd))
	written, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("config harden did not write %s: %v", configFile, err)
	}
	if !strings.Contains(string(written), "provision-host-config: false") {
		t.Errorf("per-slug config = %q, want provision-host-config false", written)
	}
}

// hardenScopeSecretFile returns the per-slug user scope's env.secret.yaml.
func hardenScopeSecretFile(t *testing.T, slug string) string {
	t.Helper()
	return filepath.Join(configpaths.Get().UserSlugConfigDir(slug), configpaths.EnvSecretFileName+".yaml")
}

// TestConfigHardenMovesAnInlineConfigSecret pins the load-bearing secret-value
// source: an inline credential in a managed agent config file resolves in memory
// (without it every C7 add is refused) and lands in the per-slug env.secret.yaml.
func TestConfigHardenMovesAnInlineConfigSecret(t *testing.T) {
	initTestProjectDir(t)
	cmd, ui := setupCommandFixtures(t, cmdConfig, cmdHarden)
	stubPrepareMSBRuntime(t)
	acceptEveryFinding(ui)
	slug := hardenSlug(t, cmd)
	agentDir := configpathstest.CreateDir(
		filepath.Join(configpaths.Get().UserSlugConfigDir(slug), mustOpencode(t).ConfigDirName()),
	)
	testutil.WritePath(t, filepath.Join(agentDir, "opencode.jsonc"),
		`{"provider":{"openai":{"options":{"apiKey":"sk-inline","baseURL":"https://api.openai.com/v1"}}}}`)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config harden: %v", err)
	}
	written, err := os.ReadFile(hardenScopeSecretFile(t, slug))
	if err != nil {
		t.Fatalf("config harden did not write the secret file: %v", err)
	}
	if !strings.Contains(string(written), "OPENAI_API_KEY") ||
		!strings.Contains(string(written), "sk-inline") {
		t.Errorf("secret file = %q, want the inline value moved under OPENAI_API_KEY", written)
	}
}

// TestConfigHardenMovesAProjectSecret pins the other secret-value source: a
// static secret committed in the approved project scope resolves from the
// scope's env.secret.yaml and is moved to the per-slug user scope (removed from
// the project file, added to the per-slug one).
func TestConfigHardenMovesAProjectSecret(t *testing.T) {
	initTestProjectDir(t)
	cmd, ui := setupCommandFixtures(t, cmdConfig, cmdHarden)
	stubPrepareMSBRuntime(t)
	projectSecret := filepath.Join(configpaths.Get().ProjectConfigDir(), configpaths.EnvSecretFileName+".yaml")
	testutil.WritePath(t, projectSecret,
		"WIDGET_TOKEN:\n  value: sk-project-secret\n  hosts:\n    - api.widget.example\n")
	approveProjectConfig(t)
	acceptEveryFinding(ui)
	slug := hardenSlug(t, cmd)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config harden: %v", err)
	}
	written, err := os.ReadFile(hardenScopeSecretFile(t, slug))
	if err != nil {
		t.Fatalf("config harden did not write the per-slug secret file: %v", err)
	}
	if !strings.Contains(string(written), "WIDGET_TOKEN") ||
		!strings.Contains(string(written), "sk-project-secret") {
		t.Errorf("per-slug secret file = %q, want the project secret moved under WIDGET_TOKEN", written)
	}
	remaining, err := os.ReadFile(projectSecret)
	if err == nil && strings.Contains(string(remaining), "WIDGET_TOKEN") {
		t.Errorf("project secret file still names WIDGET_TOKEN:\n%s", remaining)
	}
}

// TestConfigHardenLeavesAnUnapprovedProjectLayerAlone pins that a project
// secret whose layer is unapproved is reported once and never read or moved:
// the trust gate excludes the layer, and config harden writes nothing for it.
func TestConfigHardenLeavesAnUnapprovedProjectLayerAlone(t *testing.T) {
	initTestProjectDir(t)
	cmd, ui := setupCommandFixtures(t, cmdConfig, cmdHarden)
	stubPrepareMSBRuntime(t)
	projectSecret := filepath.Join(configpaths.Get().ProjectConfigDir(), configpaths.EnvSecretFileName+".yaml")
	testutil.WritePath(t, projectSecret,
		"WIDGET_TOKEN:\n  value: sk-project-secret\n  hosts:\n    - api.widget.example\n")
	acceptEveryFinding(ui)
	slug := hardenSlug(t, cmd)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config harden: %v", err)
	}
	// The gate reports the unapproved layer once; harden adds no second notice.
	if len(ui.WarnCalls) != 1 {
		t.Errorf("WarnCalls = %v, want the gate's one notice", ui.WarnCalls)
	}
	if _, err := os.Stat(hardenScopeSecretFile(t, slug)); !os.IsNotExist(err) {
		t.Errorf("config harden wrote a per-slug secret for an unapproved layer (stat err = %v)", err)
	}
	if remaining, err := os.ReadFile(
		projectSecret,
	); err != nil ||
		!strings.Contains(string(remaining), "WIDGET_TOKEN") {
		t.Errorf("the unapproved project secret file changed (err = %v)", err)
	}
}
