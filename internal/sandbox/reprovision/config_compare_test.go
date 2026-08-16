package reprovision

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/testutil"
)

func TestConfigEqualHomeFileByteMatch(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		OpenCode:    []byte(`{"model":"x"}`),
		HomeFiles: map[string][]byte{
			"/home/dev/.gitconfig": []byte("user.name=X\n"),
		},
		Keys: []string{"/home/dev/.gitconfig"},
	}
	vmData := map[string][]byte{
		"/home/dev/.config/opencode/opencode.json": []byte(`{"model":"x"}`),
		"/home/dev/.gitconfig":                     []byte("user.name=X\n"),
	}
	if !ConfigEqual(cf, vmData) {
		t.Error("expected equality for matching opencode.json and home file")
	}
}

func TestConfigEqualHomeFileMismatch(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		OpenCode:    []byte(`{"model":"x"}`),
		HomeFiles: map[string][]byte{
			"/home/dev/.gitconfig": []byte("user.name=X\n"),
		},
		Keys: []string{"/home/dev/.gitconfig"},
	}
	vmData := map[string][]byte{
		"/home/dev/.config/opencode/opencode.json": []byte(`{"model":"x"}`),
		"/home/dev/.gitconfig":                     []byte("user.name=Y\n"),
	}
	if ConfigEqual(cf, vmData) {
		t.Error("expected mismatch for differing home file content")
	}
}

func TestOpenCodeConfigEqualIgnoresHomeFiles(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		OpenCode:    []byte(`{"model":"x"}`),
		HomeFiles: map[string][]byte{
			"/home/dev/.gitconfig": []byte("user.name=X\n"),
		},
		Keys: []string{"/home/dev/.gitconfig"},
	}
	vmData := map[string][]byte{
		"/home/dev/.config/opencode/opencode.json": []byte(`{"model":"x"}`),
		"/home/dev/.gitconfig":                     []byte("user.name=Y\n"),
	}
	if !OpenCodeConfigEqual(cf, vmData) {
		t.Error("expected opencode config equality despite differing home file content")
	}
}

func TestOpenCodeConfigEqualDetectsConfigChange(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		OpenCode:    []byte(`{"model":"x"}`),
		HomeFiles:   map[string][]byte{},
	}
	vmData := map[string][]byte{
		"/home/dev/.config/opencode/opencode.json": []byte(`{"model":"y"}`),
	}
	if OpenCodeConfigEqual(cf, vmData) {
		t.Error("expected mismatch when the opencode config differs")
	}
}

func TestOpenCodeConfigEqualNoSnippets(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: false,
		OpenCode:    nil,
		HomeFiles:   map[string][]byte{},
	}
	if !OpenCodeConfigEqual(cf, nil) {
		t.Error("expected equality when no snippets are configured")
	}
}

func TestProvisionWritesOpenCodeAndHomeFiles(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		OpenCode:    []byte("{\n  \"model\": \"x\"\n}\n"),
		HomeFiles: map[string][]byte{
			"/home/dev/.config/tool/cfg.toml": []byte("k=v\n"),
		},
	}
	fs := msb.NewTestFS(nil, nil)
	if err := Provision(context.Background(), fs, cf); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if fs.Writes[OpenCodeConfigPath(VMHomeDir)] == nil {
		t.Error("expected opencode.json to be written")
	}
	if fs.Writes["/home/dev/.config/tool/cfg.toml"] == nil {
		t.Error("expected home file to be written")
	}
}

func TestProvisionNoSnippetsSkipsOpenCode(t *testing.T) {
	cf := &ConfigFiles{OpenCode: nil, HomeFiles: map[string][]byte{}}
	fs := msb.NewTestFS(nil, nil)
	if err := Provision(context.Background(), fs, cf); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if _, ok := fs.Writes[OpenCodeConfigPath(VMHomeDir)]; ok {
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
		t.Errorf("expected 0 env vars when .opencode-sandbox/env missing, got %d", len(env))
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
				"/home/dev/.config/opencode/opencode.json": data,
				"/home/dev/.gitconfig":                     []byte("x=y\n"),
			},
			nil,
		),
	}
	want := map[string][]byte{
		"/home/dev/.config/opencode/opencode.json": data,
		"/home/dev/.gitconfig":                     []byte("x=y\n"),
	}
	got := ReadVMConfig(context.Background(), sb,
		[]string{"/home/dev/.config/opencode/opencode.json", "/home/dev/.gitconfig", "/home/dev/missing"},
		&termio.Mock{})
	if len(got) != 2 {
		t.Fatalf("expected 2 files read, got %d", len(got))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReadVMConfig = %v, want %v", got, want)
	}
}
