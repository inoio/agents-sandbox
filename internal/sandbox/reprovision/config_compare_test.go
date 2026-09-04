package reprovision

import (
	"bytes"
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"

	"github.com/inoio/agents-sandbox/internal/testutil"
)

// configEqual reports whether the desired state matches the VM state. The
// merged opencode.json is compared semantically; home files byte-for-byte.
func configEqual(cf *ConfigFiles, vmData map[string][]byte) bool {
	if cf.HasSnippets {
		ocPath := AgentConfigPath(opencodeTestAgent(), VMHomeDir)
		vm, ok := vmData[ocPath]
		if !ok {
			return false
		}
		if !jsonEqual(cf.Merged, vm) {
			return false
		}
	}
	for path, want := range cf.HomeFiles {
		got, ok := vmData[path]
		if !ok || !bytes.Equal(want, got) {
			return false
		}
	}
	return true
}

func TestConfigEqualHomeFileByteMatch(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		Merged:      []byte(`{"model":"x"}`),
		HomeFiles: map[string][]byte{
			"/home/dev/.gitconfig": []byte("user.name=X\n"),
		},
		Keys: []string{"/home/dev/.gitconfig"},
	}
	vmData := map[string][]byte{
		AgentConfigPath(opencodeTestAgent(), VMHomeDir): []byte(`{"model":"x"}`),
		"/home/dev/.gitconfig":                          []byte("user.name=X\n"),
	}
	if !configEqual(cf, vmData) {
		t.Error("expected equality for matching opencode.json and home file")
	}
}

func TestConfigEqualHomeFileMismatch(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		Merged:      []byte(`{"model":"x"}`),
		HomeFiles: map[string][]byte{
			"/home/dev/.gitconfig": []byte("user.name=X\n"),
		},
		Keys: []string{"/home/dev/.gitconfig"},
	}
	vmData := map[string][]byte{
		AgentConfigPath(opencodeTestAgent(), VMHomeDir): []byte(`{"model":"x"}`),
		"/home/dev/.gitconfig":                          []byte("user.name=Y\n"),
	}
	if configEqual(cf, vmData) {
		t.Error("expected mismatch for differing home file content")
	}
}

func TestAgentConfigEqualIgnoresHomeFiles(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		Merged:      []byte(`{"model":"x"}`),
		MergedPath:  AgentConfigPath(opencodeTestAgent(), VMHomeDir),
		HomeFiles: map[string][]byte{
			"/home/dev/.gitconfig": []byte("user.name=X\n"),
		},
		Keys: []string{"/home/dev/.gitconfig"},
	}
	vmData := map[string][]byte{
		AgentConfigPath(opencodeTestAgent(), VMHomeDir): []byte(`{"model":"x"}`),
		"/home/dev/.gitconfig":                          []byte("user.name=Y\n"),
	}
	if !AgentConfigEqual(cf, vmData) {
		t.Error("expected opencode config equality despite differing home file content")
	}
}

func TestAgentConfigEqualDetectsConfigChange(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		Merged:      []byte(`{"model":"x"}`),
		MergedPath:  AgentConfigPath(opencodeTestAgent(), VMHomeDir),
		HomeFiles:   map[string][]byte{},
	}
	vmData := map[string][]byte{
		AgentConfigPath(opencodeTestAgent(), VMHomeDir): []byte(`{"model":"y"}`),
	}
	if AgentConfigEqual(cf, vmData) {
		t.Error("expected mismatch when the opencode config differs")
	}
}

func TestAgentConfigEqualNoSnippets(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: false,
		Merged:      nil,
		HomeFiles:   map[string][]byte{},
	}
	if !AgentConfigEqual(cf, nil) {
		t.Error("expected equality when no snippets are configured")
	}
}

func TestAgentConfigEqualMirrorMatch(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		Merged:      []byte(`{"model":"x"}`),
		MergedPath:  AgentConfigPath(opencodeTestAgent(), VMHomeDir),
		Mirror: map[string][]byte{
			"/home/dev/.config/opencode/tui.json": []byte(`{"theme":"dark"}`),
		},
	}
	vmData := map[string][]byte{
		AgentConfigPath(opencodeTestAgent(), VMHomeDir): []byte(`{"model":"x"}`),
		"/home/dev/.config/opencode/tui.json":           []byte(`{"theme":"dark"}`),
	}
	if !AgentConfigEqual(cf, vmData) {
		t.Error("expected equality when merged config and mirror match")
	}
}

