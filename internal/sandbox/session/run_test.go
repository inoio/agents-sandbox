package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/options"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/reprovision"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"

	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
)

// parseMemory and resolveTmpSizeMiB tests moved to internal/sandbox/options/options_test.go.
// All other moved tests (ConfigEqual, EqualJSONFiles, BuildEnvMap, MergeEnvMaps, ReadVMFiles, IsSandboxActive, PlanReconfig)
// live in their owning packages: internal/sandbox/reprovision/*_test.go, internal/sandbox/msb/msb_test.go.

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

func TestBuildAttachCommand(t *testing.T) {
	got := buildAttachCommand("/workspace", []string{"foo"})
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

func TestBuildAttachCommandWorktreeTarget(t *testing.T) {
	got := buildAttachCommand("/home/dev/.local/share/opencode/worktree/abc/feat", nil)
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
	origDaemon := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == "curl -sfm2 "+daemonHealthURL {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	defer SetDaemonShellFunc(origDaemon)

	fs := msb.NewTestFS(nil, nil) // empty FS simulates a VM with empty config dir
	sb := &msb.MockSandbox{Name_: "test-vm", FSValue_: fs}
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.GetConfigPaths()
	snippet := filepath.Join(cp.UserOpencodeConfigDir(), "opencode.json5")
	if err := os.MkdirAll(filepath.Dir(snippet), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snippet, []byte(`{"model":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ui := &termio.Mock{}
	target, err := setUpSandbox(
		context.Background(),
		sb,
		options.RunOptions{},
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
		if command == "curl -sfm2 "+daemonHealthURL {
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
	ui := testutil.TermUIMock(t)
	restartDaemons(
		context.Background(),
		sb,
		&reprovision.ConfigFiles{HasSnippets: true, OpenCode: []byte("{}")},
		false,
		&ui,
	)

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
	orig := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		commands = append(commands, command)
		if command == "curl -sfm2 "+daemonHealthURL {
			return `{"healthy":true,"version":"x"}`, 0, nil
		}
		return "", 0, nil
	})
	defer SetDaemonShellFunc(orig)

	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{Name_: "test-vm", FSValue_: fs}
	ui := testutil.TermUIMock(t)

	configpaths.WithMockConfigPaths(t)

	// setUpSandbox's signature is
	//   (ctx, sb, opts, created, ui, restart).
	// Pass restart=true to force the daemon-restart path on a reused VM.
	commands = commands[:0]
	target, err := setUpSandbox(
		context.Background(), sb, options.RunOptions{},
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

func TestServeOnlyMessage(t *testing.T) {
	msg := serveOnlyMessage("127.0.0.1", "4096")
	for _, want := range []string{"Connect Opencode Desktop to", "http://127.0.0.1:4096", "OPENCODE_SERVER_PASSWORD", "Ctrl-D"} {
		if !strings.Contains(msg, want) {
			t.Errorf("serveOnlyMessage missing %q in:\n%s", want, msg)
		}
	}
}

func TestRunServeOnlyBlocksUntilCancel(t *testing.T) {
	sb := msb.NewMockSandbox(msb.SandboxOpts{})
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan error, 1)
	ui := &termio.Mock{}
	go func() { got <- runServeOnly(ctx, sb, ui) }()
	cancel()
	select {
	case err := <-got:
		if err == nil {
			t.Error("expected ctx.Err from completed runServeOnly")
		}
	case <-time.After(2 * time.Second):
		t.Error("runServeOnly did not return after ctx cancel")
	}
	if len(ui.InfoCalls) == 0 {
		t.Fatal("expected Infof call to print connect URL, got no info calls")
	}
	found := false
	for _, call := range ui.InfoCalls {
		if strings.Contains(call, "http://127.0.0.1:4096") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected connect URL 'http://127.0.0.1:4096' in info output, got %v", ui.InfoCalls)
	}
}
