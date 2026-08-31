package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/homeconfig"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/opencode-sandbox/internal/termio"
	"github.com/inoio/opencode-sandbox/internal/testutil"
)

func TestBuildMountsIncludesTmpfsAtTmp(t *testing.T) {
	mounts := buildMounts("test-home-vol", "/repo/path", options.DefaultTmpSizeMiB, options.DefaultWorkspaceQuotaMiB)

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
	mounts := buildMounts("test-home-vol", "/repo/path", 4096, options.DefaultWorkspaceQuotaMiB)

	tmpMount := mounts[tmpMountPath]
	if tmpMount.SizeMiB != 4096 {
		t.Errorf("expected /tmp tmpfs size 4096 MiB, got %d", tmpMount.SizeMiB)
	}
}

func TestBuildMountsSetsWorkspaceQuota(t *testing.T) {
	mounts := buildMounts("test-home-vol", "/repo/path", options.DefaultTmpSizeMiB, 32*1024)

	wsMount, ok := mounts[defaultTargetDir]
	if !ok {
		t.Fatal("expected /workspace mount, not found in mounts map")
	}
	if wsMount.Kind() != msbSdk.MountKindBind {
		t.Errorf("expected /workspace to be a bind mount, got kind %d", wsMount.Kind())
	}
	if wsMount.QuotaMiB != 32*1024 {
		t.Errorf("expected /workspace quota 32768 MiB, got %d", wsMount.QuotaMiB)
	}
}

func TestBuildMountsWorkspaceQuotaDefault(t *testing.T) {
	mounts := buildMounts("test-home-vol", "/repo/path", options.DefaultTmpSizeMiB, options.DefaultWorkspaceQuotaMiB)

	wsMount, ok := mounts[defaultTargetDir]
	if !ok {
		t.Fatal("expected /workspace mount, not found in mounts map")
	}
	if wsMount.QuotaMiB != options.DefaultWorkspaceQuotaMiB {
		t.Errorf("expected /workspace default quota %d MiB, got %d", options.DefaultWorkspaceQuotaMiB, wsMount.QuotaMiB)
	}
}

func TestSetUpSandboxProvisionsConfigOnFreshSetup(t *testing.T) {
	setUpSandboxProvisionsConfig(t, "fresh setup")
}

func TestSetUpSandboxProvisionsConfigOnReuseWithEmptyDir(t *testing.T) {
	setUpSandboxProvisionsConfig(t, "reused VM with empty config dir")
}

func setUpSandboxProvisionsConfig(t *testing.T, provisionMsg string) {
	t.Helper()
	origDaemon := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	defer SetDaemonShellFunc(origDaemon)

	fs := msb.NewTestFS(nil, nil) // empty FS simulates a VM with empty config dir
	sb := &msb.MockSandbox{Name_: "test-vm", FSValue_: fs}
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	snippet := filepath.Join(cp.UserOpencodeConfigDir(), "opencode-x.json5")
	if err := os.MkdirAll(filepath.Dir(snippet), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, snippet, `{"model":"x"}`)

	ui := &termio.Mock{}
	cfs, err := reprovision.LoadConfigFiles(configpaths.Get().UserOpencodeConfigDir(), ui)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	target, err := setUpSandbox(
		context.Background(),
		sb,
		options.RunOptions{},
		cfs,
		ui,
		false,
		vmBootStarted,
	)
	if err != nil {
		t.Fatalf("setUpSandbox: %v", err)
	}
	if target != defaultTargetDir {
		t.Errorf("target = %q, want %q", target, defaultTargetDir)
	}

	wroteConfig := fs.Writes != nil && fs.Writes[reprovision.OpenCodeConfigPath(reprovision.VMHomeDir)] != nil
	if !wroteConfig {
		t.Errorf(
			"expected config to be provisioned on %s, but opencode.json was never written",
			provisionMsg,
		)
	}
}

func TestRestartDaemonsRestartsServe(t *testing.T) {
	var cmdCalls []string
	savedShell := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		cmdCalls = append(cmdCalls, command)
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"x"}`, 0, nil
		}
		return "", 0, nil
	})
	defer SetDaemonShellFunc(savedShell)

	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{
		Name_:      "vm",
		FSValue_:   fs,
		ShellCalls: &cmdCalls,
		ShellOut: map[string]msb.ShellResult{
			dockerdBinaryCheckCmd: msb.NewTestResult(true, 0, "", "", nil),
			dockerdReadyCmd:       msb.NewTestResult(false, 1, "", "", nil),
		},
	}
	ui := termio.NewTestMock(t)
	restartDaemons(
		context.Background(),
		opencodeAgent(t),
		sb,
		false,
		&ui,
	)

	var joined strings.Builder
	for _, c := range cmdCalls {
		joined.WriteString(c)
		joined.WriteByte('|')
	}
	if !containsSubstring(joined.String(), opencodeProvider(t).DaemonKillCmd()) {
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
	orig := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		commands = append(commands, command)
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"x"}`, 0, nil
		}
		return "", 0, nil
	})
	defer SetDaemonShellFunc(orig)

	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{Name_: "test-vm", FSValue_: fs}
	ui := termio.NewTestMock(t)

	configpaths.WithMockConfigPaths(t)

	// setUpSandbox's signature is
	//   (ctx, sb, opts, cfs, ui, restart).
	// Pass restart=true to force the daemon-restart path on a reused VM.
	commands = commands[:0]
	cfs, err := reprovision.LoadConfigFiles(configpaths.Get().UserOpencodeConfigDir(), &ui)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	target, err := setUpSandbox(
		context.Background(), sb, options.RunOptions{},
		cfs, &ui, true, vmBootStarted,
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
	if len(commands) == 0 || !containsSubstring(joined, opencodeProvider(t).DaemonKillCmd()) {
		t.Errorf("expected opencode serve restart on restartDaemons=true, got %q", joined)
	}
}

