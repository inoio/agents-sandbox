package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

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

// TestDetectToolAgentVersionNotProvider covers the branch where the agent does
// not implement VersionProvider: no version is detected.
func TestDetectToolAgentVersionNotProvider(t *testing.T) {
	version, ok := detectToolAgentVersion(
		context.Background(),
		agent.Resolve(&fakeAgent{}),
		&msb.MockSandbox{},
		&termio.Mock{},
	)
	if ok || version != "" {
		t.Errorf("expected no detection, got (%q, %v)", version, ok)
	}
}

// TestDetectToolAgentVersionShellError covers the branch where detecting the
// agent version via Shell fails: a warning is emitted and no version is
// detected.
func TestDetectToolAgentVersionShellError(t *testing.T) {
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{ShellErr: errors.New("shell failed")}
	version, ok := detectToolAgentVersion(context.Background(), opencodeResolved(t), sb, ui)
	if ok || version != "" {
		t.Errorf("expected no detection on shell error, got (%q, %v)", version, ok)
	}
	if !contains(joinStrings(ui.WarnCalls), "could not detect agent version") {
		t.Errorf("expected a warning about the failed version detection, got %v", ui.WarnCalls)
	}
}

// TestDetectToolAgentVersionParseError covers the branch where the agent
// version output cannot be parsed: a warning is emitted and no version is
// detected.
func TestDetectToolAgentVersionParseError(t *testing.T) {
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			"opencode --version": msb.NewTestResult(true, 0, "not a version string", "", nil),
		},
	}
	version, ok := detectToolAgentVersion(context.Background(), opencodeResolved(t), sb, ui)
	if ok || version != "" {
		t.Errorf("expected no detection on parse error, got (%q, %v)", version, ok)
	}
	if !contains(joinStrings(ui.WarnCalls), "could not parse agent version") {
		t.Errorf("expected a warning about the parse failure, got %v", ui.WarnCalls)
	}
}

// TestRecordImageProvenanceLoadError covers the branch where the project state
// cannot be read: a warning is emitted and recording is skipped.
func TestRecordImageProvenanceLoadError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	// Make the state file resolve to a directory so os.ReadFile fails with a
	// non-not-exist error, forcing ReadState to return an error.
	if err := os.MkdirAll(filepath.Join(state.KeyDir(k), "state.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}
	fs := msb.NewTestFS(map[string][]byte{
		"/etc/agents-sandbox/agent-source": []byte("tool\n"),
	}, nil)
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{FSValue_: fs}
	recordImageProvenance(context.Background(), k, opencodeResolved(t), sb, ui)
	if !contains(joinStrings(ui.WarnCalls), "read state for transaction") {
		t.Errorf("expected a warning about the unreadable project state, got %v", ui.WarnCalls)
	}
}

// TestRecordImageProvenancePersistError covers the branch where persisting the
// project state fails after a successful read: a warning is emitted.
func TestRecordImageProvenancePersistError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	// Persist an initial state first so the subsequent ReadState call succeeds,
	// then make the state directory read-only so the atomic write fails while
	// loading still works.
	if err := seedHomeState(k, state.NewHomeState("vol", "digest")); err != nil {
		t.Fatal(err)
	}
	dir := state.KeyDir(k)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Skipf("cannot chmod state dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	fs := msb.NewTestFS(map[string][]byte{
		"/etc/agents-sandbox/agent-source": []byte("user\n"),
	}, nil)
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{FSValue_: fs}
	recordImageProvenance(context.Background(), k, opencodeResolved(t), sb, ui)
	if !contains(joinStrings(ui.WarnCalls), "could not persist project state") {
		t.Errorf("expected a warning about the failed persist, got %v", ui.WarnCalls)
	}
}

func TestFindWorktreeDirSkipsNonMatching(t *testing.T) {
	// A non-matching entry is skipped so a later one can match.
	dir, ok := findWorktreeDir([]string{"/w/bar", "/w/foo"}, "foo")
	if !ok || dir != "/w/foo" {
		t.Errorf("findWorktreeDir = %q, %v; want /w/foo, true", dir, ok)
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
	createCmd := provider.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x"}, agent.WorktreeProject{})
	base := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(agent.WorktreeProject{}): msb.NewTestResult(true, 0, `[]`, "", nil),
		},
		ShellCalls: &[]string{},
	}
	sb := &errOnCmdSandbox{Sandbox: base, cmd: createCmd}
	_, err := ResolveTarget(
		context.Background(),
		opencodeResolved(t),
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	)
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

