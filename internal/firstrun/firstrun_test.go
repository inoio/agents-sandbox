package firstrun

import (
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
// reports no managed config for a checkout that has none.
func TestBuildFactsCollectsHostFilesAndPriorDecision(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".pi/agent/settings.json", `{"token":"host-value"}`)
	if err := state.RecordFirstRun("widget-abc", "pi", state.FirstRunRecord{
		Choice:     state.FirstRunDismissed,
		SourceHash: "stale-hash",
	}); err != nil {
		t.Fatalf("RecordFirstRun: %v", err)
	}

	f, err := BuildFacts("pi", "widget-abc", hostHome, configpaths.Get())
	if err != nil {
		t.Fatalf("BuildFacts: %v", err)
	}
	if f.Agent != "pi" || f.Slug != "widget-abc" {
		t.Errorf("identity = %q/%q, want pi/widget-abc", f.Agent, f.Slug)
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

// TestBuildFactsUnknownAgentIsAnError pins that the assembler refuses an agent
// name the registry does not know.
func TestBuildFactsUnknownAgentIsAnError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	if _, err := BuildFacts("not-a-registered-agent", "widget-abc", t.TempDir(), configpaths.Get()); err == nil {
		t.Fatal("BuildFacts must error for an unknown agent name")
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
