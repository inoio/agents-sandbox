package firstrun

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestOfferWhenNoManagedConfigAndUndecided pins the first-run case: a checkout
// with no managed config and no recorded decision offers the menu.
func TestOfferWhenNoManagedConfigAndUndecided(t *testing.T) {
	f := Facts{Agent: "pi", Slug: "widget-abc"}
	if !Offer(f) {
		t.Fatal("an undecided checkout with no managed config must offer the menu")
	}
}

// TestOfferFalseWhenManagedConfigPresent pins that a checkout already configured
// for the agent offers nothing, whatever the decision record says.
func TestOfferFalseWhenManagedConfigPresent(t *testing.T) {
	f := Facts{Agent: "pi", Slug: "widget-abc", ManagedConfig: true}
	if Offer(f) {
		t.Fatal("a checkout with managed config must not offer the menu")
	}
}

// TestOfferFalseWhenDecidedForSameSource pins that a decided checkout whose host
// config is unchanged stays quiet.
func TestOfferFalseWhenDecidedForSameSource(t *testing.T) {
	f := Facts{
		Agent:          "pi",
		Slug:           "widget-abc",
		HostSourceHash: "hostconfig-abc123",
		Prior: state.FirstRunRecord{
			Choice:     state.FirstRunLearned,
			SourceHash: "hostconfig-abc123",
		},
	}
	if Offer(f) {
		t.Fatal("a decided checkout whose host config is unchanged must stay quiet")
	}
}

// TestOfferTrueWhenSourceHashChanged pins that a changed host config re-arms the
// decision exactly once: the recorded source hash no longer matches.
func TestOfferTrueWhenSourceHashChanged(t *testing.T) {
	f := Facts{
		Agent:          "pi",
		Slug:           "widget-abc",
		HostSourceHash: "hostconfig-def456",
		Prior: state.FirstRunRecord{
			Choice:     state.FirstRunLearned,
			SourceHash: "hostconfig-abc123",
		},
	}
	if !Offer(f) {
		t.Fatal("a changed host config must re-arm the menu")
	}
}

// TestOfferFalseWhenNoHostConfigAndAlreadyLearned pins the "learn by using it"
// case: a learned decision with no host config has an empty source hash on both
// sides, so it stays quiet rather than re-arming on an empty mismatch.
func TestOfferFalseWhenNoHostConfigAndAlreadyLearned(t *testing.T) {
	f := Facts{
		Agent: "pi",
		Slug:  "widget-abc",
		Prior: state.FirstRunRecord{Choice: state.FirstRunLearned},
	}
	if Offer(f) {
		t.Fatal("a learned decision with no host config must stay quiet")
	}
}

// TestBuildFactsCollectsHostFilesAndPriorDecision pins the assembler: it
// resolves the agent, probes the host config, reads the prior decision, and
// reports no managed config for a checkout that has none. It uses codex, whose
// seedable host file is config.toml (pi's settings.json is a login store and is
// not seedable).
func TestBuildFactsCollectsHostFilesAndPriorDecision(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".codex/config.toml", "model = \"gpt-5\"\n")
	if err := state.RecordFirstRun("widget-abc", "codex", state.FirstRunRecord{
		Choice:     state.FirstRunDismissed,
		SourceHash: "stale-hash",
	}); err != nil {
		t.Fatalf("RecordFirstRun: %v", err)
	}

	f, err := BuildFacts("codex", "widget-abc", hostHome, configpaths.Get())
	if err != nil {
		t.Fatalf("BuildFacts: %v", err)
	}
	if f.Agent != "codex" || f.Slug != "widget-abc" {
		t.Errorf("identity = %q/%q, want codex/widget-abc", f.Agent, f.Slug)
	}
	if len(f.HostFiles) != 1 || f.HostSourceHash == "" {
		t.Errorf("HostFiles = %v, hash = %q; want one host file and a non-empty hash", f.HostFiles, f.HostSourceHash)
	}
	if f.Prior.Choice != state.FirstRunDismissed {
		t.Errorf("Prior.Choice = %q, want %q", f.Prior.Choice, state.FirstRunDismissed)
	}
	if f.ManagedConfig {
		t.Error("no managed config was written, so ManagedConfig must be false")
	}
}

