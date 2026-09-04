package vm

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestProvisionHostConfig(t *testing.T) {
	truePtr := true
	falsePtr := false
	cases := []struct {
		name string
		opts options.RunOptions
		want bool
	}{
		{name: "nil enables by default", opts: options.RunOptions{}, want: true},
		{name: "explicit true", opts: options.RunOptions{ProvisionHostConfig: &truePtr}, want: true},
		{name: "explicit false", opts: options.RunOptions{ProvisionHostConfig: &falsePtr}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := provisionHostConfig(tc.opts); got != tc.want {
				t.Errorf("provisionHostConfig(%+v) = %v, want %v", tc.opts, got, tc.want)
			}
		})
	}
}

func TestListSandboxesError(t *testing.T) {
	mock := &msb.MockMsbClient{}
	mock.ListSandboxesFn = func(_ context.Context, _ map[string]string) ([]msb.SandboxHandle, error) {
		return nil, errors.New("list failed")
	}
	msb.WithMsbMock(t, mock)

	if _, err := ListSandboxes(context.Background()); err == nil {
		t.Fatal("expected error when the msb ListSandboxes fails")
	}
}

// TestRecordToolAgentVersionNotProvider covers the branch where the agent does
// not implement VersionProvider: the entry is returned unchanged.
func TestRecordToolAgentVersionNotProvider(t *testing.T) {
	entry := agentUpgradeState{CurrentVersion: "1.0.0"}
	got := recordToolAgentVersion(context.Background(), &fakeAgent{}, &msb.MockSandbox{}, &termio.Mock{}, entry)
	if got.CurrentVersion != "1.0.0" {
		t.Errorf("expected entry unchanged, got %+v", got)
	}
}

// TestRecordToolAgentVersionShellError covers the branch where detecting the
// agent version via Shell fails: a warning is emitted and the entry is returned
// unchanged.
func TestRecordToolAgentVersionShellError(t *testing.T) {
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{ShellErr: errors.New("shell failed")}
	entry := agentUpgradeState{CurrentVersion: "1.0.0"}
	got := recordToolAgentVersion(context.Background(), opencodeAgent(t), sb, ui, entry)
	if got.CurrentVersion != "1.0.0" {
		t.Errorf("expected entry unchanged on shell error, got %+v", got)
	}
	if !contains(joinStrings(ui.WarnCalls), "could not detect agent version") {
		t.Errorf("expected a warning about the failed version detection, got %v", ui.WarnCalls)
	}
}

// TestRecordToolAgentVersionParseError covers the branch where the agent
// version output cannot be parsed: a warning is emitted and the entry is
// returned unchanged.
func TestRecordToolAgentVersionParseError(t *testing.T) {
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			"opencode --version": msb.NewTestResult(true, 0, "not a version string", "", nil),
		},
	}
	entry := agentUpgradeState{CurrentVersion: "1.0.0"}
	got := recordToolAgentVersion(context.Background(), opencodeAgent(t), sb, ui, entry)
	if got.CurrentVersion != "1.0.0" {
		t.Errorf("expected entry unchanged on parse error, got %+v", got)
	}
	if !contains(joinStrings(ui.WarnCalls), "could not parse agent version") {
		t.Errorf("expected a warning about the parse failure, got %v", ui.WarnCalls)
	}
}

// TestRecordImageProvenanceLoadError covers the branch where the updater state
// cannot be read: a warning is emitted and recording is skipped.
func TestRecordImageProvenanceLoadError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	// Make upgradeStatePath resolve to a directory so os.ReadFile fails with a
	// non-not-exist error, forcing loadUpgradeState to return an error.
	if err := os.MkdirAll(upgradeStatePath(), 0o700); err != nil {
		t.Fatal(err)
	}
	fs := msb.NewTestFS(map[string][]byte{
		"/etc/agents-sandbox/agent-source": []byte("tool\n"),
	}, nil)
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{FSValue_: fs}
	recordImageProvenance(context.Background(), opencodeAgent(t), sb, ui)
	if !contains(joinStrings(ui.WarnCalls), "could not read updater state") {
		t.Errorf("expected a warning about the unreadable updater state, got %v", ui.WarnCalls)
	}
}