func containsSubstring(hay, needle string) bool {
	return len(hay) >= len(needle) && contains(hay, needle)
}

// TestSetUpSandboxSkipsRestartOnProvisionError verifies that when a daemon
// restart is warranted (restart=true) but provisioning the updated config
// fails, the running daemon is preserved: restartDaemons is not called and no
// daemon commands are issued, but a target is still returned.
func TestSetUpSandboxSkipsRestartOnProvisionError(t *testing.T) {
	var commands []string
	orig := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		commands = append(commands, command)
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"x"}`, 0, nil
		}
		return "", 0, nil
	})
	defer SetDaemonShellFunc(orig)

	configpaths.WithMockConfigPaths(t)

	ui := termio.NewTestMock(t)
	fs := msb.NewTestFS(nil, nil)
	fs.WriteErr = errors.New("write denied")
	sb := &msb.MockSandbox{Name_: "vm", FSValue_: fs}

	cfs := &reprovision.ConfigFiles{HasSnippets: true, OpenCode: []byte("{}")}
	target, err := setUpSandbox(
		context.Background(),
		sb,
		options.RunOptions{},
		cfs,
		&ui,
		true,
		vmBootStarted,
	)
	if err != nil {
		t.Fatalf("setUpSandbox: %v", err)
	}
	if target == "" {
		t.Error("expected a resolved target even when provisioning failed")
	}

	joined := joinStrings(commands)
	if len(commands) > 0 {
		t.Errorf("expected no daemon commands on provision failure (daemon preserved), got %q", joined)
	}
	if !contains(joinStrings(ui.WarnCalls), "provision failed") {
		t.Errorf("expected a provision-failure warning, got %v", ui.WarnCalls)
	}
}

func TestSetUpSandboxProvisionsUpdatedConfigOnKeep(t *testing.T) {
	origDaemon := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	defer SetDaemonShellFunc(origDaemon)

	// The VM already contains an OLD opencode.json (content differs from desired).
	// This simulates attaching to a running VM and choosing "keep": no daemon
	// restart, but the updated config must still be provisioned so the next
	// daemon start picks it up.
	ocPath := reprovision.OpenCodeConfigPath(reprovision.VMHomeDir)
	fs := msb.NewTestFS(map[string][]byte{
		ocPath: []byte(`{"model":"old"}`),
	}, nil)
	sb := &msb.MockSandbox{Name_: "test-vm", FSValue_: fs}

	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	snippet := filepath.Join(cp.UserOpencodeConfigDir(), "opencode-x.json5")
	if err := os.MkdirAll(filepath.Dir(snippet), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, snippet, `{"model":"new"}`)

	ui := &termio.Mock{}
	cfs, err := reprovision.LoadConfigFiles(configpaths.Get().UserOpencodeConfigDir(), ui)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	_, err = setUpSandbox(
		context.Background(),
		sb,
		options.RunOptions{},
		cfs,
		ui,
		false, // restart=false (user chose "keep")
		vmBootStarted,
	)
	if err != nil {
		t.Fatalf("setUpSandbox: %v", err)
	}

	wrote := fs.Writes != nil && fs.Writes[ocPath] != nil
	if !wrote {
		t.Error("expected updated opencode.json to be provisioned even when daemon restart is deferred (keep)")
	}
}

// TestSetUpSandboxRunsHooksOnlyOnBoot verifies that startup hooks run only
// when the VM transitioned to running this run (started/created), and are
// skipped when the VM was already running and merely connected to.
func TestSetUpSandboxRunsHooksOnlyOnBoot(t *testing.T) {
	origDaemon := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	defer SetDaemonShellFunc(origDaemon)

	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{Name_: "test-vm", FSValue_: fs}
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	cfs := &reprovision.ConfigFiles{
		Hooks: []homeconfig.HookSpec{
			{Target: "/home/dev/.hello.sh", Source: "x", Interpreter: "/bin/sh"},
		},
	}

	for _, tc := range []struct {
		name   string
		boot   vmBoot
		expect bool
	}{
		{"connected VM skips hooks", vmBootConnected, false},
		{"started VM runs hooks", vmBootStarted, true},
		{"created VM runs hooks", vmBootCreated, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sb.AttachCmd = ""
			sb.AttachUser = ""
			_, err := setUpSandbox(context.Background(), sb, options.RunOptions{}, cfs, ui, false, tc.boot)
			if err != nil {
				t.Fatalf("setUpSandbox: %v", err)
			}
			ran := sb.AttachCmd != ""
			if ran != tc.expect {
				t.Errorf("hook ran = %v, want %v", ran, tc.expect)
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
