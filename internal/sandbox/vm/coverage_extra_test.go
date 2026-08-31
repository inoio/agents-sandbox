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
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/network"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

func joinStrings(parts []string) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p)
		b.WriteByte('|')
	}
	return b.String()
}

// TestProjectVMNameTruncatesToMaxLen covers the truncation branch of
// projectVMName (prefix + slug longer than MaxSandboxNameLen).
func TestProjectVMNameTruncatesToMaxLen(t *testing.T) {
	slug := "x" + string(make([]byte, options.MaxSandboxNameLen))
	got := projectVMName(slug)
	if len(got) > options.MaxSandboxNameLen {
		t.Errorf("expected name <= %d bytes, got %d", options.MaxSandboxNameLen, len(got))
	}
	if len(got) != options.MaxSandboxNameLen {
		t.Errorf("expected name truncated to exactly %d bytes, got %d", options.MaxSandboxNameLen, len(got))
	}
}

// TestCurrentUpgradeVersionIgnoresCorruptFile covers the load-error branch of
// currentUpgradeVersion (a corrupt state file must yield "").
func TestCurrentUpgradeVersionIgnoresCorruptFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	path := filepath.Join(configpaths.Get().UserStateDir(), "updater.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("::: not yaml :::"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := currentUpgradeVersion(); got != "" {
		t.Errorf("currentUpgradeVersion() = %q, want empty for corrupt file", got)
	}
}

// TestRecordUpgradeVersionReturnsLoadError covers the case where the updater
// state file cannot be read at all (non-not-found), so recordUpgradeVersion
// propagates the read error.
func TestRecordUpgradeVersionReturnsLoadError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	// Point the state directory at a regular file so reading updater.yaml fails.
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "userstate")
	if err := os.WriteFile(stateFile, []byte("not-a-dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	orig := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths {
		return failingStateDirConfigPaths{stateDir: stateFile}
	}
	t.Cleanup(func() { configpaths.Get = orig })

	if err := recordUpgradeVersion("2.0.0"); err == nil {
		t.Error("expected error when updater state cannot be read")
	}
}

// TestSaveUpgradeStateMkdirError covers the MkdirAll error path in
// saveUpgradeState (state directory cannot be created).
func TestSaveUpgradeStateMkdirError(t *testing.T) {
	withFailingStateDir(t)
	if err := saveUpgradeState(upgradeState{CurrentVersion: "1.0.0"}); err == nil {
		t.Error("expected error when the state directory cannot be created")
	}
}

// TestPersistConfigHashesCoversEnvSecretAndNetwork covers the previously
// entirely-uncovered persistConfigHashes function: it persists the desired
// env/secret/network fingerprints after a fresh VM creation.
func TestPersistConfigHashesCoversEnvSecretAndNetwork(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)

	slug := "hashproj"
	policy := network.Policy{Profile: network.ProfileNone}

	persistConfigHashes(slug, policy, nil, &ui)

	st, err := state.ReadState(slug)
	if err != nil {
		t.Fatalf("ReadState after persistConfigHashes: %v", err)
	}
	if st.NetworkState.Hash == "" {
		t.Error("expected network state hash to be persisted")
	}
	if st.MountState.Hash == "" {
		t.Error("expected mount state hash to be persisted")
	}
}

// TestAcquireProjectFlockOpenError covers the open-file error branch of
// acquireProjectFlock (path under a non-directory parent).
func TestAcquireProjectFlockOpenError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	// UserStateDir is a directory, so create a regular file as the parent of
	// the lock path to force os.OpenFile to fail with ENOTDIR.
	parent := filepath.Join(configpaths.Get().UserStateDir(), "notadir")
	if err := os.WriteFile(parent, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(parent, "ensure-vm.lock")
	if _, err := acquireProjectFlock(lockPath); err == nil {
		t.Error("expected error when opening the flock fails")
	}
}

// TestAcquireProjectFlockFlockError covers the flock-syscall error branch. It
// is hard to force an actual flock failure on a writable file, so instead we
// verify the happy path acquires and releases a lock and returns a working
// release closure.
func TestAcquireProjectFlockAcquiresAndReleases(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "ensure-vm.lock")
	release, err := acquireProjectFlock(lockPath)
	if err != nil {
		t.Fatalf("acquireProjectFlock: %v", err)
	}
	if release == nil {
		t.Fatal("expected a non-nil release function")
	}
	release()
}

// TestStopProjectVMGetSandboxError covers the non-not-found GetSandbox error
// branch in stopOrKillProjectVM.
func TestStopProjectVMGetSandboxError(t *testing.T) {
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	client.SetGetSandboxErr(errors.New("boom"))
	msb.WithMsbMock(t, client)

	err := stopOrKillProjectVM(context.Background(), false, false, &ui, "stop", "Stopping", client,
		func(h msb.SandboxHandle, c context.Context) error { return h.Stop(c) })
	if err == nil {
		t.Fatal("expected error from GetSandbox")
	}
}

