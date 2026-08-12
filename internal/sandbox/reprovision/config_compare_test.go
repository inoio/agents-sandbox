package reprovision

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"

	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
)

func TestConfigEqualIgnoresExtraFilesOnVM(t *testing.T) {
	vmData := map[string][]byte{
		"opencode.jsonc":    []byte(`{"provider":{"litellm":{"name":"LiteLLM"}}}`),
		".gitignore":        []byte("node_modules/\n"),
		"package.json":      []byte(`{"name":"opencode"}`),
		"package-lock.json": []byte(`{"lockfileVersion":2}`),
	}
	goSide := map[string]map[string]any{
		"opencode.jsonc": {"provider": map[string]any{"litellm": map[string]any{"name": "LiteLLM"}}},
	}
	keys := []string{"opencode.jsonc"}

	got := ConfigEqual(goSide, keys, vmData)
	if !got {
		t.Error("expected equality: extra VM files should be ignored")
	}
}

func TestConfigEqualJSONMismatch(t *testing.T) {
	goSideKeys := []string{"opencode.jsonc"}
	goSide := map[string]map[string]any{
		"opencode.jsonc": {"provider": map[string]any{"litellm": map[string]any{"name": "LiteLLM"}}},
	}
	vmData := map[string][]byte{
		"opencode.jsonc": []byte(`{"provider":{"litellm":{"name":"other"}}}`),
	}

	got := ConfigEqual(goSide, goSideKeys, vmData)
	if got {
		t.Error("expected mismatch: different provider names")
	}
}

func TestConfigEqualVMKeyMissing(t *testing.T) {
	goSideKeys := []string{"opencode.jsonc"}
	goSide := map[string]map[string]any{
		"opencode.jsonc": {"provider": map[string]any{"litellm": map[string]any{"name": "LiteLLM"}}},
	}
	vmData := map[string][]byte{}

	got := ConfigEqual(goSide, goSideKeys, vmData)
	if got {
		t.Error("expected mismatch: VM missing expected file")
	}
}

func TestConfigEqualNonJSONByteMatch(t *testing.T) {
	goSideKeys := []string{".gitignore"}
	goSide := map[string]map[string]any{
		".gitignore": nil,
	}
	vmData := map[string][]byte{
		".gitignore": []byte("node_modules/\n"),
	}

	got := ConfigEqual(goSide, goSideKeys, vmData)
	if !got {
		t.Error("expected equality: non-JSON files should match byte-for-byte")
	}
}

func TestConfigEqualKeyMismatch(t *testing.T) {
	goSideKeys := []string{"opencode.jsonc", ".gitignore"}
	goSide := map[string]map[string]any{
		"opencode.jsonc": {},
		".gitignore":     nil,
	}
	vmData := map[string][]byte{
		"opencode.jsonc": []byte(`{}`),
	}

	got := ConfigEqual(goSide, goSideKeys, vmData)
	if got {
		t.Error("expected mismatch: different file keys")
	}
}

func TestConfigEqualJSONEquivalent(t *testing.T) {
	goSideKeys := []string{"opencode.jsonc"}
	goSide := map[string]map[string]any{
		"opencode.jsonc": {"a": 1, "b": "hello", "c": []any{1, 2}},
	}
	vmData := map[string][]byte{
		"opencode.jsonc": []byte(`{"c":[1,2],"a":1,"b":"hello"}`),
	}

	got := ConfigEqual(goSide, goSideKeys, vmData)
	if !got {
		t.Error("expected equality: semantically equivalent JSON despite key order")
	}
}

func TestEqualJSONFilesEmptyMaps(t *testing.T) {
	goSide := map[string]map[string]any{}
	vmData := map[string][]byte{}
	if !EqualJSONFiles(goSide, nil, vmData) {
		t.Error("expected equality for empty maps")
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
		t.Errorf("expected 0 env vars when .opencode-msb/env missing, got %d", len(env))
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

func TestReadVMFilesUsesSDKFs(t *testing.T) {
	data := []byte("test-config-data")
	gitignore := []byte("node_modules/\n")
	sb := &msb.MockSandbox{
		Name_: "test-vm",
		FSValue_: msb.NewTestFS(
			map[string][]byte{
				"/home/dev/.config/opencode/thing.json": data,
				"/home/dev/.config/opencode/.gitignore": gitignore,
			},
			[]msbSdk.FsEntry{
				{Path: "/home/dev/.config/opencode/thing.json", Kind: msbSdk.FsEntryKindFile},
				{Path: "/home/dev/.config/opencode/.gitignore", Kind: msbSdk.FsEntryKindFile},
			},
		),
	}
	want := map[string][]byte{
		"thing.json": data,
		".gitignore": gitignore,
	}
	got := ReadVMFiles(context.Background(), sb, "/home/dev/.config/opencode", &termio.Mock{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReadVMFiles(%q) = %v, want %v", "/home/dev/.config/opencode", got, want)
	}
}

func TestReadVMFilesSkipsDirectories(t *testing.T) {
	sb := &msb.MockSandbox{
		Name_: "test-vm",
		FSValue_: msb.NewTestFS(
			map[string][]byte{
				"/home/dev/.config/opencode/file.txt": []byte("hello"),
			},
			[]msbSdk.FsEntry{
				{Path: "/home/dev/.config/opencode/file.txt", Kind: msbSdk.FsEntryKindFile},
				{Path: "/home/dev/.config/opencode/dir1", Kind: msbSdk.FsEntryKindDirectory},
				{Path: "/home/dev/.config/opencode/dir2", Kind: msbSdk.FsEntryKindDirectory},
			},
		),
	}
	want := map[string][]byte{
		"file.txt": []byte("hello"),
	}
	got := ReadVMFiles(context.Background(), sb, "/home/dev/.config/opencode", &termio.Mock{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReadVMFiles(%q) = %v, want %v", "/home/dev/.config/opencode", got, want)
	}
}

func TestReadVMFilesEmptyDir(t *testing.T) {
	sb := &msb.MockSandbox{
		Name_:    "test-vm",
		FSValue_: msb.NewTestFS(nil, nil),
	}
	got := ReadVMFiles(context.Background(), sb, "/home/dev/.config/opencode", &termio.Mock{})
	if len(got) != 0 {
		t.Errorf("expected empty result for empty dir, got %v", got)
	}
}
