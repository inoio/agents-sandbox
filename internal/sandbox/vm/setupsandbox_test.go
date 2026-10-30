package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// opencodeMergedConfigPath is where the merged opencode config lands in the
// sandbox home (reprovision exposes it as ConfigFiles.MergedPath).
const opencodeMergedConfigPath = "/home/dev/.config/opencode/opencode.jsonc"

func TestBuildMountsIncludesTmpfsAtTmp(t *testing.T) {
	mounts := buildMounts(
		"test-home-vol",
		"/repo/path",
		options.DefaultTmpSizeMiB,
		options.DefaultWorkspaceQuotaMiB,
		nil,
	)

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
	mounts := buildMounts("test-home-vol", "/repo/path", 4096, options.DefaultWorkspaceQuotaMiB, nil)

	tmpMount := mounts[tmpMountPath]
	if tmpMount.SizeMiB != 4096 {
		t.Errorf("expected /tmp tmpfs size 4096 MiB, got %d", tmpMount.SizeMiB)
	}
}

func TestBuildMountsSetsWorkspaceQuota(t *testing.T) {
	mounts := buildMounts("test-home-vol", "/repo/path", options.DefaultTmpSizeMiB, 32*1024, nil)

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

func TestBuildMountsSetsWorkspaceMirrorHostPermissions(t *testing.T) {
	mounts := buildMounts(
		"test-home-vol",
		"/repo/path",
		options.DefaultTmpSizeMiB,
		options.DefaultWorkspaceQuotaMiB,
		nil,
	)

	wsMount, ok := mounts[defaultTargetDir]
	if !ok {
		t.Fatal("expected /workspace mount, not found in mounts map")
	}
	if wsMount.HostPermissions != msbSdk.HostPermissionsMirror {
		t.Errorf(
			"expected /workspace host permissions %q, got %q",
			msbSdk.HostPermissionsMirror,
			wsMount.HostPermissions,
		)
	}
}

func TestBuildMountsWorkspaceQuotaDefault(t *testing.T) {
	mounts := buildMounts(
		"test-home-vol",
		"/repo/path",
		options.DefaultTmpSizeMiB,
		options.DefaultWorkspaceQuotaMiB,
		nil,
	)

	wsMount, ok := mounts[defaultTargetDir]
	if !ok {
		t.Fatal("expected /workspace mount, not found in mounts map")
	}
	if wsMount.QuotaMiB != options.DefaultWorkspaceQuotaMiB {
		t.Errorf("expected /workspace default quota %d MiB, got %d", options.DefaultWorkspaceQuotaMiB, wsMount.QuotaMiB)
	}
}

func TestBuildMountsIncludesConfiguredBindMount(t *testing.T) {
	mounts := buildMounts(
		"test-home-vol",
		"/repo/path",
		options.DefaultTmpSizeMiB,
		options.DefaultWorkspaceQuotaMiB,
		mounts.Mounts{
			"/home/dev/.m2": {Source: "/host/home/.m2"},
			"/home/dev/ref": {Source: "/host/ref", Readonly: true},
		},
	)

	m2, ok := mounts["/home/dev/.m2"]
	if !ok {
		t.Fatalf("expected configured .m2 bind mount, got %v", mounts)
	}
	if m2.Kind() != msbSdk.MountKindBind {
		t.Errorf("expected a bind mount, got kind %d", m2.Kind())
	}
	if m2.Bind != "/host/home/.m2" {
		t.Errorf("bind source = %q, want /host/home/.m2", m2.Bind)
	}
	if m2.Readonly {
		t.Error("expected .m2 to be writable")
	}
	if m2.HostPermissions != msbSdk.HostPermissionsDefault {
		t.Errorf(
			"expected configured mounts to keep the private host-permissions default, got %q",
			m2.HostPermissions,
		)
	}

	ref, ok := mounts["/home/dev/ref"]
	if !ok {
		t.Fatal("expected configured readonly bind mount")
	}
	if !ref.Readonly {
		t.Error("expected readonly to be propagated to the mount config")
	}
}

// TestBuildMountsKeepsManagedMounts ensures configured mounts are additive and
// never replace the managed home/workspace/tmp mounts.
func TestBuildMountsKeepsManagedMounts(t *testing.T) {
	built := buildMounts(
		"test-home-vol",
		"/repo/path",
		options.DefaultTmpSizeMiB,
		options.DefaultWorkspaceQuotaMiB,
		mounts.Mounts{"/home/dev/.m2": {Source: "/host/.m2"}},
	)

	if home, ok := built[mounts.VMHomeDir]; !ok || home.Named != "test-home-vol" {
		t.Errorf("managed home mount altered: %+v", built[mounts.VMHomeDir])
	}
	if ws, ok := built[defaultTargetDir]; !ok || ws.Bind != "/repo/path" {
		t.Errorf("managed workspace mount altered: %+v", built[defaultTargetDir])
	}
	if tmp, ok := built[tmpMountPath]; !ok || tmp.Kind() != msbSdk.MountKindTmpfs {
		t.Errorf("managed tmp mount altered: %+v", built[tmpMountPath])
	}
	for _, target := range []string{mounts.VMHomeDir, tmpMountPath} {
		if got := built[target].HostPermissions; got != msbSdk.HostPermissionsDefault {
			t.Errorf("mount %q should keep the private host-permissions default, got %q", target, got)
		}
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
	origDaemon := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(origDaemon) })

	fs := msb.NewTestFS(nil, nil) // empty FS simulates a VM with empty config dir
	sb := &msb.MockSandbox{Name_: "test-vm", FSValue_: fs}
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	snippet := filepath.Join(configpaths.MustDir(cp.UserAgentConfigDir(opencodeAgent(t))), "opencode-x.json5")
	if err := os.MkdirAll(filepath.Dir(snippet), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, snippet, `{"model":"x"}`)

	ui := &termio.Mock{}
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	target, err := setUpSandbox(
		context.Background(),
		testVMKey(),
		sb,
		options.RunOptions{Agent: "opencode"},
		cfs,
		ui,
		false,
		vmBootStarted,
		mounts.WorkspaceMountPath,
	)
	if err != nil {
		t.Fatalf("setUpSandbox: %v", err)
	}
	if target != defaultTargetDir {
		t.Errorf("target = %q, want %q", target, defaultTargetDir)
	}

	wroteConfig := fs.Writes != nil &&
		fs.Writes[opencodeMergedConfigPath] != nil
	if !wroteConfig {
		t.Errorf(
			"expected config to be provisioned on %s, but opencode.json was never written",
			provisionMsg,
		)
	}
}

