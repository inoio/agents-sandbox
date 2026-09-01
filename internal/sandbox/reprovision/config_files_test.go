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
	cf, err := LoadConfigFiles(a, configpaths.Get().UserOpencodeConfigDir(), &ui, true)
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
		"/home/dev/.config/opencode/opencode.jsonc /home/dev/.config/tool /home/dev/.config/tool/cfg.toml " +
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
	cf, err := LoadConfigFilesForHost(a, hostHome, vmHome, &ui, true)
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
	cf, err := LoadConfigFilesForHost(a, hostHome, vmHome, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}

	mergedPath := filepath.Join(vmHome, ".config", "opencode", "opencode.jsonc")
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

// TestLoadConfigFilesRemovesStaleConfigWithSnippets verifies that when snippets
// produce a merged config, the config-file family is excluded from the drop-in
// copy and marked for removal so host config cannot shadow the merged config,
// while unrelated host config files are still provisioned.
func TestLoadConfigFilesRemovesStaleConfigWithSnippets(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	hostHome := t.TempDir()
	vmHome := t.TempDir()

	a, _ := agent.Lookup("opencode")

	// Snippets exist so hasSnippets=true and the merged config is written.
	testutil.WriteFile(t, cp.ProjectAgentConfigDir(a), "opencode-model.json", `{"model":"x"}`)

	// Host files the drop-in copy would pick up: opencode.jsonc is the merged
	// config filename, other.json is unrelated.
	ocConfig := filepath.Join(hostHome, ".config/opencode")
	if err := os.MkdirAll(ocConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, ocConfig, "opencode.jsonc", `{"model":"host"}`)
	testutil.WriteFile(t, ocConfig, "other.json", `{"host":1}`)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(a, hostHome, vmHome, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}

	mergedPath := filepath.Join(vmHome, ".config", "opencode", "opencode.jsonc")
	for _, name := range opencodeConfigFileNames() {
		want := filepath.Join(vmHome, ".config", "opencode", name)
		if !slices.Contains(cf.Remove, want) {
			t.Errorf("expected config file %s in Remove, got %v", want, cf.Remove)
		}
		if _, ok := cf.Provisioned[want]; ok {
			t.Errorf("config file %s must not be in Provisioned when snippets exist, got %v", want, cf.Provisioned)
		}
	}
	if _, ok := cf.Provisioned[mergedPath]; ok {
		t.Errorf(
			"merged config path %s must not be in Provisioned (merged config wins), got %v",
			mergedPath,
			cf.Provisioned,
		)
	}
	otherPath := filepath.Join(vmHome, ".config", "opencode", "other.json")
	if _, ok := cf.Provisioned[otherPath]; !ok {
		t.Errorf("unrelated host config %s should still be provisioned, got %v", otherPath, cf.Provisioned)
	}
}

// TestLoadConfigFilesShadowingCopiedWithoutSnippets verifies that without
// snippets the host opencode.jsonc is drop-in copied as the default config.
func TestLoadConfigFilesShadowingCopiedWithoutSnippets(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	hostHome := t.TempDir()
	vmHome := t.TempDir()

	a, _ := agent.Lookup("opencode")

	ocConfig := filepath.Join(hostHome, ".config/opencode")
	if err := os.MkdirAll(ocConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, ocConfig, "opencode.jsonc", `{"model":"host"}`)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(a, hostHome, vmHome, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	if cf.HasSnippets {
		t.Fatal("expected HasSnippets=false without snippets")
	}

	shadowPath := filepath.Join(vmHome, ".config", "opencode", "opencode.jsonc")
	if _, ok := cf.Provisioned[shadowPath]; !ok {
		t.Errorf("expected host %s provisioned when no snippets exist, got %v", shadowPath, cf.Provisioned)
	}
}

// TestLoadConfigFilesProvisioningDisabled verifies that with host config
// provisioning disabled the drop-in copy is skipped entirely, the config-file
// family and auth.json are marked for removal, and snippet merging still works.
func TestLoadConfigFilesProvisioningDisabled(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	hostHome := t.TempDir()
	vmHome := t.TempDir()

	a, _ := agent.Lookup("opencode")

	testutil.WriteFile(t, cp.ProjectAgentConfigDir(a), "opencode-model.json", `{"model":"x"}`)
	ocConfig := filepath.Join(hostHome, ".config/opencode")
	if err := os.MkdirAll(ocConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, ocConfig, "opencode.json", `{"a":1}`)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(a, hostHome, vmHome, &ui, false)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}

	if len(cf.Provisioned) != 0 {
		t.Errorf("expected no drop-in copy when provisioning disabled, got %v", cf.Provisioned)
	}
	for _, name := range opencodeConfigFileNames() {
		want := filepath.Join(vmHome, ".config", "opencode", name)
		if !slices.Contains(cf.Remove, want) {
			t.Errorf("expected config file %s in Remove, got %v", want, cf.Remove)
		}
	}
	authPath := filepath.Join(vmHome, ".local", "share", "opencode", "auth.json")
	if !slices.Contains(cf.Remove, authPath) {
		t.Errorf("expected auth.json in Remove when provisioning disabled, got %v", cf.Remove)
	}
	if !cf.HasSnippets || len(cf.OpenCode) == 0 {
		t.Error("expected merged config to be built despite disabled provisioning")
	}
}

// TestProvisionRemovesStalePaths verifies that Provision removes the marked
// stale paths before writing the merged config.
func TestProvisionRemovesStalePaths(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		OpenCode:    []byte(`{"model":"x"}`),
		Remove:      configFileFamilyPaths(OpenCodeConfigPath(VMHomeDir)),
	}
	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{FSValue_: fs, ShellCalls: &[]string{}}
	if err := Provision(context.Background(), sb, cf); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if len(fs.Removed) != len(cf.Remove) {
		t.Fatalf("expected %d removals, got %v", len(cf.Remove), fs.Removed)
	}
	for i, p := range cf.Remove {
		if i >= len(fs.Removed) || fs.Removed[i] != p {
			t.Errorf("Remove order = %v, want %v", fs.Removed, cf.Remove)
			break
		}
	}
	if fs.Writes[OpenCodeConfigPath(VMHomeDir)] == nil {
		t.Error("expected merged config written after removal")
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
