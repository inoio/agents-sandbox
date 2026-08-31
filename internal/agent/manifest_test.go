package agent_test

import (
	"testing"

	"github.com/inoio/opencode-sandbox/internal/agent"
)

// selected reports whether a rel path under dir would be copied.
func selected(t *testing.T, rule agent.ProvisionRule, rel string, isDir bool) bool {
	t.Helper()
	// Eval walks a temp tree; here we test matcher selection directly via a
	// tiny exported helper. For this plan, expose agent.SelectProvisionRule.
	return agent.SelectProvisionRule(rule, rel, isDir)
}

func TestManifestIncludeAllExcept(t *testing.T) {
	rule := agent.ProvisionRule{Dir: ".config/opencode", Patterns: []string{
		"**", "!node_modules/", "!package*.json", "!.gitignore",
	}}
	if !selected(t, rule, "opencode.json", false) {
		t.Error("opencode.json should be selected")
	}
	if selected(t, rule, "node_modules", true) {
		t.Error("node_modules dir should NOT be selected")
	}
	if selected(t, rule, "package.json", false) {
		t.Error("package.json should NOT be selected")
	}
	if selected(t, rule, ".gitignore", false) {
		t.Error(".gitignore should NOT be selected")
	}
}

func TestManifestOnlyAuthFile(t *testing.T) {
	rule := agent.ProvisionRule{Dir: ".local/share/opencode", Patterns: []string{"auth.json"}}
	if !selected(t, rule, "auth.json", false) {
		t.Error("auth.json should be selected")
	}
	if selected(t, rule, "opencode.db", false) {
		t.Error("opencode.db should NOT be selected")
	}
	if selected(t, rule, "log", true) {
		t.Error("log dir should NOT be selected")
	}
}

func TestManifestOrderingLastWins(t *testing.T) {
	rule := agent.ProvisionRule{Dir: ".config", Patterns: []string{"**", "!secret/**", "secret/keep.txt"}}
	if !selected(t, rule, "keep.txt", false) {
		t.Error("later include should win for keep.txt")
	}
}

func TestManifestPrunesExcludedDir(t *testing.T) {
	rule := agent.ProvisionRule{Dir: ".config/opencode", Patterns: []string{"**", "!node_modules/"}}
	if selected(t, rule, "node_modules/foo/index.js", false) {
		t.Error("file under excluded dir must NOT be selected")
	}
}