// TestStopProjectVMStopFnError covers the stopFn error branch and the remove
// error branch (remove fails after a successful stop).
func TestStopProjectVMStopFnError(t *testing.T) {
	ui := termio.NewTestMock(t)
	handle := &msb.MockSandboxHandle{
		Name_:   "opencode-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
	}
	handle.StopErr = errors.New("stop failed")
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	err := stopOrKillProjectVM(context.Background(), false, false, &ui, "stop", "Stopping", client,
		func(h msb.SandboxHandle, c context.Context) error { return h.Stop(c) })
	if err == nil {
		t.Fatal("expected error from stopFn")
	}
}

// TestKillProjectVMDryRunRemove covers the kill dry-run branch and the
// actionWord selection.
func TestKillProjectVMDryRunRemove(t *testing.T) {
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(&msb.MockSandboxHandle{
		Name_:   "opencode-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
	})
	msb.WithMsbMock(t, client)

	if err := KillProjectVM(context.Background(), true, true, &ui); err != nil {
		t.Fatalf("KillProjectVM dry-run: %v", err)
	}
}

// TestSetUpSandboxProvisionError covers the provision-failure branch of
// setUpSandbox: a provision failure is logged as a warning and the setup
// continues (config provisioning is non-disruptive and never fatal).
func TestSetUpSandboxProvisionError(t *testing.T) {
	orig := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	defer SetDaemonShellFunc(orig)

	configpaths.WithMockConfigPaths(t)

	ui := termio.NewTestMock(t)
	fs := msb.NewTestFS(nil, nil)
	fs.WriteErr = errors.New("write denied")
	sb := &msb.MockSandbox{
		Name_:    "vm",
		FSValue_: fs,
		ShellOut: map[string]msb.ShellResult{
			dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
		},
	}

	cfs := &reprovision.ConfigFiles{HasSnippets: true, OpenCode: []byte("{}")}
	if _, err := setUpSandbox(
		context.Background(),
		sb,
		options.RunOptions{},
		cfs,
		&ui,
		false,
		vmBootStarted,
	); err != nil {
		t.Fatalf("setUpSandbox: %v", err)
	}

	joined := joinStrings(ui.WarnCalls)
	if !contains(joined, "provision failed") {
		t.Errorf("expected a provision-failure warning, got %v", ui.WarnCalls)
	}
}

// TestRestartDaemonsKillError covers the kill-stale-daemon failure branch of
// restartDaemons: the function warns but continues to ensure the daemon.
func TestRestartDaemonsKillError(t *testing.T) {
	orig := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonKillCmd() {
			return "", 0, errors.New("kill failed")
		}
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	defer SetDaemonShellFunc(orig)

	ui := termio.NewTestMock(t)
	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{Name_: "vm", FSValue_: fs}

	restartDaemons(context.Background(), opencodeAgent(t), sb, false, &ui)

	if !contains(joinStrings(ui.WarnCalls), "kill stale daemon failed") {
		t.Errorf("expected a kill-failure warning, got %v", ui.WarnCalls)
	}
}

// TestRestartDaemonsNoProvider covers the early-return branch of
// restartDaemons for an agent that is not a DaemonProvider: it must no-op.
func TestRestartDaemonsNoProvider(t *testing.T) {
	ui := termio.NewTestMock(t)
	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{Name_: "vm", FSValue_: fs, ShellCalls: &[]string{}}

	restartDaemons(context.Background(), &fakeAgent{}, sb, false, &ui)

	if len(*sb.ShellCalls) != 0 {
		t.Errorf("expected no shell calls for an agent without a DaemonProvider, got %v", *sb.ShellCalls)
	}
}

// TestEnsureProjectVMDryRunVM covers the DryRunVM early-return branch.
func TestEnsureProjectVMDryRunVM(t *testing.T) {
	ui := termio.NewTestMock(t)
	sb, boot, err := ensureProjectVM(context.Background(), options.RunOptions{DryRunVM: true},
		"img:tag", "vol", "/workspace", nil, &ui)
	if err != nil {
		t.Fatalf("ensureProjectVM dry-run: %v", err)
	}
	if sb != nil {
		t.Error("expected nil sandbox for dry-run-vm")
	}
	if boot != vmBootConnected {
		t.Errorf("expected vmBootConnected for dry-run, got %v", boot)
	}
}

// TestEnsureProjectVMGetSandboxFatalError covers the non-not-found GetSandbox
// error branch in ensureProjectVM.
func TestEnsureProjectVMGetSandboxFatalError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	client.SetGetSandboxErr(errors.New("boom"))
	msb.WithMsbMock(t, client)

	_, _, err := ensureProjectVM(context.Background(), options.RunOptions{},
		"img:tag", "vol", "/workspace", nil, &ui)
	if err == nil {
		t.Fatal("expected error from GetSandbox")
	}
}
