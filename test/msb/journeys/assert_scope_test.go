package journeys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// TestStepAssertScopeExcludesHostLoginStore pins the host-seed login-store guard: a file the
// seed must drop must not appear in the scope's managed agent config dir, so a
// copied login store fails the step rather than passing the key/placeholder
// checks.
func TestStepAssertScopeExcludesHostLoginStore(t *testing.T) {
	const agentName = "opencode2"
	root := t.TempDir()
	if err := os.MkdirAll(driver.ProjectDir(root, agentName), 0o750); err != nil {
		t.Fatalf("create project dir: %v", err)
	}
	ctx := &Context{TB: t, Root: root, Agent: agentName, Harness: opencode2Harness, Home: t.TempDir()}

	scopeDir, err := journeyScopeDir(ctx)
	if err != nil {
		t.Fatalf("journeyScopeDir: %v", err)
	}
	configDir := filepath.Join(scopeDir, agentName)
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		t.Fatalf("create scope config dir: %v", err)
	}
	loginStore := filepath.Join(configDir, "auth.json")
	if err := os.WriteFile(loginStore, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write %s: %v", loginStore, err)
	}
	if err := StepAssertScopeExcludesHostLoginStore().Run(ctx); err == nil {
		t.Error("a copied login store: got nil error, want failure")
	} else if !strings.Contains(err.Error(), "must drop") {
		t.Errorf("error = %v, want the copied-login-store failure", err)
	}
	if err := os.Remove(loginStore); err != nil {
		t.Fatalf("remove %s: %v", loginStore, err)
	}
	if err := StepAssertScopeExcludesHostLoginStore().Run(ctx); err != nil {
		t.Errorf("an absent login store: %v", err)
	}

	unknownRoot := t.TempDir()
	if err := os.MkdirAll(driver.ProjectDir(unknownRoot, "nonexistent"), 0o750); err != nil {
		t.Fatalf("create the unknown agent's project dir: %v", err)
	}
	unknown := &Context{TB: t, Root: unknownRoot, Agent: "nonexistent", Harness: opencode2Harness, Home: t.TempDir()}
	if err := StepAssertScopeExcludesHostLoginStore().Run(unknown); err == nil {
		t.Error("an unknown agent: got nil error, want failure")
	} else if !strings.Contains(err.Error(), "unknown agent") {
		t.Errorf("error = %v, want the unknown-agent failure", err)
	}

	if err := StepAssertScopeExcludesHostLoginStore().Run(&Context{TB: t, Agent: agentName}); err == nil {
		t.Error("an adapter without a login store: got nil error, want failure")
	}
}
