package reprovision

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/agent"
	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/termio"
	"github.com/inoio/opencode-sandbox/internal/testutil"
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
	testutil.WriteFile(t, cp.UserOpencodeConfigDir(), "opencode-model.json", `{"model":"x"}`)
	cf, err = LoadConfigFilesForHost(plainAgent{}, hostHome, vmHome, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	if cf.HasSnippets {
		t.Error("expected HasSnippets=false for a non-ConfigMerger agent")
	}
	if cf.OpenCode != nil {
		t.Errorf("expected nil merged config, got %q", cf.OpenCode)
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
