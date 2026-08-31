package reprovision

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/agent"
	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/homeconfig"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/termio"
	"github.com/inoio/opencode-sandbox/internal/testutil"
)

func TestLoadConfigFilesPopulatesHooks(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()

	testutil.WritePath(t, filepath.Join(cp.ProjectConfigDir(), "connect.sh"), "#!/bin/sh\n")
	testutil.WriteFile(t, cp.ProjectConfigDir(), "home.yaml",
		".vpn/connect.sh:\n  source: connect.sh\n  hook: startup\n  root: true\n")

	ui := termio.NewTestMock(t)
	a, _ := agent.Lookup("")
	cf, err := LoadConfigFiles(a, configpaths.Get().UserOpencodeConfigDir(), &ui)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	want := []homeconfig.HookSpec{
		{
			Target:      "/home/dev/.vpn/connect.sh",
			Source:      filepath.Join(cp.ProjectConfigDir(), "connect.sh"),
			Interpreter: "/bin/sh",
			Root:        true,
		},
	}
	if !reflect.DeepEqual(cf.Hooks, want) {
		t.Errorf("Hooks = %v, want %v", cf.Hooks, want)
	}
}

// TestProvisionChownsHomeFiles verifies that Provision writes the home files and
// the merged opencode config, then chowns every provisioned path to dev:dev so
// the files are readable by the runtime user.
func TestProvisionChownsHomeFiles(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		OpenCode:    []byte(`{"model":"x"}`),
		HomeFiles: map[string][]byte{
			"/home/dev/.gitconfig":            []byte("user.name=X\n"),
			"/home/dev/.config/tool/cfg.toml": []byte("k=v\n"),
		},
	}
	fs := msb.NewTestFS(nil, nil)
	var calls []string
	sb := &msb.MockSandbox{FSValue_: fs, ShellCalls: &calls}

	if err := Provision(context.Background(), sb, cf); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	for path := range cf.HomeFiles {
		if _, ok := fs.Writes[path]; !ok {
			t.Errorf("home file %s was not written", path)
		}
	}
	if _, ok := fs.Writes[OpenCodeConfigPath(VMHomeDir)]; !ok {
		t.Error("opencode config was not written")
	}

	if len(calls) != 1 {
		t.Fatalf("expected 1 chown shell call, got %d: %v", len(calls), calls)
	}
	wantChown := "chown -R dev:dev /home /home/dev /home/dev/.config /home/dev/.config/opencode " +
		"/home/dev/.config/opencode/opencode.json /home/dev/.config/tool /home/dev/.config/tool/cfg.toml " +
		"/home/dev/.gitconfig"
	if calls[0] != wantChown {
		t.Errorf("shell command = %q, want %q", calls[0], wantChown)
	}
}

// TestProvisionNoFilesSkipsChown verifies that Provision does not run a chown
// when there are no files to provision.
func TestProvisionNoFilesSkipsChown(t *testing.T) {
	var calls []string
	sb := &msb.MockSandbox{FSValue_: msb.NewTestFS(nil, nil), ShellCalls: &calls}
	if err := Provision(context.Background(), sb, &ConfigFiles{}); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if len(calls) != 0 {
		t.Errorf("expected no shell call, got %v", calls)
	}
}

// TestProvisionJoinsErrors verifies that when a write fails and the deferred
// chown also fails, both errors are surfaced via errors.Join.
func TestProvisionJoinsErrors(t *testing.T) {
	writeErr := errors.New("write boom")
	chownErr := errors.New("chown boom")
	cf := &ConfigFiles{HomeFiles: map[string][]byte{"/home/dev/.gitconfig": []byte("x\n")}}
	fs := msb.NewTestFS(nil, nil)
	fs.WriteErr = writeErr
	sb := &msb.MockSandbox{FSValue_: fs, ShellErr: chownErr}

	err := Provision(context.Background(), sb, cf)
	if !errors.Is(err, writeErr) {
		t.Errorf("Provision error does not wrap write error: %v", err)
	}
	if !errors.Is(err, chownErr) {
		t.Errorf("Provision error does not wrap chown error: %v", err)
	}
}

