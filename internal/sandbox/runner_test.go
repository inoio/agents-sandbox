package sandbox

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/options"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"

	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
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

// parseMemory and resolveTmpSizeMiB tests moved to internal/sandbox/options/options_test.go.

func TestBuildMountsIncludesTmpfsAtTmp(t *testing.T) {
	mounts := buildMounts("test-home-vol", "/repo/path", options.DefaultTmpSizeMiB)

	tmpMount, ok := mounts[tmpMountPath]
	if !ok {
		t.Fatal("expected /tmp mount, not found in mounts map")
	}
	if tmpMount.Kind() != msbSdk.MountKindTmpfs {
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

	tmpMount := mounts[tmpMountPath]
	if tmpMount.SizeMiB != 4096 {
		t.Errorf("expected /tmp tmpfs size 4096 MiB, got %d", tmpMount.SizeMiB)
	}
}

func TestBuildEnvMap(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), "env")
	testutil.WritePath(t, envFile, "FOO=bar\n# comment\n\nBAZ=qux\n")
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
		{"auto default", nil, true, []string{options.AutoFlag}},
		{"auto with forwarded args", []string{"foo", "bar"}, true, []string{options.AutoFlag, "foo", "bar"}},
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
		status msbSdk.SandboxStatus
		want   bool
	}{
		{"running", msbSdk.SandboxStatusRunning, true},
		{"draining", msbSdk.SandboxStatusDraining, true},
		{"paused", msbSdk.SandboxStatusPaused, true},
		{"stopped", msbSdk.SandboxStatusStopped, false},
		{"crashed", msbSdk.SandboxStatusCrashed, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := msb.IsSandboxActive(tt.status); got != tt.want {
				t.Errorf("IsSandboxActive(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestMergeEnvMapsProjectOverridesUser(t *testing.T) {
	userFile := filepath.Join(t.TempDir(), "env")
	testutil.WritePath(t, userFile, "FOO=user\nBAR=user\n")
	projectFile := filepath.Join(t.TempDir(), "env")
	testutil.WritePath(t, projectFile, "FOO=project\n")

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

func TestSetUpSandboxProvisionsConfigOnFreshSetup(t *testing.T) {
	setUpSandboxProvisionsConfig(t, true, "fresh setup")
}

func TestSetUpSandboxProvisionsConfigOnReuseWithEmptyDir(t *testing.T) {
	setUpSandboxProvisionsConfig(t, false, "reused VM with empty config dir")
}

func setUpSandboxProvisionsConfig(t *testing.T, created bool, provisionMsg string) {
	t.Helper()
	origDaemon := SetDaemonShellFunc(func(_ context.Context, _ Sandbox, command string) (string, int, error) {
		if command == "curl -sfm2 "+daemonHealthURL {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	defer SetDaemonShellFunc(origDaemon)

	fs := newTestFS(nil, nil) // empty FS simulates a VM with empty config dir
	sb := &MockSandbox{Name_: "test-vm", FSValue_: fs}
	WithMockConfigPaths(t)

	ui := &termio.Mock{}
	target, err := setUpSandbox(
		context.Background(),
		sb,
		RunOptions{},
		created,
		ui,
		false,
	)
	if err != nil {
		t.Fatalf("setUpSandbox: %v", err)
	}
	if target != defaultTargetDir {
		t.Errorf("target = %q, want %q", target, defaultTargetDir)
	}

	wroteConfig := fs.Writes != nil && fs.Writes["/home/dev/.config/opencode/opencode.jsonc"] != nil
	if !wroteConfig {
		t.Errorf(
			"expected config to be provisioned on %s, but opencode.jsonc was never written",
			provisionMsg,
		)
	}
}

func TestReadVMFilesUsesSDKFs(t *testing.T) {
	data := []byte("test-config-data")
	gitignore := []byte("node_modules/\n")
	sb := &MockSandbox{
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
	got := readVMFiles(context.Background(), sb, "/home/dev/.config/opencode", &termio.Mock{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("readVMFiles(%q) = %v, want %v", "/home/dev/.config/opencode", got, want)
	}
}

func TestReadVMFilesSkipsDirectories(t *testing.T) {
	sb := &MockSandbox{
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
	got := readVMFiles(context.Background(), sb, "/home/dev/.config/opencode", &termio.Mock{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("readVMFiles(%q) = %v, want %v", "/home/dev/.config/opencode", got, want)
	}
}

func TestReadVMFilesEmptyDir(t *testing.T) {
	sb := &MockSandbox{
		Name_:    "test-vm",
		FSValue_: msb.NewTestFS(nil, nil),
	}
	got := readVMFiles(context.Background(), sb, "/home/dev/.config/opencode", &termio.Mock{})
	if len(got) != 0 {
		t.Errorf("expected empty result for empty dir, got %v", got)
	}
}

func TestRestartDaemonsRestartsServe(t *testing.T) {
	var cmdCalls []string
	savedShell := SetDaemonShellFunc(func(_ context.Context, _ Sandbox, command string) (string, int, error) {
		cmdCalls = append(cmdCalls, command)
		if command == "curl -sfm2 "+daemonHealthURL {
			return `{"healthy":true,"version":"x"}`, 0, nil
		}
		return "", 0, nil
	})
	defer SetDaemonShellFunc(savedShell)

	fs := newTestFS(nil, nil)
	sb := &MockSandbox{
		Name_:      "vm",
		FSValue_:   fs,
		ShellCalls: &cmdCalls,
		ShellOut: map[string]msb.ShellResult{
			dockerdBinaryCheckCmd: msb.NewTestResult(true, 0, "", "", nil),
			dockerdReadyCmd:       msb.NewTestResult(false, 1, "", "", nil),
		},
	}
	ui := testutil.TermUIMock(t)
	restartDaemons(context.Background(), sb, map[string][]byte{"opencode.jsonc": []byte("{}")}, &ui)

	var joined strings.Builder
	for _, c := range cmdCalls {
		joined.WriteString(c)
		joined.WriteByte('|')
	}
	if !containsSubstring(joined.String(), daemonKillCmd) {
		t.Errorf("expected serve kill command, got %q", joined.String())
	}
	if containsSubstring(joined.String(), dockerdRestartCmd) {
		t.Errorf(
			"restartDaemons must NOT restart dockerd (env/secret changes go through the recreate path), got %q",
			joined.String(),
		)
	}
}

func TestSetUpSandboxRestartsDaemonsOnReuseDecision(t *testing.T) {
	var commands []string
	orig := SetDaemonShellFunc(func(_ context.Context, _ Sandbox, command string) (string, int, error) {
		commands = append(commands, command)
		if command == "curl -sfm2 "+daemonHealthURL {
			return `{"healthy":true,"version":"x"}`, 0, nil
		}
		return "", 0, nil
	})
	defer SetDaemonShellFunc(orig)

	fs := msb.NewTestFS(nil, nil)
	sb := &MockSandbox{Name_: "test-vm", FSValue_: fs}
	ui := testutil.TermUIMock(t)

	WithMockConfigPaths(t)

	// setUpSandbox's signature is
	//   (ctx, sb, opts, created, ui, restart).
	// Pass restart=true to force the daemon-restart path on a reused VM.
	commands = commands[:0]
	target, err := setUpSandbox(
		context.Background(), sb, RunOptions{},
		false, &ui, true,
	)
	if err != nil {
		t.Fatalf("setUpSandbox: %v", err)
	}
	if target == "" {
		t.Error("expected a resolved target")
	}
	joinedParts := make([]string, 0, len(commands))
	for _, c := range commands {
		joinedParts = append(joinedParts, c, "|")
	}
	joined := strings.Join(joinedParts, "")
	if len(commands) == 0 || !containsSubstring(joined, daemonKillCmd) {
		t.Errorf("expected opencode serve restart on restartDaemons=true, got %q", joined)
	}
}

func containsSubstring(hay, needle string) bool {
	return len(hay) >= len(needle) && contains(hay, needle)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
