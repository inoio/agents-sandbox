package reprovision

import (
	"context"
	"errors"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// opencodeTestAgent returns the default opencode profile for LoadConfigFiles
// tests that do not care which agent is active.
func opencodeTestAgent() agent.Agent {
	a, _ := agent.Lookup("")
	return a
}

// TestLoadConfigFilesWithSnippet verifies that a snippet produces a non-empty
// OpenCode config and includes the opencode.json key.
func TestLoadConfigFilesWithSnippet(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.ProjectAgentConfigDir(opencodeTestAgent()), "opencode-model.json", `{"model":"x"}`)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(opencodeTestAgent(), t.TempDir(), VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	if !cf.HasSnippets {
		t.Error("expected HasSnippets=true when a snippet exists")
	}
	if len(cf.Merged) == 0 {
		t.Error("expected non-empty OpenCode config")
	}
	if len(cf.Keys) == 0 || cf.Keys[0] != AgentConfigPath(opencodeTestAgent(), VMHomeDir) {
		t.Errorf("expected opencode config key in Keys, got %v", cf.Keys)
	}
}

// TestLoadConfigFilesWarnsMissingSource verifies that a home entry referencing a
// non-existent source produces a warning.
func TestLoadConfigFilesWarnsMissingSource(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml",
		"home:\n"+
			"  .tool/x:\n"+
			"    source: does-not-exist\n")

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(opencodeTestAgent(), t.TempDir(), VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	if len(cf.HomeFiles) != 0 {
		t.Errorf("expected no home files for missing source, got %v", cf.HomeFiles)
	}
	if len(ui.WarnCalls) == 0 {
		t.Error("expected a warning for the missing home source")
	}
}

// TestLoadConfigFilesBuildHomeFilesError verifies the error path when the
// home config is malformed.
func TestLoadConfigFilesBuildHomeFilesError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	// parseEntry rejects a non-string source value, surfacing a BuildHomeFiles error.
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml",
		"home:\n"+
			"  .tool/x:\n"+
			"    source: 123\n")

	ui := termio.NewTestMock(t)
	if _, err := LoadConfigFilesForHost(opencodeTestAgent(), t.TempDir(), VMHomeDir, &ui, true); err == nil {
		t.Error("expected an error for a malformed home source type")
	}
}

// TestLoadConfigFilesBuildHooksError verifies the error path when the manifest
// used by BuildHooks is malformed (invalid hook value).
func TestLoadConfigFilesBuildHooksError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	// A manifest that BuildHomeFiles accepts but BuildHooks rejects via the
	// hook validation. An unknown hook value is rejected by parseEntry, so use a
	// hook value that is invalid for BuildHooks filtering.
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml",
		"home:\n"+
			"  .vpn/x:\n"+
			"    source: s\n"+
			"    hook: 123\n")

	ui := termio.NewTestMock(t)
	if _, err := LoadConfigFilesForHost(opencodeTestAgent(), t.TempDir(), VMHomeDir, &ui, true); err == nil {
		t.Error("expected an error for an invalid hook value")
	}
}

// TestAgentConfigEqualMissingVMConfig verifies that a missing VM opencode
// config is reported as a mismatch.
func TestAgentConfigEqualMissingVMConfig(t *testing.T) {
	cf := &ConfigFiles{HasSnippets: true, Merged: []byte(`{"model":"x"}`)}
	if AgentConfigEqual(cf, map[string][]byte{}) {
		t.Error("expected mismatch when the VM config is absent")
	}
}

// TestJsonEqualParseError verifies that jsonEqual returns false when either
// side is not valid JSON.
func TestJsonEqualParseError(t *testing.T) {
	if jsonEqual([]byte(`{"a":1}`), []byte(`not-json`)) {
		t.Error("expected false when the VM side is invalid JSON")
	}
	if jsonEqual([]byte(`not-json`), []byte(`{"a":1}`)) {
		t.Error("expected false when the desired side is invalid JSON")
	}
}

// TestParseJSONError verifies that parseJSON returns an error on invalid input.
func TestParseJSONError(t *testing.T) {
	if _, err := parseJSON([]byte(`{oops`)); err == nil {
		t.Error("expected an error for invalid JSON")
	}
}

// TestMkdirAllFSExisting verifies that an existing directory short-circuits.
func TestMkdirAllFSExisting(t *testing.T) {
	fs := msb.NewTestFS(map[string][]byte{"/home/dev/.config/opencode": nil}, nil)
	made, err := mkdirAllFS(context.Background(), fs, "/home/dev/.config/opencode")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(made) != 0 {
		t.Errorf("expected no dirs created for existing path, got %v", made)
	}
}

// TestMkdirAllFSRootPath verifies the root/empty/`.` short-circuit branches.
func TestMkdirAllFSRootPath(t *testing.T) {
	fs := msb.NewTestFS(nil, nil)
	for _, p := range []string{"", "/", "."} {
		made, err := mkdirAllFS(context.Background(), fs, p)
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", p, err)
		}
		if len(made) != 0 {
			t.Errorf("expected no dirs for %q, got %v", p, made)
		}
	}
}

// TestChownPathsReportsUnsuccessfulShell verifies the error branch when the
// chown shell command reports a failed (non-zero) exit status.
func TestChownPathsReportsUnsuccessfulShell(t *testing.T) {
	cmd := "chown -R dev:dev /a /b"
	sb := &msb.MockSandbox{
		FSValue_: msb.NewTestFS(nil, nil),
		ShellOut: map[string]msb.ShellResult{
			cmd: msb.NewTestResult(false, 1, "", "permission denied", nil),
		},
	}
	err := chownPaths(context.Background(), sb, []string{"/b", "/a"})
	if err == nil {
		t.Error("expected an error when chown reports a failed exit status")
	}
}

// TestProvisionWriteOpenCodeError verifies that a write failure for the
// opencode config surfaces an error.
func TestProvisionWriteOpenCodeError(t *testing.T) {
	writeErr := errors.New("write boom")
	cf := &ConfigFiles{HasSnippets: true, Merged: []byte(`{"model":"x"}`)}
	fs := msb.NewTestFS(nil, nil)
	fs.WriteErr = writeErr
	sb := &msb.MockSandbox{FSValue_: fs, ShellErr: nil}
	err := Provision(context.Background(), sb, cf)
	if !errors.Is(err, writeErr) {
		t.Errorf("expected write error to be surfaced, got %v", err)
	}
}