// TestLoadConfigFilesProvisioning verifies the default drop-in copy: host files
// under the agent's config dirs are provisioned into the VM home, while
// excluded paths (node_modules) are not.
func TestLoadConfigFilesProvisioning(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	hostHome := t.TempDir()
	vmHome := t.TempDir()
	ocConfig := filepath.Join(hostHome, ".config/opencode")
	if err := os.MkdirAll(filepath.Join(ocConfig, "node_modules", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, ocConfig, "opencode.json", `{"a":1}`)
	testutil.WriteFile(t, filepath.Join(ocConfig, "node_modules"), "x/index.js", "//x")

	a, _ := agent.Lookup("opencode")
	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(a, hostHome, vmHome, &ui)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	wantKey := filepath.Join(vmHome, ".config", "opencode", "opencode.json")
	if _, ok := cf.Provisioned[wantKey]; !ok {
		t.Errorf("expected opencode.json provisioned at %s, got %v", wantKey, cf.Provisioned)
	}
	for p := range cf.Provisioned {
		if strings.Contains(p, "node_modules") {
			t.Errorf("node_modules must not be provisioned, got %s", p)
		}
	}
}

// TestLoadConfigFilesProvisioningPrecedence verifies the precedence rules: a
// home-file key overrides a provisioned key at the same VM path, and when
// snippets exist the merged agent config wins over the provisioned default at
// its path (so the merged config path is absent from Provisioned).
func TestLoadConfigFilesProvisioningPrecedence(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	hostHome := t.TempDir()
	vmHome := t.TempDir()

	a, _ := agent.Lookup("opencode")

	// Snippets exist so hasSnippets=true and the merged config is written.
	testutil.WriteFile(t, cp.ProjectAgentConfigDir(a), "opencode-model.json", `{"model":"x"}`)

	// Host files that the drop-in copy would pick up.
	ocConfig := filepath.Join(hostHome, ".config/opencode")
	if err := os.MkdirAll(ocConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, ocConfig, "opencode.json", `{"a":1}`)
	testutil.WriteFile(t, ocConfig, "somefile.json", `{"host":1}`)

	// A home file at the same VM path as one of the provisioned files, which
	// must override it.
	project := cp.ProjectConfigDir()
	testutil.WriteFile(t, project, "somefile-home.json", `{"home":1}`)
	testutil.WriteFile(t, project, "home.yaml",
		".config/opencode/somefile.json:\n  source: somefile-home.json\n")

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(a, hostHome, vmHome, &ui)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}

	mergedPath := filepath.Join(vmHome, ".config", "opencode", "opencode.json")
	if _, ok := cf.Provisioned[mergedPath]; ok {
		t.Errorf(
			"merged config path %s must not be in Provisioned (merged config wins), got %v",
			mergedPath,
			cf.Provisioned,
		)
	}
	if !slices.Contains(cf.Keys, mergedPath) {
		t.Errorf("expected merged config path %s in Keys, got %v", mergedPath, cf.Keys)
	}

	homePath := filepath.Join(vmHome, ".config", "opencode", "somefile.json")
	if _, ok := cf.Provisioned[homePath]; ok {
		t.Errorf("home-file path %s must not be in Provisioned (home file wins), got %v", homePath, cf.Provisioned)
	}
	if !bytes.Equal(cf.HomeFiles[homePath], []byte(`{"home":1}`)) {
		t.Errorf("expected home file to override provisioned content at %s, got %q", homePath, cf.HomeFiles[homePath])
	}
}

// TestProvisionWritesProvisioned verifies that Provision writes the default
// drop-in copy just like home files.
func TestProvisionWritesProvisioned(t *testing.T) {
	cf := &ConfigFiles{
		Provisioned: map[string][]byte{
			"/home/dev/.config/opencode/opencode.json":  []byte(`{"a":1}`),
			"/home/dev/.local/share/opencode/auth.json": []byte(`{"t":"x"}`),
		},
	}
	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{FSValue_: fs, ShellCalls: &[]string{}}
	if err := Provision(context.Background(), sb, cf); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	for p := range cf.Provisioned {
		if _, ok := fs.Writes[p]; !ok {
			t.Errorf("provisioned file %s was not written", p)
		}
	}
}