// TestBuildFactsManagedConfigFromAgentConfigDir pins that a file in the agent's
// primary per-slug config dir counts as managed config.
func TestBuildFactsManagedConfigFromAgentConfigDir(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	slug := "widget-abc"
	a, ok := agent.Lookup("pi")
	if !ok {
		t.Fatal("pi agent is not registered")
	}
	dirNames, err := agent.ConfigDirNames(a)
	if err != nil {
		t.Fatalf("ConfigDirNames: %v", err)
	}
	dir := filepath.Join(configpaths.Get().UserSlugConfigDir(slug), dirNames[0])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	testutil.WriteFile(t, dir, "settings.json", "{}")

	f, err := BuildFacts("pi", slug, t.TempDir(), configpaths.Get())
	if err != nil {
		t.Fatalf("BuildFacts: %v", err)
	}
	if !f.ManagedConfig {
		t.Error("a file in the agent's per-slug config dir must count as managed config")
	}
	if Offer(f) {
		t.Error("managed config must suppress the menu")
	}
}

// TestBuildFactsManagedConfigFromEnvSecret pins that a per-slug env.secret.yaml
// alone also counts as managed config.
func TestBuildFactsManagedConfigFromEnvSecret(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	slug := "widget-abc"
	dir := configpaths.Get().UserSlugConfigDir(slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	testutil.WriteFile(t, dir, "env.secret.yaml", "TOKEN: secret\n")

	f, err := BuildFacts("pi", slug, t.TempDir(), configpaths.Get())
	if err != nil {
		t.Fatalf("BuildFacts: %v", err)
	}
	if !f.ManagedConfig {
		t.Error("a per-slug env.secret.yaml must count as managed config")
	}
}

// TestBuildFactsManagedConfigFromGenericUserScope pins the P2 fix: managed
// config in the generic user scope (the layout docs/manage-config.md documents,
// shared by every project) is a managed config layer, so the first run does not
// offer the menu and override config the user already had.
func TestBuildFactsManagedConfigFromGenericUserScope(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	slug := "widget-abc"
	a, ok := agent.Lookup("codex")
	if !ok {
		t.Fatal("codex agent is not registered")
	}
	dirNames, err := agent.ConfigDirNames(a)
	if err != nil {
		t.Fatalf("ConfigDirNames: %v", err)
	}
	dir := filepath.Join(configpaths.Get().UserConfigDir(), dirNames[0])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	testutil.WriteFile(t, dir, "config.toml", "model = \"gpt-5\"\n")

	f, err := BuildFacts("codex", slug, t.TempDir(), configpaths.Get())
	if err != nil {
		t.Fatalf("BuildFacts: %v", err)
	}
	if !f.ManagedConfig {
		t.Error("a file in the generic user scope must count as managed config")
	}
	if Offer(f) {
		t.Error("generic-user managed config must suppress the menu")
	}
}

// TestBuildFactsManagedConfigFromProjectScope pins that managed config in the
// project scope counts too: the config merge reads it, so the first run must not
// offer to override it.
func TestBuildFactsManagedConfigFromProjectScope(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	slug := "widget-abc"
	a, ok := agent.Lookup("codex")
	if !ok {
		t.Fatal("codex agent is not registered")
	}
	dirNames, err := agent.ConfigDirNames(a)
	if err != nil {
		t.Fatalf("ConfigDirNames: %v", err)
	}
	dir := filepath.Join(configpaths.Get().ProjectConfigDir(), dirNames[0])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	testutil.WriteFile(t, dir, "config.toml", "model = \"gpt-5\"\n")

	f, err := BuildFacts("codex", slug, t.TempDir(), configpaths.Get())
	if err != nil {
		t.Fatalf("BuildFacts: %v", err)
	}
	if !f.ManagedConfig {
		t.Error("a file in the project scope must count as managed config")
	}
	if Offer(f) {
		t.Error("project-scope managed config must suppress the menu")
	}
}

// TestBuildFactsManagedConfigIgnoresNonRegularEntries pins that a config dir
// holding only a non-regular entry (a symlink) is not a managed config layer:
// the predicate names a regular file, and a symlink is not one.
func TestBuildFactsManagedConfigIgnoresNonRegularEntries(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	slug := "widget-abc"
	a, ok := agent.Lookup("codex")
	if !ok {
		t.Fatal("codex agent is not registered")
	}
	dirNames, err := agent.ConfigDirNames(a)
	if err != nil {
		t.Fatalf("ConfigDirNames: %v", err)
	}
	dir := filepath.Join(configpaths.Get().UserConfigDir(), dirNames[0])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), filepath.Join(dir, "config.toml")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	f, err := BuildFacts("codex", slug, t.TempDir(), configpaths.Get())
	if err != nil {
		t.Fatalf("BuildFacts: %v", err)
	}
	if f.ManagedConfig {
		t.Error("a symlink must not count as a managed config file")
	}
}

