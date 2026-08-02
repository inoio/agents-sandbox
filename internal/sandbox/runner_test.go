package sandbox

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"
	"gitlab.inoio.de/inoio/opencode-msb/internal/testhelpers"
)

func TestConfigEqualIgnoresExtraFilesOnVM(t *testing.T) {
	// BuildMergedConfig produces only opencode.jsonc, but the VM has
	// extra pre-installed npm files from the Docker image.
	// configEqual should only compare the expected JSON files.
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

	got := configEqual(goSide, keys, vmData)
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

	got := configEqual(goSide, goSideKeys, vmData)
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

	got := configEqual(goSide, goSideKeys, vmData)
	if got {
		t.Error("expected mismatch: VM missing expected file")
	}
}

func TestConfigEqualNonJSONByteMatch(t *testing.T) {
	goSideKeys := []string{".gitignore"}
	goSide := map[string]map[string]any{
		".gitignore": nil, // not a JSON file
	}
	vmData := map[string][]byte{
		".gitignore": []byte("node_modules/\n"),
	}

	got := configEqual(goSide, goSideKeys, vmData)
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

	got := configEqual(goSide, goSideKeys, vmData)
	if got {
		t.Error("expected mismatch: different file keys")
	}
}

func TestConfigEqualJSONEquivalent(t *testing.T) {
	// Different JSON representations (key order) should be semantically equal.
	goSideKeys := []string{"opencode.jsonc"}
	goSide := map[string]map[string]any{
		"opencode.jsonc": {"a": 1, "b": "hello", "c": []any{1, 2}},
	}
	vmData := map[string][]byte{
		"opencode.jsonc": []byte(`{"c":[1,2],"a":1,"b":"hello"}`),
	}

	got := configEqual(goSide, goSideKeys, vmData)
	if !got {
		t.Error("expected equality: semantically equivalent JSON despite key order")
	}
}

func TestEqualJSONFilesEmptyMaps(t *testing.T) {
	goSide := map[string]map[string]any{}
	vmData := map[string][]byte{}
	if !equalJSONFiles(goSide, nil, vmData) {
		t.Error("expected equality for empty maps")
	}
}

func TestParseMemoryGigabytes(t *testing.T) {
	got := parseMemory("4G")
	if got != 4096 {
		t.Errorf("expected 4096, got %d", got)
	}
}

func TestParseMemoryMegabytes(t *testing.T) {
	got := parseMemory("512M")
	if got != 512 {
		t.Errorf("expected 512, got %d", got)
	}
}

func TestParseMemoryPlainNumber(t *testing.T) {
	got := parseMemory("2048")
	if got != 2048 {
		t.Errorf("expected 2048, got %d", got)
	}
}

func TestParseMemoryLowercase(t *testing.T) {
	got := parseMemory("2g")
	if got != 2048 {
		t.Errorf("expected 2048, got %d", got)
	}
}

func TestResolveTmpSizeDefaultsWhenEmpty(t *testing.T) {
	got := resolveTmpSizeMiB("")
	if got != defaultTmpSizeMiB {
		t.Errorf("expected default %d, got %d", defaultTmpSizeMiB, got)
	}
}

func TestResolveTmpSizeParsesSpec(t *testing.T) {
	got := resolveTmpSizeMiB("4G")
	if got != 4096 {
		t.Errorf("expected 4096, got %d", got)
	}
}

func TestBuildMountsIncludesTmpfsAtTmp(t *testing.T) {
	mounts := buildMounts("test-home-vol", "/repo/path", defaultTmpSizeMiB)

	tmpMount, ok := mounts["/tmp"]
	if !ok {
		t.Fatal("expected /tmp mount, not found in mounts map")
	}
	if tmpMount.Kind() != msb.MountKindTmpfs {
		t.Errorf("expected /tmp to be a tmpfs mount, got kind %d", tmpMount.Kind())
	}
	if tmpMount.SizeMiB == 0 {
		t.Error("expected /tmp tmpfs to have a nonzero size cap")
	}
	if tmpMount.SizeMiB < 1024 {
		t.Errorf("expected /tmp tmpfs to be at least 1 GiB, got %d MiB", tmpMount.SizeMiB)
	}
}

func TestBuildMountsRespectsCustomTmpSize(t *testing.T) {
	mounts := buildMounts("test-home-vol", "/repo/path", 4096)

	tmpMount := mounts["/tmp"]
	if tmpMount.SizeMiB != 4096 {
		t.Errorf("expected /tmp tmpfs size 4096 MiB, got %d", tmpMount.SizeMiB)
	}
}

func TestBuildEnvMap(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), "env")
	testhelpers.WritePath(t, envFile, "FOO=bar\n# comment\n\nBAZ=qux\n")
	got := buildEnvMap(envFile)

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
	env := buildEnvMap("missing")
	if len(env) != 0 {
		t.Errorf("expected 0 env vars when .opencode-msb/env missing, got %d", len(env))
	}
}

func TestBuildOpencodeArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		auto bool
		want []string
	}{
		{"auto default", nil, true, []string{autoFlag}},
		{"auto with forwarded args", []string{"foo", "bar"}, true, []string{autoFlag, "foo", "bar"}},
		{"no-auto", []string{"foo"}, false, []string{"foo"}},
		{"no-auto empty args", nil, false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildOpencodeArgs(tt.args, tt.auto)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildOpencodeArgs(%v, %v) = %v, want %v", tt.args, tt.auto, got, tt.want)
			}
		})
	}
}

func TestIsSandboxActive(t *testing.T) {
	tests := []struct {
		name   string
		status msb.SandboxStatus
		want   bool
	}{
		{"running", msb.SandboxStatusRunning, true},
		{"draining", msb.SandboxStatusDraining, true},
		{"paused", msb.SandboxStatusPaused, true},
		{"stopped", msb.SandboxStatusStopped, false},
		{"crashed", msb.SandboxStatusCrashed, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSandboxActive(tt.status); got != tt.want {
				t.Errorf("isSandboxActive(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestMergeEnvMapsProjectOverridesUser(t *testing.T) {
	userFile := filepath.Join(t.TempDir(), "env")
	testhelpers.WritePath(t, userFile, "FOO=user\nBAR=user\n")
	projectFile := filepath.Join(t.TempDir(), "env")
	testhelpers.WritePath(t, projectFile, "FOO=project\n")

	got := mergeEnvMaps(buildEnvMap(userFile), buildEnvMap(projectFile))
	want := map[string]string{"FOO": "project", "BAR": "user"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBuildAttachCommand(t *testing.T) {
	got := buildAttachCommand("/workspace", true, []string{"foo"})
	if !strings.Contains(got, "opencode attach") {
		t.Errorf("expected 'opencode attach' in command, got %q", got)
	}
	if !strings.Contains(got, "http://127.0.0.1:4096") {
		t.Errorf("expected daemon URL in command, got %q", got)
	}
	if !strings.Contains(got, "--dir /workspace") {
		t.Errorf("expected --dir /workspace in command, got %q", got)
	}
	if strings.Contains(got, "--auto") {
		t.Errorf("did not expect --auto flag (removed by human), got %q", got)
	}
	if !strings.Contains(got, "foo") {
		t.Errorf("expected forwarded args in command, got %q", got)
	}
}

func TestBuildAttachCommandNoAuto(t *testing.T) {
	got := buildAttachCommand("/workspace", false, nil)
	if strings.Contains(got, "--auto") {
		t.Errorf("did not expect --auto flag, got %q", got)
	}
}

func TestBuildAttachCommandWorktreeTarget(t *testing.T) {
	got := buildAttachCommand("/home/dev/.local/share/opencode/worktree/abc/feat", true, nil)
	if !strings.Contains(got, "--dir /home/dev/.local/share/opencode/worktree/abc/feat") {
		t.Errorf("expected worktree dir in command, got %q", got)
	}
}

func TestReadVMFilesUsesSDKFs(t *testing.T) {
	data := []byte("test-config-data")
	gitignore := []byte("node_modules/\n")
	sb := &mockSandbox{
		name: "test-vm",
		fsValue: &mockFs{
			files: map[string][]byte{
				"/home/dev/.config/opencode/thing.json": data,
				"/home/dev/.config/opencode/.gitignore": gitignore,
			},
			list: []msb.FsEntry{
				{Path: "/home/dev/.config/opencode/thing.json", Kind: msb.FsEntryKindFile},
				{Path: "/home/dev/.config/opencode/.gitignore", Kind: msb.FsEntryKindFile},
			},
		},
	}
	want := map[string][]byte{
		"thing.json": data,
		".gitignore": gitignore,
	}
	got := readVMFiles(context.Background(), sb, "/home/dev/.config/opencode", &stdio.Mock{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("readVMFiles(%q) = %v, want %v", "/home/dev/.config/opencode", got, want)
	}
}

func TestReadVMFilesSkipsDirectories(t *testing.T) {
	sb := &mockSandbox{
		name: "test-vm",
		fsValue: &mockFs{
			files: map[string][]byte{
				"/home/dev/.config/opencode/file.txt": []byte("hello"),
			},
			list: []msb.FsEntry{
				{Path: "/home/dev/.config/opencode/file.txt", Kind: msb.FsEntryKindFile},
				{Path: "/home/dev/.config/opencode/dir1", Kind: msb.FsEntryKindDirectory},
				{Path: "/home/dev/.config/opencode/dir2", Kind: msb.FsEntryKindDirectory},
			},
		},
	}
	want := map[string][]byte{
		"file.txt": []byte("hello"),
	}
	got := readVMFiles(context.Background(), sb, "/home/dev/.config/opencode", &stdio.Mock{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("readVMFiles(%q) = %v, want %v", "/home/dev/.config/opencode", got, want)
	}
}

func TestReadVMFilesEmptyDir(t *testing.T) {
	sb := &mockSandbox{
		name: "test-vm",
		fsValue: &mockFs{
			list: nil,
		},
	}
	got := readVMFiles(context.Background(), sb, "/home/dev/.config/opencode", &stdio.Mock{})
	if len(got) != 0 {
		t.Errorf("expected empty result for empty dir, got %v", got)
	}
}
