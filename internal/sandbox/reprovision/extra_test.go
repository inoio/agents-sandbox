package reprovision

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// plainAgent implements only the base Agent, so it is neither a Provisioner
// nor a ConfigMerger; LoadConfigFilesForHost must produce no merged config.
type plainAgent struct{}

func (plainAgent) Name() string          { return "plain" }
func (plainAgent) ConfigDirName() string { return "plain" }
func (plainAgent) ImageSpec() agent.ImageSpec {
	return agent.ImageSpec{InstallCommand: "true"}
}

// badProvisionerAgent implements Provisioner with malformed rules so the
// validate-warning branch is exercised.
type badProvisionerAgent struct {
	plainAgent
}

func (badProvisionerAgent) ProvisionRules() []agent.ProvisionRule {
	return []agent.ProvisionRule{
		{Dir: ".config/tool", Patterns: []string{"", "**", "!"}},
	}
}

// relMergedConfigAgent is a ConfigMerger whose merged-config path is always
// relative, so deriving the reserved home target against an absolute vmHome
// fails in filepath.Rel.
type relMergedConfigAgent struct {
	plainAgent
}

func (relMergedConfigAgent) SnippetPattern() string { return "nomatch*.json" }
func (relMergedConfigAgent) VMConfigPath(string) string {
	return "relative/merged.jsonc"
}
func (relMergedConfigAgent) ConfigFileNames() []string { return []string{"merged.jsonc"} }

// TestLoadConfigFilesReservedTargetError verifies the error path when the
// merged-config path cannot be made relative to the VM home.
func TestLoadConfigFilesReservedTargetError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	_, err := LoadConfigFilesForHost(relMergedConfigAgent{}, t.TempDir(), t.TempDir(), &ui, true)
	if err == nil || !strings.Contains(err.Error(), "reserved home target") {
		t.Fatalf("LoadConfigFilesForHost error = %v, want reserved home target error", err)
	}
}

// TestLoadConfigFilesHomeFilesError verifies that a non-hook home target that
// fails VM-target resolution surfaces from the home-files loader (which runs
// after the hook loader).
func TestLoadConfigFilesHomeFilesError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml",
		"home:\n"+
			"  /absolute/target:\n"+
			"    source: somefile\n")

	ui := termio.NewTestMock(t)
	_, err := LoadConfigFilesForHost(opencodeTestAgent(), t.TempDir(), t.TempDir(), &ui, true)
	if err == nil || !strings.Contains(err.Error(), "must be relative to the home directory") {
		t.Fatalf("LoadConfigFilesForHost error = %v, want home-target resolution error", err)
	}
}

// TestLoadConfigFilesProvisionError verifies the error path when evaluating the
// agent's provision rules fails.
func TestLoadConfigFilesProvisionError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	restore := evalProvisionRules
	t.Cleanup(func() { evalProvisionRules = restore })
	boom := errors.New("provision boom")
	evalProvisionRules = func(
		[]agent.ProvisionRule,
		string,
		string,
		func(string, []byte, os.FileMode) error,
	) (int, error) {
		return 0, boom
	}

	ui := termio.NewTestMock(t)
	_, err := LoadConfigFilesForHost(badProvisionerAgent{}, t.TempDir(), t.TempDir(), &ui, true)
	if !errors.Is(err, boom) {
		t.Fatalf("LoadConfigFilesForHost error = %v, want %v", err, boom)
	}
}

// TestProvisionReturnsMkdirError verifies that a failure creating a parent
// directory surfaces from Provision.
func TestProvisionReturnsMkdirError(t *testing.T) {
	mkdirErr := errors.New("mkdir boom")
	cf := &ConfigFiles{
		HomeFiles: map[string][]byte{"/home/dev/.config/tool/cfg.toml": []byte("k=v\n")},
	}
	fs := msb.NewTestFS(nil, nil)
	fs.MkdirErr = mkdirErr
	sb := &msb.MockSandbox{FSValue_: fs, ShellCalls: &[]string{}}

	err := Provision(context.Background(), sb, cf)
	if !errors.Is(err, mkdirErr) {
		t.Fatalf("Provision error = %v, want %v", err, mkdirErr)
	}
}

func TestLoadConfigFilesNoMergedForNonConfigMerger(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	vmHome := t.TempDir()
	hostHome := t.TempDir()

	// No snippets: a non-ConfigMerger agent yields no merged config.
	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(plainAgent{}, hostHome, vmHome, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	if cf.HasSnippets {
		t.Error("expected HasSnippets=false when no snippet exists")
	}

	// Even with snippet files present, a non-ConfigMerger agent produces no
	// merged config because only ConfigMerger agents merge snippets.
	testutil.WriteFile(t, cp.UserAgentConfigDir(plainAgent{}), "opencode-model.json", `{"model":"x"}`)
	cf, err = LoadConfigFilesForHost(plainAgent{}, hostHome, vmHome, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	if cf.HasSnippets {
		t.Error("expected HasSnippets=false for a non-ConfigMerger agent")
	}
	if cf.Merged != nil {
		t.Errorf("expected nil merged config, got %q", cf.Merged)
	}
}

func TestLoadConfigFilesWarnsOnMalformedProvisionRules(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	hostHome := t.TempDir()
	vmHome := t.TempDir()
	// A real file under the rule dir so the valid pattern copies something.
	cfgPath := filepath.Join(hostHome, ".config/tool/config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{"x":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(badProvisionerAgent{}, hostHome, vmHome, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	if cf == nil {
		t.Fatal("expected a ConfigFiles result")
	}
	// A valid pattern copies host files; malformed ones only warn.
	if len(cf.Provisioned) == 0 {
		t.Errorf("expected some provisioned files, got %v", cf.Provisioned)
	}
}

func TestProvisionReturnsErrorWhenProvisionedWriteFails(t *testing.T) {
	writeErr := errors.New("provisioned write boom")
	cf := &ConfigFiles{
		Provisioned: map[string][]byte{"/home/dev/.config/tool/cfg.toml": []byte("k=v\n")},
	}
	fs := msb.NewTestFS(nil, nil)
	fs.WriteErr = writeErr
	sb := &msb.MockSandbox{FSValue_: fs, ShellCalls: &[]string{}}

	err := Provision(context.Background(), sb, cf)
	if !errors.Is(err, writeErr) {
		t.Errorf("Provision error = %v, want to wrap %v", err, writeErr)
	}
}
