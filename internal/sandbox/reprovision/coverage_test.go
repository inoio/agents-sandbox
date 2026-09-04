package reprovision

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/opencode-sandbox/internal/agent"
	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/termio"
	"github.com/inoio/opencode-sandbox/internal/testutil"
)

// opencodeTestAgent returns the default opencode profile for LoadConfigFiles
// tests that do not care which agent is active.
func opencodeTestAgent() agent.Agent {
	a, _ := agent.Lookup("")
	return a
}

// TestParseKeyValueLinesOnLineError verifies that an error returned by the
// callback is propagated.
func TestParseKeyValueLinesOnLineError(t *testing.T) {
	boom := errors.New("boom")
	err := parseKeyValueLines("A=1\nB=2\n", func(key, _ string) error {
		if key == "B" {
			return boom
		}
		return nil
	})
	if !errors.Is(err, boom) {
		t.Errorf("expected boom, got %v", err)
	}
}

// TestParseKeyValueLinesSkipsNoEqualsLine verifies that a line without an '='
// separator is skipped rather than passed to the callback.
func TestParseKeyValueLinesSkipsNoEqualsLine(t *testing.T) {
	var seen []string
	err := parseKeyValueLines("A=1\nNOTSEPARATED\n\n# comment\n  \nC=3\n", func(key, value string) error {
		seen = append(seen, key+"="+value)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"A=1", "C=3"}
	if len(seen) != len(want) {
		t.Fatalf("seen = %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("seen[%d] = %q, want %q", i, seen[i], want[i])
		}
	}
}

// TestParseKeyValueLinesTrimsKeyAndValue verifies trimming of lines and that
// keys/values are passed verbatim (no further trimming of parts).
func TestParseKeyValueLinesHandlesWhitespace(t *testing.T) {
	var got []string
	err := parseKeyValueLines("  A = 1  \n\tB=x\n", func(key, value string) error {
		got = append(got, key+"="+value)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"A = 1", "B=x"}
	if len(got) != len(want) {
		t.Fatalf("got = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
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

// TestLoadConfigFilesWarnsMissingSource verifies that a home.yaml referencing a
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
		t.Error("expected a warning for the missing home.yaml source")
	}
}

// TestLoadConfigFilesBuildHomeFilesError verifies the error path when the
// home.yaml manifest is malformed.
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
		t.Error("expected an error for a malformed home.yaml source type")
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

// TestConfigChangeListLabelOnly verifies the default formatting branch when Old
// or New is empty.
func TestConfigChangeListLabelOnly(t *testing.T) {
	got := configChangeList([]Change{
		{Label: "environment variables"},
		{Label: "size", Old: "1", New: "2"},
	})
	if got == "" {
		t.Fatal("expected a non-empty change list")
	}
}

// TestResolveReconfigPromptAError verifies that a prompt selection error is
// swallowed without applying.
func TestResolveReconfigPromptAError(t *testing.T) {
	plan := &Plan{Recreate: true}
	ui := &termio.Mock{}
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) {
		return "", errors.New("select boom")
	}
	applyRecreate, _, err := ResolveReconfig(context.Background(), ui, plan, 1, plan.Changes)
	if err != nil {
		t.Errorf("expected select error to be swallowed, got %v", err)
	}
	if applyRecreate {
		t.Error("expected no recreate when the select errored")
	}
}

// TestResolveReconfigPromptADefer verifies that selecting keep defers the change.
func TestResolveReconfigPromptADefer(t *testing.T) {
	plan := &Plan{Recreate: true}
	ui := &termio.Mock{}
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) { return keepKey, nil }
	applyRecreate, _, err := ResolveReconfig(context.Background(), ui, plan, 1, plan.Changes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applyRecreate {
		t.Error("expected no recreate when keep is selected")
	}
}

// TestResolveReconfigPromptBError verifies that a PromptB select error is
// swallowed without applying a restart.
func TestResolveReconfigPromptBError(t *testing.T) {
	plan := &Plan{RestartDaemons: true}
	ui := &termio.Mock{}
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) {
		return "", errors.New("select boom")
	}
	_, applyRestart, err := ResolveReconfig(context.Background(), ui, plan, 1, plan.Changes)
	if err != nil {
		t.Errorf("expected select error to be swallowed, got %v", err)
	}
	if applyRestart {
		t.Error("expected no restart when the select errored")
	}
}

// TestResolveReconfigRestartAlone verifies the silent restart path with no
// other clients attached.
func TestResolveReconfigRestartAlone(t *testing.T) {
	plan := &Plan{RestartDaemons: true}
	ui := &termio.Mock{}
	applyRecreate, applyRestart, err := ResolveReconfig(context.Background(), ui, plan, 0, plan.Changes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applyRecreate {
		t.Error("expected no recreate")
	}
	if !applyRestart {
		t.Error("expected restart when alone")
	}
	if len(ui.InfoCalls) == 0 {
		t.Error("expected an informational line when restarting silently")
	}
}

// TestResolveReconfigNoAction verifies the no-op branch when the plan has no
// recreate or restart requirement.
func TestResolveReconfigNoAction(t *testing.T) {
	ui := &termio.Mock{}
	applyRecreate, applyRestart, err := ResolveReconfig(context.Background(), ui, &Plan{}, 1, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applyRecreate || applyRestart {
		t.Errorf("expected no actions, got recreate=%v restart=%v", applyRecreate, applyRestart)
	}
}

// TestDiskMiBOr0NilRootDisk verifies diskMiBOr0 returns 0 when RootDisk is nil.
func TestDiskMiBOr0NilRootDisk(t *testing.T) {
	if got := diskMiBOr0(&msbSdk.SandboxConfig{}); got != 0 {
		t.Errorf("expected 0 for nil RootDisk, got %d", got)
	}
}

// TestPlanReconfigDiskChangeWithNilRootDisk verifies the disk recreate trigger
// when RootDisk is nil (diskMiBOr0 nil branch) and exercises the disk change
// formatting.
func TestPlanReconfigDiskChangeWithNilRootDisk(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: "img"}
	plan := PlanReconfig(cfg, "img", options.RunOptions{DiskSize: "8G"}, ChangeFlags{}, "")
	if !plan.Recreate {
		t.Fatal("expected recreate on disk size change with nil RootDisk")
	}
	found := false
	for _, c := range plan.Changes {
		if c.Label == "root disk size" && c.Old == "0M" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected root disk size change with Old=0M, got %+v", plan.Changes)
	}
}

// TestPortBindingsEqualLengthMismatch verifies the length-mismatch branch.
func TestPortBindingsEqualLengthMismatch(t *testing.T) {
	a := []msbSdk.PortBinding{{Bind: "localhost"}}
	if portBindingsEqual(a, nil) {
		t.Error("expected false for differing lengths")
	}
}

// TestPortBindingsEqualElementMismatch verifies the element-mismatch branch for
// equal-length bindings.
func TestPortBindingsEqualElementMismatch(t *testing.T) {
	a := []msbSdk.PortBinding{{Bind: "localhost", HostPort: 1}}
	b := []msbSdk.PortBinding{{Bind: "localhost", HostPort: 2}}
	if portBindingsEqual(a, b) {
		t.Error("expected false for differing elements")
	}
}

// TestPortBindingsEqualMatch verifies the equal branch.
func TestPortBindingsEqualMatch(t *testing.T) {
	a := []msbSdk.PortBinding{{Bind: "localhost", HostPort: 1}}
	b := []msbSdk.PortBinding{{Bind: "localhost", HostPort: 1}}
	if !portBindingsEqual(a, b) {
		t.Error("expected true for matching bindings")
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

// TestParseSecretSpecLegacyEmptyKey verifies that a line with an empty (after
// trimming) key is skipped.
func TestParseSecretSpecLegacyEmptyKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.secret")
	testutil.WritePath(t, path, "=val@h.example\nFOO=bar@baz.example\n")
	testUI := termio.NewTestMock(t)
	specs := ParseSecretSpecLegacy(path, &testUI)
	if _, ok := specs["FOO"]; !ok {
		t.Error("expected FOO to be parsed")
	}
	if len(specs) != 1 {
		t.Errorf("expected only 1 spec (empty key skipped), got %d", len(specs))
	}
}