func TestRestartDaemonsRestartsServe(t *testing.T) {
	savedHealth := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, _ string) (string, int, error) {
		return `{"healthy":true,"version":"x"}`, 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(savedHealth) })

	calls := &[]msb.MockDetachedCall{}
	sb := &msb.MockSandbox{Name_: "vm", ExecDetachedCalls: calls}
	ui := termio.NewTestMock(t)
	restartDaemons(
		context.Background(),
		opencodeAgent(t),
		sb,
		false,
		&ui,
	)

	if len(*calls) != 1 {
		t.Errorf("expected one opencode serve launch, got %+v", *calls)
	}
	assertDetachedHelperLaunch(t, *calls, "agent-daemon", "opencode")
}

func TestSetUpSandboxRestartsDaemonsOnReuseDecision(t *testing.T) {
	orig := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, _ string) (string, int, error) {
		return `{"healthy":true,"version":"x"}`, 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(orig) })

	calls := &[]msb.MockDetachedCall{}
	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{Name_: "test-vm", FSValue_: fs, ExecDetachedCalls: calls}
	ui := termio.NewTestMock(t)

	configpaths.WithMockConfigPaths(t)

	// setUpSandbox's signature is
	//   (ctx, k, sb, opts, cfs, ui, restart, boot, guestCWD).
	// Pass restart=true to force the daemon-restart path on a reused VM.
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	target, err := setUpSandbox(
		context.Background(), testVMKey(), sb, options.RunOptions{Agent: "opencode"},
		cfs, &ui, true, vmBootStarted, mounts.WorkspaceMountPath,
	)
	if err != nil {
		t.Fatalf("setUpSandbox: %v", err)
	}
	if target == "" {
		t.Error("expected a resolved target")
	}
	if len(*calls) != 1 {
		t.Errorf("expected opencode serve restart on restartDaemons=true, got %+v", *calls)
	}
	assertDetachedHelperLaunch(t, *calls, "agent-daemon", "opencode")
}