func TestStopOrKillProjectVMRejectsUnsafeStatuses(t *testing.T) {
	for _, status := range []msbSdk.SandboxStatus{
		msbSdk.SandboxStatusPaused,
		msbSdk.SandboxStatus("future-status"),
	} {
		t.Run(string(status), func(t *testing.T) {
			ui := termio.NewTestMock(t)
			client := &msb.MockMsbClient{}
			client.SetGotSandbox(&msb.MockSandboxHandle{Name_: "vm", Status_: status})
			err := stopOrKillProjectVM(
				context.Background(),
				false,
				false,
				&ui,
				testVMKey(),
				"stop",
				"Stopping",
				client,
				func(msb.SandboxHandle, context.Context) error { return nil },
			)
			if err == nil {
				t.Fatalf("expected unsafe status %q to be rejected", status)
			}
		})
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
	orig := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == provider.DaemonHealthCmd() {
			healthChecks++
			if healthChecks == 3 {
				cancel()
			}
			return "", 1, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(orig) })

	origPoll := daemonPollInterval
	t.Cleanup(func() { daemonPollInterval = origPoll })
	daemonPollInterval = time.Millisecond

	err := ensureDaemon(ctx, opencodeResolved(t), false, false, &msb.MockSandbox{}, &termio.Mock{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// TestDefaultDaemonHealthFuncSuccess covers the default daemonHealthFunc success
// path: it forwards stdout and the exit code from sb.Shell.
func TestDefaultDaemonHealthFuncSuccess(t *testing.T) {
	orig := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, _ string) (string, int, error) {
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(orig) })

	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			"cmd": msb.NewTestResult(true, 7, "hello", "", nil),
		},
	}
	// orig is the default daemonHealthFunc closure; invoking it directly
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

	_, _, err := createProjectVM(context.Background(), ProjectVMCreation{
		Client:       client,
		DockerClient: docker.Get(),
		Options: options.RunOptions{
			Memory:  "1G",
			Network: network.Policy{Profiles: []network.Profile{network.Profile("bogus-profile")}},
		},
		Image: image.ImageInfo{
			DockerTag: "agents-sandbox/runner-test:latest",
			Ref:       "agents-sandbox/runner-test:latest",
		},
		HomeVolume: "test-home-vol",
		RepoPath:   t.TempDir(),
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		Env:        envsecret.EnvSecrets{},
		UI:         &ui,
	}, "agents-sandbox-vm-test")
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

	_, _, err := createProjectVM(context.Background(), ProjectVMCreation{
		Client:       client,
		DockerClient: docker.Get(),
		Options:      options.RunOptions{Memory: "1G"},
		Image: image.ImageInfo{
			DockerTag: "agents-sandbox/runner-test:latest",
			Ref:       "agents-sandbox/runner-test:latest",
		},
		HomeVolume: "test-home-vol",
		RepoPath:   t.TempDir(),
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		Env:        envsecret.EnvSecrets{},
		UI:         &ui,
	}, "agents-sandbox-vm-test")
	if err == nil {
		t.Fatal("expected error when CreateSandbox fails")
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

	_, _, err := createProjectVM(context.Background(), ProjectVMCreation{
		Client:       client,
		DockerClient: docker.Get(),
		Options: options.RunOptions{
			Memory:    "1G",
			ServeOnly: true,
			Network:   network.Policy{Profiles: []network.Profile{network.ProfilePublic}},
		},
		Image: image.ImageInfo{
			DockerTag: "agents-sandbox/runner-test:latest",
			Ref:       "agents-sandbox/runner-test:latest",
		},
		HomeVolume: "test-home-vol",
		RepoPath:   t.TempDir(),
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		Env:        envsecret.EnvSecrets{},
		UI:         &ui,
	}, "agents-sandbox-vm-test")
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

	_, _, err := createProjectVM(context.Background(), ProjectVMCreation{
		Client:       client,
		DockerClient: docker.Get(),
		Options: options.RunOptions{
			Memory:        "1G",
			ServeOnly:     true,
			ServeHostPort: 4097,
		},
		Image: image.ImageInfo{
			DockerTag: "agents-sandbox/runner-test:latest",
			Ref:       "agents-sandbox/runner-test:latest",
		},
		HomeVolume: "test-home-vol",
		RepoPath:   t.TempDir(),
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		Env:        envsecret.EnvSecrets{},
		UI:         &ui,
	}, "agents-sandbox-vm-test")
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

// TestDefaultDaemonHealthFuncErrorBranch covers the error branch of the default
// daemonHealthFunc closure: a failed sb.Shell yields exit code -1.
func TestDefaultDaemonHealthFuncErrorBranch(t *testing.T) {
	orig := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, _ string) (string, int, error) {
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(orig) })

	sb := &msb.MockSandbox{ShellErr: errors.New("shell failed")}
	stdout, code, err := orig(context.Background(), sb, "cmd")
	if err == nil {
		t.Fatal("expected error from the default daemonHealthFunc")
	}
	if code != -1 {
		t.Errorf("exit code = %d, want -1", code)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
}
