package journeys

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// TestStepAssertScopeExcludesHostLoginStore pins the host-seed login-store guard: a file the
// seed must drop must not appear anywhere in the scope's managed agent config,
// under whatever name, so a copied login store fails the step rather than
// passing the key/placeholder checks.
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
	// A copy under another name, in a nested dir, is still the login store.
	loginStore := filepath.Join(configDir, "nested", "copied-store.json")
	if err := os.MkdirAll(filepath.Dir(loginStore), 0o750); err != nil {
		t.Fatalf("create nested dir: %v", err)
	}
	content := opencode2Harness.Learn.HostLoginStore[opencodeLoginStoreRelPath]
	if err := os.WriteFile(loginStore, []byte(content), 0o600); err != nil {
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

func TestScopeWantResolvesAgainstTheHarnesssLearnFacts(t *testing.T) {
	learn := LearnFacts{CredentialEnvVar: "STUB_API_KEY", ManagedConfigKeys: []string{"baseURL"}}

	bare := ScopeWant{ManagedConfig: false, Placeholder: false}.expectation(learn)
	if bare.envVar != "STUB_API_KEY" || !slices.Equal(bare.hosts, []string{stubHost}) || bare.configKeys != nil ||
		bare.placeholder {
		t.Errorf("bare expectation = %+v, want only the scoped credential", bare)
	}
	full := ScopeWant{ManagedConfig: true, Placeholder: true}.expectation(learn)
	if !slices.Equal(full.configKeys, []string{"baseURL"}) || !full.placeholder {
		t.Errorf("full expectation = %+v, want the adapter's keys and the placeholder", full)
	}
}

func TestLearnStepsNeedTheHarnesssLearnFacts(t *testing.T) {
	if _, err := learnFacts(&Context{Agent: "codex"}); err == nil || !strings.Contains(err.Error(), "codex") {
		t.Errorf("learnFacts without facts = %v, want an error naming the harness", err)
	}
}
