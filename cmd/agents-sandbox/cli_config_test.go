package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/termio"
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
		configpaths.CreateDir(filepath.Join(configpaths.Get().UserConfigDir(), mustOpencode(t).ConfigDirName())),
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
		configpaths.CreateDir(filepath.Join(configpaths.Get().UserConfigDir(), a.ConfigDirName())),
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
		configpaths.CreateDir(filepath.Join(configpaths.Get().UserConfigDir(), a.ConfigDirName())),
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
	dir := configpaths.CreateDir(filepath.Join(configpaths.Get().UserConfigDir(), a.ConfigDirName()))
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
	agentDir := configpaths.CreateDir(filepath.Join(configpaths.Get().UserConfigDir(), mustOpencode(t).ConfigDirName()))
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
	srcDir := configpaths.CreateDir(filepath.Join(configpaths.Get().UserConfigDir(), mustOpencode2(t).ConfigDirName()))
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

// TestConfigCommandsWorkOutsideWorktree pins that config resolution stays on the
// repo slug. The per-slug user config dir is shared by every clone of an origin,
// so an outside worktree — which has its own checkout slug — must still read it.
// The config commands are the reason the launcher-config resolver must not move
// to the checkout slug.
func TestConfigCommandsWorkOutsideWorktree(t *testing.T) {
	repo := testutil.InitRepo(t)
	testutil.RunGit(t, repo, "remote", "add", "origin", originCloneURL)
	outside := filepath.Join(t.TempDir(), "wt")
	testutil.RunGit(t, repo, "worktree", "add", outside)

	identity := mustIdentity(t, outside)
	if !identity.OutsideWorktree {
		t.Fatalf("%q must resolve as an outside worktree", outside)
	}
	if identity.RepoSlug == identity.CheckoutSlug {
		t.Fatalf("outside worktree must have its own checkout slug, got %q", identity.CheckoutSlug)
	}

	t.Chdir(outside)

	t.Run("config agent reads the repo-slug user config", func(t *testing.T) {
		cmd, ui := setupCommandFixtures(t, cmdConfig, cmdAgent, "opencode")
		// A value present only in the repo-slug layer proves the resolver used
		// the repo slug: the checkout-slug layer is empty, so a checkout-keyed
		// resolver would report the default (true) instead.
		perSlugDir := filepath.Join(configpaths.Get().UserConfigDir(), identity.RepoSlug)
		if err := os.MkdirAll(perSlugDir, 0o755); err != nil {
			t.Fatal(err)
		}
		testutil.WriteYAML(t, perSlugDir, "config.yaml", map[string]any{"provision-host-config": false})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("config agent: %v", err)
		}
		joined := strings.Join(ui.OutCalls, "\n")
		if !strings.Contains(joined, "provision-host-config=false") {
			t.Errorf("config agent must read the repo-slug user config, got:\n%s", joined)
		}
	})

	t.Run("config home works outside the checkout", func(t *testing.T) {
		cmd, ui := setupCommandFixtures(t, cmdConfig, cmdHome)
		testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "config.yaml", "home:\n  .gitconfig:\n")

		if err := cmd.Execute(); err != nil {
			t.Fatalf("config home: %v", err)
		}
		joined := strings.Join(ui.OutCalls, "\n")
		if !strings.Contains(joined, "/home/dev/.gitconfig") {
			t.Errorf("config home must list the shared user manifest, got:\n%s", joined)
		}
	})

	// The per-slug user config dir is keyed by the repo slug, so config paths
	// must report the repo slug (shared by every clone), not the outside
	// worktree's own checkout slug.
	t.Run("config paths reports the shared repo slug", func(t *testing.T) {
		cmd, ui := setupCommandFixtures(t, cmdConfig, cmdPaths)

		if err := cmd.Execute(); err != nil {
			t.Fatalf("config paths: %v", err)
		}
		out := strings.Join(ui.OutCalls, "\n")
		if !strings.Contains(out, "repo slug:  "+identity.RepoSlug) {
			t.Errorf("config paths must report the repo slug %q, got:\n%s", identity.RepoSlug, out)
		}
		perSlugDir := filepath.Join(configpaths.Get().UserConfigDir(), identity.RepoSlug)
		if !strings.Contains(out, perSlugDir) {
			t.Errorf("config paths must list the shared repo-slug dir %q, got:\n%s", perSlugDir, out)
		}
		if !slices.Contains(ui.InfoCalls, configPathsHint()) {
			t.Errorf("config paths must print the stderr hint; InfoCalls=%q", ui.InfoCalls)
		}
	})
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
	userDir := configpaths.CreateDir(filepath.Join(configpaths.Get().UserConfigDir(), mustOpencode2(t).ConfigDirName()))
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
			cmd := buildConfigAgentCmd(&termio.Mock{})
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