// TestRecordImageProvenancePersistError covers the branch where persisting the
// updater state fails after a successful read: a warning is emitted.
func TestRecordImageProvenancePersistError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	// Persist an initial state first so the subsequent loadUpgradeState call
	// succeeds, then make the state directory read-only so the atomic write in
	// saveUpgradeState fails while loading still works.
	if err := saveUpgradeState(upgradeState{}); err != nil {
		t.Fatal(err)
	}
	dir := configpaths.Get().UserStateDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Skipf("cannot chmod state dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	fs := msb.NewTestFS(map[string][]byte{
		"/etc/agents-sandbox/agent-source": []byte("user\n"),
	}, nil)
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{FSValue_: fs}
	recordImageProvenance(context.Background(), opencodeAgent(t), sb, ui)
	if !contains(joinStrings(ui.WarnCalls), "could not persist updater state") {
		t.Errorf("expected a warning about the failed persist, got %v", ui.WarnCalls)
	}
}

func TestFindWorktreeDirNonObjectEntry(t *testing.T) {
	// A numeric entry cannot be unmarshalled into either the string or object
	// form, so it is skipped while the object entry is matched.
	if _, ok := findWorktreeDir(`[123, {"directory": "/w/foo"}]`, "foo"); !ok {
		t.Error("expected to skip the numeric entry and match the object entry")
	}
}

// errOnCmdSandbox is a sandbox stub that returns an error for exactly one
// command and delegates everything else to the wrapped sandbox.
type errOnCmdSandbox struct {
	msb.Sandbox

	cmd string
}

func (s *errOnCmdSandbox) Shell(
	ctx context.Context,
	command string,
	opts ...msbSdk.ExecOption,
) (msb.ShellResult, error) {
	if command == s.cmd {
		return nil, errors.New("shell failed")
	}
	return s.Sandbox.Shell(ctx, command, opts...)
}

// TestResolveTargetCreateShellError covers the branch where the worktree create
// command fails at the Shell level.
func TestResolveTargetCreateShellError(t *testing.T) {
	ui := &termio.Mock{}
	provider := mustWorktreeProvider(t)
	createCmd := provider.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x"})
	base := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(): msb.NewTestResult(true, 0, `[]`, "", nil),
		},
		ShellCalls: &[]string{},
	}
	sb := &errOnCmdSandbox{Sandbox: base, cmd: createCmd}
	_, err := ResolveTarget(context.Background(), opencodeAgent(t), sb, options.WorktreeSpec{Name: "feat-x"}, ui)
	if err == nil {
		t.Fatal("expected error when the worktree create shell command fails")
	}
}

func TestStopOrKillProjectVMDryRunNoRemove(t *testing.T) {
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(&msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatusRunning})
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	err := stopOrKillProjectVM(context.Background(), false, true, &ui, testVMKey(), "stop", "Stopping",
		client, func(_ msb.SandboxHandle, _ context.Context) error { return nil })
	if err != nil {
		t.Fatalf("dry-run without remove: %v", err)
	}
	joined := joinStrings(ui.InfoCalls)
	if !contains(joined, "Would stop") {
		t.Errorf("expected a dry-run info message, got %v", ui.InfoCalls)
	}
	if strings.Contains(joined, "would remove") {
		t.Errorf("dry-run without remove should not mention removing state, got %v", ui.InfoCalls)
	}
}

func TestStopOrKillProjectVMRemoveSuccess(t *testing.T) {
	ui := termio.NewTestMock(t)
	handle := &msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatusRunning}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	err := stopOrKillProjectVM(context.Background(), true, false, &ui, testVMKey(), "stop", "Stopping",
		client, func(h msb.SandboxHandle, c context.Context) error { return h.Stop(c) })
	if err != nil {
		t.Fatalf("remove success: %v", err)
	}
	if !handle.DidRmv {
		t.Error("expected the sandbox handle to be removed")
	}
	if !contains(joinStrings(ui.VerboseCalls), "persisted state removed") {
		t.Errorf("expected a verbose message about removing state, got %v", ui.VerboseCalls)
	}
}