// TestBuildFactsManagedConfigFromLegacySecretFile pins the rule-1 fix: the probe
// counts every spelling of the secret layer the loader reads, not just
// env.secret.yaml, so a scope holding only the legacy env.secret suppresses the
// menu (and the host seed cannot override the user's value).
func TestBuildFactsManagedConfigFromLegacySecretFile(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	slug := "widget-abc"
	dir := configpaths.Get().UserSlugConfigDir(slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	scope := configpaths.ConfigScope{Kind: configpaths.ScopePerSlugUser, Dir: dir}
	testutil.WriteFile(t, dir, filepath.Base(scope.EnvSecretFile()), "OPENAI_API_KEY=sk-user@api.openai.com\n")

	f, err := BuildFacts("codex", slug, t.TempDir(), configpaths.Get())
	if err != nil {
		t.Fatalf("BuildFacts: %v", err)
	}
	if !f.ManagedConfig {
		t.Error("a legacy env.secret must count as managed config")
	}
	if Offer(f) {
		t.Error("a managed secret layer must suppress the menu")
	}
}

// TestBuildFactsUnknownAgentIsAnError pins that the assembler refuses an agent
// name the registry does not know.
func TestBuildFactsUnknownAgentIsAnError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	if _, err := BuildFacts("not-a-registered-agent", "widget-abc", t.TempDir(), configpaths.Get()); err == nil {
		t.Fatal("BuildFacts must error for an unknown agent name")
	}
}

// badConfigDirAgent is a registered stub whose config dir name is not a single
// path element, so ConfigDirNames refuses it. No built-in profile can produce
// this, but the registry extension point can, so the refusal path is reachable.
type badConfigDirAgent struct{}

func (badConfigDirAgent) Name() string               { return "firstrun-bad-config-dir-stub" }
func (badConfigDirAgent) ConfigDirName() string      { return ".." }
func (badConfigDirAgent) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }
func (badConfigDirAgent) Experimental() bool         { return false }

// TestBuildFactsErrorsWhenTheAgentConfigDirNameIsInvalid pins that BuildFacts
// surfaces the managed-config probe's ConfigDirNames refusal rather than
// swallowing it: the agent's config dir names are a precondition of the probe.
func TestBuildFactsErrorsWhenTheAgentConfigDirNameIsInvalid(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	agent.Register(badConfigDirAgent{})

	_, err := BuildFacts(badConfigDirAgent{}.Name(), "widget-abc", t.TempDir(), configpaths.Get())
	if err == nil {
		t.Fatal("BuildFacts must surface ConfigDirNames' invalid-name refusal")
	}
}

// TestManagedConfigPresentEmptySlugIsNotManaged pins that an empty slug has no
// per-slug scope and therefore no managed config: the early return keeps the
// probe from resolving a scope for the empty key.
func TestManagedConfigPresentEmptySlugIsNotManaged(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	a, ok := agent.Lookup("pi")
	if !ok {
		t.Fatal("pi agent is not registered")
	}

	present, err := managedConfigPresent(a, "", configpaths.Get())
	if err != nil {
		t.Fatalf("managedConfigPresent: %v", err)
	}
	if present {
		t.Error("an empty slug has no per-slug scope, so it must not report managed config")
	}
}

