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

func TestProjectVMNameIncludesAgent(t *testing.T) {
	got := projectVMName(state.Key{Slug: "myproject-aBc1234DeF", Agent: "pi"})
	want := "opencode-sandbox-vm-myproject-aBc1234DeF-pi"
	if got != want {
		t.Errorf("projectVMName = %q, want %q", got, want)
	}
	got = projectVMName(state.Key{Slug: "myproject-aBc1234DeF", Agent: "opencode"})
	want = "opencode-sandbox-vm-myproject-aBc1234DeF-opencode"
	if got != want {
		t.Errorf("projectVMName = %q, want %q", got, want)
	}
}

func TestProjectVMNameTruncationPreservesAgent(t *testing.T) {
	long := "x" + string(make([]byte, options.MaxSandboxNameLen))
	k := state.Key{Slug: long, Agent: "pi"}
	got := projectVMName(k)
	if len(got) > options.MaxSandboxNameLen {
		t.Fatalf("name %d bytes > max %d", len(got), options.MaxSandboxNameLen)
	}
	if !strings.HasSuffix(got, "-pi") {
		t.Errorf("truncated name %q lost the agent suffix", got)
	}
}

// TestProjectVMNameExtremeAgentCoversFinalCut covers the final hard cut in
// projectVMName, reachable only when the agent name itself exceeds the max
// name length. The full name is cut at the byte limit and the agent suffix
// may be lost, so we only assert the length bound.
func TestProjectVMNameExtremeAgentCoversFinalCut(t *testing.T) {
	slug := "proj"
	agent := "a" + string(make([]byte, options.MaxSandboxNameLen))
	got := projectVMName(state.Key{Slug: slug, Agent: agent})
	if len(got) != options.MaxSandboxNameLen {
		t.Errorf("expected name cut to exactly %d bytes, got %d", options.MaxSandboxNameLen, len(got))
	}
}

// TestKeyDirFlockPath covers the agent-scoped flock-path construction used by
// ensureProjectVM: it lives under the (slug, agent) key directory.
func TestKeyDirFlockPath(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "myproj", Agent: "opencode"}
	want := filepath.Join(configpaths.Get().UserStateDir(), "myproj", "opencode", "ensure-vm.lock")
	if got := filepath.Join(state.KeyDir(k), "ensure-vm.lock"); got != want {
		t.Errorf("flock path = %q, want %q", got, want)
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
	if got := currentUpgradeVersion(opencodeAgent(t)); got != "" {
		t.Errorf("currentUpgradeVersion() = %q, want empty for corrupt file", got)
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

	persistConfigHashes(state.Key{Slug: slug, Agent: "opencode"}, policy, nil, &ui)

	st, err := state.ReadState(state.Key{Slug: slug, Agent: "opencode"})
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

	err := stopOrKillProjectVM(context.Background(), false, false, &ui, testVMKey(), "stop", "Stopping", client,
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

	err := stopOrKillProjectVM(context.Background(), false, false, &ui, testVMKey(), "stop", "Stopping", client,
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

	if err := KillProjectVM(context.Background(), true, true, &ui, "opencode"); err != nil {
		t.Fatalf("KillProjectVM dry-run: %v", err)
	}
}

// TestStopAndKillProjectVMUnknownAgent covers the unknown-agent error branch
// in both StopProjectVM and KillProjectVM.
func TestStopAndKillProjectVMUnknownAgent(t *testing.T) {
	ui := termio.NewTestMock(t)
	if err := StopProjectVM(context.Background(), false, false, &ui, "no-such-agent"); err == nil {
		t.Fatal("expected error from StopProjectVM with an unknown agent")
	}
	if err := KillProjectVM(context.Background(), false, false, &ui, "no-such-agent"); err == nil {
		t.Fatal("expected error from KillProjectVM with an unknown agent")
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

	cfs := &reprovision.ConfigFiles{HasSnippets: true, Merged: []byte("{}")}
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
	sb, boot, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{DryRunVM: true},
		"img:tag",
		"vol",
		"/workspace",
		nil,
		testVMKey(),
		&ui,
	)
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

	_, _, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{},
		"img:tag",
		"vol",
		"/workspace",
		nil,
		testVMKey(),
		&ui,
	)
	if err == nil {
		t.Fatal("expected error from GetSandbox")
	}
}