func TestStopOrKillProjectVMRemoveError(t *testing.T) {
	ui := termio.NewTestMock(t)
	handle := &msb.MockSandboxHandle{
		Name_:     "vm",
		Status_:   msbSdk.SandboxStatusRunning,
		RemoveErr: errors.New("remove failed"),
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	err := stopOrKillProjectVM(context.Background(), true, false, &ui, testVMKey(), "stop", "Stopping",
		client, func(h msb.SandboxHandle, c context.Context) error { return h.Stop(c) })
	if err != nil {
		t.Fatalf("remove error should be non-fatal: %v", err)
	}
	if !contains(joinStrings(ui.WarnCalls), "failed to remove sandbox state") {
		t.Errorf("expected a warning about the failed remove, got %v", ui.WarnCalls)
	}
}

// TestSaveUpgradeStateRenameError covers the atomic-rename failure branch where
// the destination already exists as a directory.
func TestSaveUpgradeStateRenameError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := os.MkdirAll(upgradeStatePath(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := saveUpgradeState(upgradeState{}); err == nil {
		t.Fatal("expected error when the rename destination is a directory")
	}
}

// TestCurrentAgentSourceLoadError covers the branch where the updater state
// cannot be read: an empty source is returned.
func TestCurrentAgentSourceLoadError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := os.MkdirAll(upgradeStatePath(), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := currentAgentSource(opencodeAgent(t)); got != "" {
		t.Errorf("currentAgentSource() = %q, want empty on load error", got)
	}
}

// TestEnsureDaemonContextCancelled covers the branch where the poll loop is
// aborted because the context is cancelled while waiting for the daemon to
// become healthy.
func TestEnsureDaemonContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	provider := opencodeProvider(t)
	healthChecks := 0
	orig := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == provider.DaemonHealthCmd() {
			healthChecks++
			if healthChecks == 3 {
				cancel()
			}
			return "", 1, nil
		}
		return "", 0, nil
	})
	defer SetDaemonShellFunc(orig)

	t.Cleanup(func() { daemonPollInterval = 2 * time.Second })
	daemonPollInterval = time.Millisecond

	err := ensureDaemon(ctx, opencodeAgent(t), false, nil, &termio.Mock{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// TestDefaultDaemonShellFuncSuccess covers the default daemonShellFunc success
// path: it forwards stdout and the exit code from sb.Shell.
func TestDefaultDaemonShellFuncSuccess(t *testing.T) {
	orig := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, _ string) (string, int, error) {
		return "", 0, nil
	})
	defer SetDaemonShellFunc(orig)

	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			"cmd": msb.NewTestResult(true, 7, "hello", "", nil),
		},
	}
	// orig is the default daemonShellFunc closure; invoking it directly
	// exercises the production success path.
	stdout, code, err := orig(context.Background(), sb, "cmd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout != "hello" {
		t.Errorf("stdout = %q, want hello", stdout)
	}
	if code != 7 {
		t.Errorf("exit code = %d, want 7", code)
	}
}

// TestCreateProjectVMNetworkError covers the branch where the network policy
// cannot be converted to a config.
func TestCreateProjectVMNetworkError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	client := &msb.MockMsbClient{}
	ui := termio.NewTestMock(t)

	_, _, err := createProjectVM(
		context.Background(),
		client,
		"agents-sandbox-vm-test",
		testVMKey(),
		"agents-sandbox/runner-test:latest",
		"test-home-vol",
		t.TempDir(),
		options.RunOptions{
			Memory:  "1G",
			Network: network.Policy{Profile: network.Profile("bogus-profile")},
		},
		nil,
		&ui,
	)
	if err == nil {
		t.Fatal("expected error when the network policy cannot be converted")
	}
}

// TestCreateProjectVMCreateError covers the branch where CreateSandbox fails.
func TestCreateProjectVMCreateError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	client := &msb.MockMsbClient{CreateSandboxErr: errors.New("create failed")}
	ui := termio.NewTestMock(t)

	_, _, err := createProjectVM(
		context.Background(),
		client,
		"agents-sandbox-vm-test",
		testVMKey(),
		"agents-sandbox/runner-test:latest",
		"test-home-vol",
		t.TempDir(),
		options.RunOptions{Memory: "1G"},
		nil,
		&ui,
	)
	if err == nil {
		t.Fatal("expected error when CreateSandbox fails")
	}
}

