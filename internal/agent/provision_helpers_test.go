package agent_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// assertProvisionedSet writes every candidate under hostHome/agentDir and checks
// that the agent's provision rules copy exactly copied, relative to agentDir.
func assertProvisionedSet(t *testing.T, agentName, agentDir string, copied, notCopied []string) {
	t.Helper()
	profile, _ := agent.Lookup(agentName)
	provisioner, ok := agent.AsProvisioner(profile)
	if !ok {
		t.Fatalf("%s should implement Provisioner", agentName)
	}
	hostHome := t.TempDir()
	for _, rel := range append(slices.Clone(copied), notCopied...) {
		path := filepath.Join(hostHome, agentDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	vmHome := "/home/dev"
	var got []string
	_, err := agent.EvalProvisionRules(provisioner.ProvisionRules(), hostHome, vmHome,
		func(dst string, _ []byte, _ os.FileMode) error {
			rel, _ := filepath.Rel(filepath.Join(vmHome, agentDir), dst)
			got = append(got, filepath.ToSlash(rel))
			return nil
		})
	if err != nil {
		t.Fatalf("EvalProvisionRules: %v", err)
	}
	slices.Sort(got)
	if want := slices.Sorted(slices.Values(copied)); !slices.Equal(got, want) {
		t.Errorf("%s copied %v, want %v", agentName, got, want)
	}
}