// TestBuildFactsErrorsWhenPriorDecisionIsUnreadable pins that a corrupt
// root-state document is surfaced, not treated as no prior decision: BuildFacts
// must not silently re-offer the menu over an unreadable state file. The
// wrapped cause is preserved, so a caller can tell the failure apart.
func TestBuildFactsErrorsWhenPriorDecisionIsUnreadable(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	statePath := filepath.Join(configpaths.Get().UserStateDir(), "state.yaml")
	if err := os.WriteFile(statePath, []byte("\tnot: [valid"), 0o644); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}

	_, err := BuildFacts("pi", "widget-abc", t.TempDir(), configpaths.Get())
	if err == nil {
		t.Fatal("BuildFacts must surface an unreadable prior-decision document")
	}
	if errors.Unwrap(err) == nil {
		t.Error("the prior-decision read error must be wrapped, not flattened")
	}
}

// TestManagedConfigPresentEmptySlugIgnoresSharedScopes pins the empty-slug
// early return: with no per-slug scope there is no key to manage, so a
// generic-user config must not report managed config and suppress the menu for
// an unkeyed run.
func TestManagedConfigPresentEmptySlugIgnoresSharedScopes(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	a, ok := agent.Lookup("codex")
	if !ok {
		t.Fatal("codex agent is not registered")
	}
	dirNames, err := agent.ConfigDirNames(a)
	if err != nil {
		t.Fatalf("ConfigDirNames: %v", err)
	}
	dir := filepath.Join(configpaths.Get().UserConfigDir(), dirNames[0])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	testutil.WriteFile(t, dir, "config.toml", "model = \"gpt-5\"\n")

	present, err := managedConfigPresent(a, "", configpaths.Get())
	if err != nil {
		t.Fatalf("managedConfigPresent: %v", err)
	}
	if present {
		t.Error("an empty slug has no per-slug scope, so it must not report managed config")
	}
}

// TestManagedConfigPresentReportsFalseWhenConfigDirNamesFails pins the error
// return: when the agent's config dir names cannot be resolved, the probe
// reports false alongside the error, so a caller never sees a spurious managed
// layer.
func TestManagedConfigPresentReportsFalseWhenConfigDirNamesFails(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	agent.Register(badConfigDirAgent{})

	present, err := managedConfigPresent(badConfigDirAgent{}, "widget-abc", configpaths.Get())
	if err == nil {
		t.Fatal("managedConfigPresent must surface ConfigDirNames' invalid-name refusal")
	}
	if present {
		t.Error("a failed probe must report false")
	}
	if errors.Unwrap(err) == nil {
		t.Error("the ConfigDirNames error must be wrapped, not flattened")
	}
}

// TestBuildFactsManagedConfigFromEnvFile pins that every spelling of the value
// layer counts, including the plain `env` file: a scope holding only it is a
// managed config layer, so the first run does not overwrite the user's value.
func TestBuildFactsManagedConfigFromEnvFile(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	slug := "widget-abc"
	dir := configpaths.Get().UserSlugConfigDir(slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	scope := configpaths.ConfigScope{Kind: configpaths.ScopePerSlugUser, Dir: dir}
	testutil.WriteFile(t, dir, filepath.Base(scope.EnvFile()), "OPENAI_API_KEY=sk-user\n")

	f, err := BuildFacts("codex", slug, t.TempDir(), configpaths.Get())
	if err != nil {
		t.Fatalf("BuildFacts: %v", err)
	}
	if !f.ManagedConfig {
		t.Error("a per-slug env file must count as managed config")
	}
}

// TestBuildFactsManagedConfigIgnoresADirectoryAtTheSecretPath pins that a
// directory named like a secret file is not a managed config file: fileExists
// names a regular file, so a directory must not suppress the menu.
func TestBuildFactsManagedConfigIgnoresADirectoryAtTheSecretPath(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	slug := "widget-abc"
	dir := configpaths.Get().UserSlugConfigDir(slug)
	if err := os.MkdirAll(filepath.Join(dir, "env.secret.yaml"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	f, err := BuildFacts("pi", slug, t.TempDir(), configpaths.Get())
	if err != nil {
		t.Fatalf("BuildFacts: %v", err)
	}
	if f.ManagedConfig {
		t.Error("a directory at the secret path must not count as a managed config file")
	}
}

func writeHostFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	testutil.WriteFile(t, root, rel, content)
}