// TestSetUpSandboxSkipsRestartOnProvisionError verifies that when a daemon
// restart is warranted (restart=true) but provisioning the updated config
// fails, the running daemon is preserved: restartDaemons is not called and no
// daemon commands are issued, but a target is still returned.
func TestSetUpSandboxSkipsRestartOnProvisionError(t *testing.T) {
	var commands []string
	orig := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		commands = append(commands, command)
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"x"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(orig) })

	configpaths.WithMockConfigPaths(t)

	ui := termio.NewTestMock(t)
	fs := msb.NewTestFS(nil, nil)
	fs.WriteErr = errors.New("write denied")
	sb := &msb.MockSandbox{Name_: "vm", FSValue_: fs}

	cfs := &reprovision.ConfigFiles{HasSnippets: true, Merged: []byte("{}")}
	target, err := setUpSandbox(
		context.Background(),
		testVMKey(),
		sb,
		options.RunOptions{Agent: "opencode"},
		cfs,
		&ui,
		true,
		vmBootStarted,
		mounts.WorkspaceMountPath,
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
	origDaemon := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(origDaemon) })

	// The VM already contains an OLD opencode.json (content differs from desired).
	// This simulates attaching to a running VM and choosing "keep": no daemon
	// restart, but the updated config must still be provisioned so the next
	// daemon start picks it up.
	ocPath := opencodeMergedConfigPath
	fs := msb.NewTestFS(map[string][]byte{
		ocPath: []byte(`{"model":"old"}`),
	}, nil)
	sb := &msb.MockSandbox{Name_: "test-vm", FSValue_: fs}

	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	snippet := filepath.Join(configpaths.MustDir(cp.UserAgentConfigDir(opencodeAgent(t))), "opencode-x.json5")
	if err := os.MkdirAll(filepath.Dir(snippet), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, snippet, `{"model":"new"}`)

	ui := &termio.Mock{}
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	_, err = setUpSandbox(
		context.Background(),
		testVMKey(),
		sb,
		options.RunOptions{Agent: "opencode"},
		cfs,
		ui,
		false, // restart=false (user chose "keep")
		vmBootStarted,
		mounts.WorkspaceMountPath,
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
	origDaemon := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(origDaemon) })

	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{Name_: "test-vm", FSValue_: fs}
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	cfs := &reprovision.ConfigFiles{
		Hooks: []homeconfig.HookSpec{
			{Target: "/home/dev/.hello.sh", Source: "x", HasShebang: true},
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
			_, err := setUpSandbox(
				context.Background(),
				testVMKey(),
				sb,
				options.RunOptions{Agent: "opencode"},
				cfs,
				ui,
				false,
				tc.boot,
				mounts.WorkspaceMountPath,
			)
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

// TestSetUpSandboxProvisionsMirrorOnly verifies that a ConfigFiles carrying
// only mirror files still triggers provisioning.
func TestSetUpSandboxProvisionsMirrorOnly(t *testing.T) {
	origDaemon := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(origDaemon) })

	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{Name_: "test-vm", FSValue_: fs}
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	cfs := &reprovision.ConfigFiles{
		Mirror: map[string][]byte{
			"/home/dev/.config/opencode/tui.json": []byte(`{"theme":"dark"}`),
		},
	}
	if _, err := setUpSandbox(
		context.Background(),
		testVMKey(),
		sb,
		options.RunOptions{Agent: "opencode"},
		cfs,
		ui,
		false,
		vmBootStarted,
		mounts.WorkspaceMountPath,
	); err != nil {
		t.Fatalf("setUpSandbox: %v", err)
	}
	if fs.Writes["/home/dev/.config/opencode/tui.json"] == nil {
		t.Error("expected mirror file to be provisioned")
	}
}