// TestAgentLatestVersionDefaultClosureNoChecker covers the default
// agentLatestVersion closure branch for an agent that is not an UpgradeChecker:
// it returns an empty version without error.
func TestAgentLatestVersionDefaultClosureNoChecker(t *testing.T) {
	orig := agentLatestVersion
	defer func() { agentLatestVersion = orig }()
	got, err := orig(context.Background(), &fakeAgent{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("agentLatestVersion for a non-checker = %q, want empty", got)
	}
}

// TestPrepareSandboxUnknownAgent covers the unknown-agent branch of
// PrepareSandbox.
func TestPrepareSandboxUnknownAgent(t *testing.T) {
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)
	ui := termio.NewTestMock(t)

	if _, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "no-such-agent"}, &ui); err == nil {
		t.Fatal("expected error for an unknown agent")
	}
}

// TestCreateProjectVMServeOnlyAndNetwork covers the serve-only and network
// option branches of createProjectVM: port bindings and a network config are
// attached when configured.
func TestCreateProjectVMServeOnlyAndNetwork(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	client := &msb.MockMsbClient{}
	ui := termio.NewTestMock(t)

	_, _, err := createProjectVM(
		context.Background(),
		client,
		"agents-sandbox-vm-test",
		testVMKey(),
		"agents-sandbox/runner-test:latest",
		"test-home-vol",
		t.TempDir(),
		options.RunOptions{
			Memory:    "1G",
			ServeOnly: true,
			Network:   network.Policy{Profile: network.ProfilePublic},
		},
		nil,
		&ui,
	)
	if err != nil {
		t.Fatalf("createProjectVM with serve-only + network: %v", err)
	}
	if len(client.CreatedSandboxCalls) != 1 {
		t.Fatalf("expected 1 create call, got %d", len(client.CreatedSandboxCalls))
	}
	cfg := msbSdk.SandboxConfig{}
	for _, o := range client.CreatedSandboxCalls[0].Opts {
		o(&cfg)
	}
	if len(cfg.PortBindings) == 0 {
		t.Error("expected port bindings for serve-only mode")
	}
	if cfg.Network == nil {
		t.Error("expected a network config to be attached")
	}
}

// TestCreateProjectVMServeOnlyPublishesExactPort covers the real production
// path of createProjectVM where PrepareSandbox has already resolved a non-zero
// opts.ServeHostPort: the created VM must publish exactly that host port.
func TestCreateProjectVMServeOnlyPublishesExactPort(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	client := &msb.MockMsbClient{}
	ui := termio.NewTestMock(t)

	_, _, err := createProjectVM(
		context.Background(),
		client,
		"agents-sandbox-vm-test",
		testVMKey(),
		"agents-sandbox/runner-test:latest",
		"test-home-vol",
		t.TempDir(),
		options.RunOptions{
			Memory:        "1G",
			ServeOnly:     true,
			ServeHostPort: 4097,
		},
		nil,
		&ui,
	)
	if err != nil {
		t.Fatalf("createProjectVM with serve-only + fixed host port: %v", err)
	}
	if len(client.CreatedSandboxCalls) != 1 {
		t.Fatalf("expected 1 create call, got %d", len(client.CreatedSandboxCalls))
	}
	cfg := msbSdk.SandboxConfig{}
	for _, o := range client.CreatedSandboxCalls[0].Opts {
		o(&cfg)
	}
	if len(cfg.PortBindings) != 1 {
		t.Fatalf("expected 1 port binding, got %d", len(cfg.PortBindings))
	}
	pb := cfg.PortBindings[0]
	if pb.HostPort != 4097 || pb.GuestPort != 4096 {
		t.Errorf("binding = host %d guest %d, want host 4097 guest 4096", pb.HostPort, pb.GuestPort)
	}
}

// TestDefaultDaemonShellFuncErrorBranch covers the error branch of the default
// daemonShellFunc closure: a failed sb.Shell yields exit code -1.
func TestDefaultDaemonShellFuncErrorBranch(t *testing.T) {
	orig := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, _ string) (string, int, error) {
		return "", 0, nil
	})
	defer SetDaemonShellFunc(orig)

	sb := &msb.MockSandbox{ShellErr: errors.New("shell failed")}
	stdout, code, err := orig(context.Background(), sb, "cmd")
	if err == nil {
		t.Fatal("expected error from the default daemonShellFunc")
	}
	if code != -1 {
		t.Errorf("exit code = %d, want -1", code)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
}
