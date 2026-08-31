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
// nor a ConfigMerger; LoadConfigFilesForHost must take the opencode fallback
// path for the merged config.
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

func TestLoadConfigFilesFallbackForNonConfigMerger(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	vmHome := t.TempDir()
	hostHome := t.TempDir()

	// No snippets: fallback yields empty config and hasSnippets=false.
	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(plainAgent{}, hostHome, vmHome, &ui)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	if cf.HasSnippets {
		t.Error("expected HasSnippets=false when no opencode snippet exists")
	}

	// With a snippet the fallback merges it at the opencode VM config path.
	testutil.WriteFile(t, cp.UserOpencodeConfigDir(), "opencode-model.json", `{"model":"x"}`)
	cf, err = LoadConfigFilesForHost(plainAgent{}, hostHome, vmHome, &ui)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	if !cf.HasSnippets {
		t.Error("expected HasSnippets=true when a snippet exists")
	}
	wantPath := filepath.Join(vmHome, ".config", "opencode", "opencode.json")
	if _, ok := cf.HomeFiles[wantPath]; ok {
		t.Errorf("merged config should not appear in HomeFiles, got %v", cf.HomeFiles)
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
	cf, err := LoadConfigFilesForHost(badProvisionerAgent{}, hostHome, vmHome, &ui)
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
