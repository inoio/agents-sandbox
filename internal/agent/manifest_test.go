package agent_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestManifestSelectEmptyRel(t *testing.T) {
	rule := agent.ProvisionRule{Dir: ".config", Patterns: []string{"**"}}
	// Empty and root-slash rels must not panic; they are not files to copy.
	if agent.SelectProvisionRule(rule, "", false) {
		t.Error("empty rel should not panic")
	}
	if agent.SelectProvisionRule(rule, "/", false) {
		t.Error("root slash rel should not panic")
	}
}

func TestEvalProvisionRulesCopiesSelectedFiles(t *testing.T) {
	hostHome := t.TempDir()
	vmHome := t.TempDir()
	// Selected file.
	mustWrite(t, filepath.Join(hostHome, ".config/opencode/opencode.json"), `{"x":1}`)
	mustWrite(t, filepath.Join(hostHome, ".config/opencode/auth.json"), "secret")
	// Selected nested subdirectory (must be descended into).
	mustWrite(t, filepath.Join(hostHome, ".config/opencode/commands/agent.json"), "nested")
	// Excluded dir (pruned) and excluded file.
	mustWrite(t, filepath.Join(hostHome, ".config/opencode/node_modules/pkg/index.js"), "js")
	mustWrite(t, filepath.Join(hostHome, ".config/opencode/package.json"), "{}")
	// Missing dir (never exists) must be tolerated.
	rules := []agent.ProvisionRule{
		{Dir: ".config/opencode", Patterns: []string{"**", "!node_modules/", "!package*.json"}},
		{Dir: ".does/not/exist", Patterns: []string{"**"}},
	}

	got := map[string]string{}
	n, err := agent.EvalProvisionRules(rules, hostHome, vmHome, func(dst string, data []byte) error {
		got[filepath.ToSlash(dst)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("EvalProvisionRules error: %v", err)
	}
	if n != 3 {
		t.Errorf("copied count = %d, want 3", n)
	}
	rel := filepath.Join(vmHome, ".config/opencode")
	if got[filepath.ToSlash(filepath.Join(rel, "opencode.json"))] != `{"x":1}` {
		t.Errorf("opencode.json not copied correctly: %v", got)
	}
	if got[filepath.ToSlash(filepath.Join(rel, "auth.json"))] != "secret" {
		t.Errorf("auth.json not copied: %v", got)
	}
	if got[filepath.ToSlash(filepath.Join(rel, "commands/agent.json"))] != "nested" {
		t.Errorf("nested file not copied: %v", got)
	}
	if _, ok := got[filepath.ToSlash(filepath.Join(rel, "node_modules/pkg/index.js"))]; ok {
		t.Error("node_modules file should have been pruned")
	}
	if _, ok := got[filepath.ToSlash(filepath.Join(rel, "package.json"))]; ok {
		t.Error("package.json should have been excluded")
	}
}

func TestEvalProvisionRulesOnCopyError(t *testing.T) {
	hostHome := t.TempDir()
	mustWrite(t, filepath.Join(hostHome, ".config/opencode/opencode.json"), "data")
	rules := []agent.ProvisionRule{{Dir: ".config/opencode", Patterns: []string{"**"}}}

	wantErr := os.ErrClosed
	_, err := agent.EvalProvisionRules(rules, hostHome, t.TempDir(), func(string, []byte) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("EvalProvisionRules error = %v, want %v", err, wantErr)
	}
}

func TestEvalProvisionRulesDirectCopy(t *testing.T) {
	hostHome := t.TempDir()
	vmHome := t.TempDir()
	mustWrite(t, filepath.Join(hostHome, ".config/opencode/opencode.json"), "data")
	rules := []agent.ProvisionRule{{Dir: ".config/opencode", Patterns: []string{"**"}}}

	n, err := agent.EvalProvisionRules(rules, hostHome, vmHome, nil)
	if err != nil {
		t.Fatalf("EvalProvisionRules error: %v", err)
	}
	if n != 1 {
		t.Errorf("copied count = %d, want 1", n)
	}
}

func TestEvalProvisionRulesEmptyDirSkipped(t *testing.T) {
	hostHome := t.TempDir()
	n, err := agent.EvalProvisionRules(
		[]agent.ProvisionRule{{Dir: "", Patterns: []string{"**"}}},
		hostHome, t.TempDir(), nil,
	)
	if err != nil {
		t.Fatalf("EvalProvisionRules error: %v", err)
	}
	if n != 0 {
		t.Errorf("copied count = %d, want 0", n)
	}
}

func TestEvalProvisionRulesUnreadableFile(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root, cannot test unreadable file")
	}
	hostHome := t.TempDir()
	vmHome := t.TempDir()
	secret := filepath.Join(hostHome, ".config/opencode/secret.txt")
	mustWrite(t, secret, "secret")
	if err := os.Chmod(secret, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(secret, 0o600) })
	mustWrite(t, filepath.Join(hostHome, ".config/opencode/ok.txt"), "ok")

	n, err := agent.EvalProvisionRules(
		[]agent.ProvisionRule{{Dir: ".config/opencode", Patterns: []string{"**"}}},
		hostHome, vmHome, nil,
	)
	if err != nil {
		t.Fatalf("EvalProvisionRules should skip unreadable file, got error: %v", err)
	}
	if n != 1 {
		t.Errorf("copied count = %d, want 1 (unreadable file skipped)", n)
	}
}

func TestEvalProvisionRulesUnreadableDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root, cannot test unreadable dir")
	}
	hostHome := t.TempDir()
	vmHome := t.TempDir()
	blocked := filepath.Join(hostHome, ".config/opencode/blocked")
	mustWrite(t, filepath.Join(blocked, "inner.txt"), "inner")
	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })
	mustWrite(t, filepath.Join(hostHome, ".config/opencode/ok.txt"), "ok")

	n, err := agent.EvalProvisionRules(
		[]agent.ProvisionRule{{Dir: ".config/opencode", Patterns: []string{"**"}}},
		hostHome, vmHome, nil,
	)
	if err != nil {
		t.Fatalf("EvalProvisionRules should skip unreadable dir, got error: %v", err)
	}
	if n != 1 {
		t.Errorf("copied count = %d, want 1 (unreadable dir skipped)", n)
	}
}

func TestValidateProvisionRules(t *testing.T) {
	rules := []agent.ProvisionRule{
		{Dir: "a", Patterns: []string{"", "**", "!"}},
		{Dir: "b", Patterns: []string{"ok"}},
	}
	warnings := agent.ValidateProvisionRules(rules)
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "empty pattern in provision rule for a") {
		t.Errorf("missing empty-pattern warning, got: %v", warnings)
	}
	if !strings.Contains(joined, "bare '!' in provision rule for a") {
		t.Errorf("missing bare-! warning, got: %v", warnings)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