func TestAgentConfigEqualMirrorMismatch(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		Merged:      []byte(`{"model":"x"}`),
		MergedPath:  AgentConfigPath(opencodeTestAgent(), VMHomeDir),
		Mirror: map[string][]byte{
			"/home/dev/.config/opencode/tui.json": []byte(`{"theme":"dark"}`),
		},
	}
	vmData := map[string][]byte{
		AgentConfigPath(opencodeTestAgent(), VMHomeDir): []byte(`{"model":"x"}`),
		"/home/dev/.config/opencode/tui.json":           []byte(`{"theme":"light"}`),
	}
	if AgentConfigEqual(cf, vmData) {
		t.Error("expected mismatch when a mirror file differs")
	}
}

func TestAgentConfigEqualMirrorNoSnippets(t *testing.T) {
	cf := &ConfigFiles{
		Mirror: map[string][]byte{
			"/home/dev/.config/opencode/tui.json": []byte(`{"theme":"dark"}`),
		},
	}
	match := map[string][]byte{"/home/dev/.config/opencode/tui.json": []byte(`{"theme":"dark"}`)}
	if !AgentConfigEqual(cf, match) {
		t.Error("expected equality when mirror matches without snippets")
	}
	if AgentConfigEqual(cf, nil) {
		t.Error("expected mismatch when the VM mirror is missing")
	}
}

func TestProvisionWritesOpenCodeAndHomeFiles(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		Merged:      []byte("{\n  \"model\": \"x\"\n}\n"),
		MergedPath:  AgentConfigPath(opencodeTestAgent(), VMHomeDir),
		HomeFiles: map[string][]byte{
			"/home/dev/.config/tool/cfg.toml": []byte("k=v\n"),
		},
	}
	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{FSValue_: fs}
	if err := Provision(context.Background(), sb, cf); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if fs.Writes[AgentConfigPath(opencodeTestAgent(), VMHomeDir)] == nil {
		t.Error("expected opencode.json to be written")
	}
	if fs.Writes["/home/dev/.config/tool/cfg.toml"] == nil {
		t.Error("expected home file to be written")
	}
}

func TestProvisionNoSnippetsSkipsOpenCode(t *testing.T) {
	cf := &ConfigFiles{Merged: nil, HomeFiles: map[string][]byte{}}
	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{FSValue_: fs}
	if err := Provision(context.Background(), sb, cf); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if _, ok := fs.Writes[AgentConfigPath(opencodeTestAgent(), VMHomeDir)]; ok {
		t.Error("did not expect opencode.json when HasSnippets=false")
	}
}

func TestBuildEnvMap(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), "env")
	testutil.WritePath(t, envFile, "FOO=bar\n# comment\n\nBAZ=qux\n")
	got := BuildEnvMap(envFile)

	if len(got) != 2 {
		t.Fatalf("expected 2 env vars, got %d: %v", len(got), got)
	}
	if got["FOO"] != "bar" {
		t.Errorf("expected FOO=bar, got %q", got["FOO"])
	}
	if got["BAZ"] != "qux" {
		t.Errorf("expected BAZ=qux, got %q", got["BAZ"])
	}
}

func TestReadSandboxEnvMissing(t *testing.T) {
	env := BuildEnvMap("missing")
	if len(env) != 0 {
		t.Errorf("expected 0 env vars when .agents-sandbox/env missing, got %d", len(env))
	}
}

func TestMergeEnvMapsProjectOverridesUser(t *testing.T) {
	userFile := filepath.Join(t.TempDir(), "env")
	testutil.WritePath(t, userFile, "FOO=user\nBAR=user\n")
	projectFile := filepath.Join(t.TempDir(), "env")
	testutil.WritePath(t, projectFile, "FOO=project\n")

	got := MergeEnvMaps(BuildEnvMap(userFile), BuildEnvMap(projectFile))
	want := map[string]string{"FOO": "project", "BAR": "user"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestReadVMConfigReadsPaths(t *testing.T) {
	data := []byte("{}")
	sb := &msb.MockSandbox{
		Name_: "test-vm",
		FSValue_: msb.NewTestFS(
			map[string][]byte{
				AgentConfigPath(opencodeTestAgent(), VMHomeDir): data,
				"/home/dev/.gitconfig":                          []byte("x=y\n"),
			},
			nil,
		),
	}
	want := map[string][]byte{
		AgentConfigPath(opencodeTestAgent(), VMHomeDir): data,
		"/home/dev/.gitconfig":                          []byte("x=y\n"),
	}
	got := ReadVMConfig(context.Background(), sb,
		[]string{AgentConfigPath(opencodeTestAgent(), VMHomeDir), "/home/dev/.gitconfig", "/home/dev/missing"},
		&termio.Mock{})
	if len(got) != 2 {
		t.Fatalf("expected 2 files read, got %d", len(got))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReadVMConfig = %v, want %v", got, want)
	}
}
