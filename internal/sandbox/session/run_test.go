package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestBuildAttachCommand(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	got := buildAttachCommand(a, "/workspace", []string{"foo"})
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
	a, _ := agent.Lookup("opencode")
	got := buildAttachCommand(a, "/home/dev/.local/share/opencode/worktree/abc/feat", nil)
	if !strings.Contains(got, "--dir /home/dev/.local/share/opencode/worktree/abc/feat") {
		t.Errorf("expected worktree dir in command, got %q", got)
	}
}

func TestBuildAttachCommandOpencode(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	got := buildAttachCommand(a, "/workspace/x", nil)
	want := "opencode attach http://127.0.0.1:4096 --dir /workspace/x"
	if got != want {
		t.Errorf("attach = %q, want %q", got, want)
	}
}

// nonAttachAgent implements agent.Agent but not agent.AttachRunner, so
// buildAttachCommand must return "" for it.
type nonAttachAgent struct{}

func (nonAttachAgent) Name() string               { return "nonattach" }
func (nonAttachAgent) ConfigDirName() string      { return "nonattach" }
func (nonAttachAgent) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }

func TestBuildAttachCommandNoAttachRunner(t *testing.T) {
	got := buildAttachCommand(nonAttachAgent{}, "/workspace/x", nil)
	if got != "" {
		t.Errorf("attach = %q, want empty string for non-AttachRunner agent", got)
	}
}

func TestServeOnlyMessage(t *testing.T) {
	msg := serveOnlyMessage("127.0.0.1", "4096")
	for _, want := range []string{"Connect a client to", "http://127.0.0.1:4096", "OPENCODE_SERVER_PASSWORD", "Ctrl-D"} {
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
	go func() { got <- runServeOnly(ctx, sb, ui, 4096) }()
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

func TestRunAttachUsesAttachWithForRoot(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	ui := &termio.Mock{}
	sb := &msb.MockSandbox{AttachCode: 0}

	release, err := state.AcquireClientLease(state.Key{Slug: "roottest", Agent: "opencode"})
	if err != nil {
		t.Fatalf("state.AcquireClientLease: %v", err)
	}
	release()

	err = runAttach(context.Background(), sb, "roottest", ui,
		options.RunOptions{Root: true, ReapPolicy: options.NewReapPolicy(false, 5)}, "-l")
	if err != nil {
		t.Fatalf("runAttach failed: %v", err)
	}
	if sb.AttachUser != "root" {
		t.Errorf("AttachUser = %q, want %q", sb.AttachUser, "root")
	}
}

func TestRunAttachDefaultDoesNotUseRoot(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	ui := &termio.Mock{}
	sb := &msb.MockSandbox{AttachCode: 0}

	release, err := state.AcquireClientLease(state.Key{Slug: "defaulttest", Agent: "opencode"})
	if err != nil {
		t.Fatalf("state.AcquireClientLease: %v", err)
	}
	release()

	err = runAttach(context.Background(), sb, "defaulttest", ui,
		options.RunOptions{ReapPolicy: options.NewReapPolicy(false, 5)}, "-l")
	if err != nil {
		t.Fatalf("runAttach failed: %v", err)
	}
	if sb.AttachUser != "" {
		t.Errorf("AttachUser = %q, want empty for default (non-root) attach", sb.AttachUser)
	}
}

type fakePrepared struct {
	sb            msb.Sandbox
	target        string
	serveHostPort int
}

func (f *fakePrepared) Cleanup()             {}
func (f *fakePrepared) Sandbox() msb.Sandbox { return f.sb }
func (f *fakePrepared) Target() string       { return f.target }
func (f *fakePrepared) ServeHostPort() int   { return f.serveHostPort }

// --- Run: dry-run ---

func TestRunDryRun(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{}, nil
	}
	defer func() { prepareSandbox = orig }()

	err := Run(context.Background(), options.RunOptions{DryRun: true}, ui)
	if err != nil {
		t.Fatalf("Run dry-run: %v", err)
	}
	if len(ui.InfoCalls) == 0 {
		t.Fatal("expected info output")
	}
	if ui.InfoCalls[0] != "dry-run: Would run agent session" {
		t.Errorf("info = %q, want dry-run message", ui.InfoCalls[0])
	}
}

func TestRunDryRunVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{}, nil
	}
	defer func() { prepareSandbox = orig }()

	err := Run(context.Background(), options.RunOptions{DryRunVM: true}, ui)
	if err != nil {
		t.Fatalf("Run dry-run-vm: %v", err)
	}
	if len(ui.InfoCalls) == 0 || ui.InfoCalls[0] != "dry-run: Would start agent session in VM" {
		t.Errorf("info = %v, want dry-run-vm message", ui.InfoCalls)
	}
}

func TestRunServeOnlyPath(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: msb.NewMockSandbox(msb.SandboxOpts{})}, nil
	}
	defer func() { prepareSandbox = orig }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, options.RunOptions{ServeOnly: true, ReapPolicy: options.NewReapPolicy(true, 5)}, ui)
	}()
	cancel()
	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run serve-only did not return after cancel")
	}
	if err == nil {
		t.Fatal("expected ExitError from serve-only path")
	}
	var exitErr *sandbox.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 0 {
		t.Errorf("err = %v, want ExitError code 0", err)
	}
}

func TestRunServeOnlyUsesSessionServeHostPort(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{
			sb:            msb.NewMockSandbox(msb.SandboxOpts{}),
			serveHostPort: 4099,
		}, nil
	}
	defer func() { prepareSandbox = orig }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, options.RunOptions{ServeOnly: true, ReapPolicy: options.NewReapPolicy(true, 5)}, ui)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run serve-only did not return after cancel")
	}
	found := false
	for _, call := range ui.InfoCalls {
		if strings.Contains(call, "http://127.0.0.1:4099") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected connect URL with session port 4099 in info output, got %v", ui.InfoCalls)
	}
}

func TestRunPrepareSandboxError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, termio.UI) (preparedSandbox, error) {
		return nil, errors.New("prepare boom")
	}
	defer func() { prepareSandbox = orig }()

	err := Run(context.Background(), options.RunOptions{}, ui)
	if err == nil || !strings.Contains(err.Error(), "prepare boom") {
		t.Errorf("err = %v, want prepareSandbox error", err)
	}
}

func TestRunServeOnlyReapFailureWarns(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{Name_: "test-vm"}
	sb.ShellErr = errors.New("shell failed")
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: sb}, nil
	}
	defer func() { prepareSandbox = orig }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, options.RunOptions{ServeOnly: true, ReapPolicy: options.NewReapPolicy(false, 5)}, ui)
	}()
	cancel()
	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run serve-only did not return after cancel")
	}
	if err == nil {
		t.Fatal("expected ExitError from serve-only path")
	}
	var exitErr *sandbox.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 0 {
		t.Errorf("err = %v, want ExitError code 0", err)
	}
	found := false
	for _, w := range ui.WarnCalls {
		if strings.Contains(w, "reap failed") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected reap warning, got %v", ui.WarnCalls)
	}
}

func TestShellPrepareSandboxError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, termio.UI) (preparedSandbox, error) {
		return nil, errors.New("prepare boom")
	}
	defer func() { prepareSandbox = orig }()

	err := Shell(context.Background(), options.RunOptions{}, ui)
	if err == nil || !strings.Contains(err.Error(), "prepare boom") {
		t.Errorf("err = %v, want prepareSandbox error", err)
	}
}

func TestRunNormalCallsRunAttach(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{AttachCode: 0, Name_: "test-vm"}
	sb.ShellOut = map[string]msb.ShellResult{
		"curl -sf http://127.0.0.1:4096/session/status": msb.NewTestResult(
			true, 0, `{"s1":{"type":"idle","attempt":0}}`, "", nil,
		),
	}
	sb.ExecOut = map[string]msb.ShellResult{
		"sleep 1h": msb.NewTestResult(true, 0, "", "", nil),
	}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: sb, target: "/workspace"}, nil
	}
	defer func() { prepareSandbox = orig }()

	err := Run(context.Background(), options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)}, ui)
	if err != nil {
		t.Fatalf("Run normal: %v", err)
	}
	found := false
	for _, v := range ui.VerboseCalls {
		if strings.Contains(v, "opencode attach") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected attach command in verbose output, got %v", ui.VerboseCalls)
	}
}

// --- Shell ---

func TestShellDryRun(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{}, nil
	}
	defer func() { prepareSandbox = orig }()

	err := Shell(context.Background(), options.RunOptions{DryRun: true}, ui)
	if err != nil {
		t.Fatalf("Shell dry-run: %v", err)
	}
	if len(ui.InfoCalls) == 0 || ui.InfoCalls[0] != "dry-run: Would start interactive shell session" {
		t.Errorf("info = %v, want dry-run shell message", ui.InfoCalls)
	}
}

func TestShellDryRunVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{}, nil
	}
	defer func() { prepareSandbox = orig }()

	err := Shell(context.Background(), options.RunOptions{DryRunVM: true}, ui)
	if err != nil {
		t.Fatalf("Shell dry-run-vm: %v", err)
	}
	if len(ui.InfoCalls) == 0 || ui.InfoCalls[0] != "dry-run: Would start interactive shell session" {
		t.Errorf("info = %v, want dry-run shell message", ui.InfoCalls)
	}
}

func TestShellNormalCallsRunAttach(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{AttachCode: 0, Name_: "test-vm"}
	sb.ShellOut = map[string]msb.ShellResult{
		"curl -sf http://127.0.0.1:4096/session/status": msb.NewTestResult(
			true, 0, `{"s1":{"type":"idle","attempt":0}}`, "", nil,
		),
	}
	sb.ExecOut = map[string]msb.ShellResult{
		"sleep 1h": msb.NewTestResult(true, 0, "", "", nil),
	}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: sb, target: "/workspace"}, nil
	}
	defer func() { prepareSandbox = orig }()

	err := Shell(context.Background(), options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)}, ui)
	if err != nil {
		t.Fatalf("Shell normal: %v", err)
	}
}

// --- runAttach error paths ---

func TestRunAttachPropagatesAttachError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{AttachErr: errors.New("attach boom")}

	err := runAttach(context.Background(), sb, "errproj", ui,
		options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)}, "-l")
	if err == nil {
		t.Fatal("expected error when attach fails")
	}
	if !strings.Contains(err.Error(), "agent session failed") {
		t.Errorf("err = %q, want wrapped attach failure", err.Error())
	}
}

func TestRunAttachReturnsExitErrorForNonZeroCode(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{AttachCode: 3}

	err := runAttach(context.Background(), sb, "exitproj", ui,
		options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)}, "-l")
	var exitErr *sandbox.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Errorf("err = %v, want ExitError code 3", err)
	}
}

func TestRunAttachWarnsOnReapFailure(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{AttachCode: 0, Name_: "test-vm"}
	sb.ShellErr = errors.New("shell failed")

	// A cancelled context makes waitQuiescent abort on its next poll, so
	// reapOnLastClient returns an error that runAttach must warn about.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := runAttach(ctx, sb, "reaperrproj", ui,
		options.RunOptions{ReapPolicy: options.NewReapPolicy(false, 5)}, "-l")
	if err != nil {
		t.Fatalf("runAttach should not fail on reap error: %v", err)
	}
	found := false
	for _, w := range ui.WarnCalls {
		if strings.Contains(w, "reap failed") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected reap warning, got %v", ui.WarnCalls)
	}
}

func TestFinalizeRun(t *testing.T) {
	if err := finalizeRun(nil, 0); err != nil {
		t.Errorf("finalizeRun(nil, 0) = %v, want nil", err)
	}
	if err := finalizeRun(errors.New("boom"), 1); err == nil {
		t.Error("finalizeRun(error, code) should return wrapped error")
	} else if !strings.Contains(err.Error(), "agent session failed") {
		t.Errorf("finalizeRun error = %q, want wrapped session failure", err.Error())
	}
	err := finalizeRun(nil, 3)
	var exitErr *sandbox.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Errorf("finalizeRun(nil, 3) = %v, want ExitError code 3", err)
	}
}

func TestRunStartsNotifyWatcher(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	origPrepare := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: msb.NewMockSandbox(msb.SandboxOpts{AttachCode: 0})}, nil
	}
	defer func() { prepareSandbox = origPrepare }()

	origWatch := notifyWatch
	defer func() { notifyWatch = origWatch }()
	started := make(chan struct{})
	notifyWatch = func(_ context.Context, _ msb.Sandbox, _ agent.EventStreamSpec, _ notify.Backend) error {
		close(started)
		return nil
	}

	err := Run(context.Background(), options.RunOptions{
		ReapPolicy: options.NewReapPolicy(true, 5),
		Notify: notify.Config{
			Desktop: true, Audio: notify.AudioOff,
			OnInput: true, OnDone: true, OnError: true,
		},
	}, ui)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("notify watcher was not started for active notify config")
	}
}

func TestRunDoesNotStartNotifyWatcherWhenInactive(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	origPrepare := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: msb.NewMockSandbox(msb.SandboxOpts{AttachCode: 0})}, nil
	}
	defer func() { prepareSandbox = origPrepare }()

	origWatch := notifyWatch
	defer func() { notifyWatch = origWatch }()
	called := false
	notifyWatch = func(_ context.Context, _ msb.Sandbox, _ agent.EventStreamSpec, _ notify.Backend) error {
		called = true
		return nil
	}

	err := Run(context.Background(), options.RunOptions{
		ReapPolicy: options.NewReapPolicy(true, 5),
	}, ui)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called {
		t.Error("notify watcher should not start when notify config is inactive")
	}
}

func TestStartNotifyWatcherNoOpWithoutSpec(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	origWatch := notifyWatch
	defer func() { notifyWatch = origWatch }()
	called := false
	notifyWatch = func(_ context.Context, _ msb.Sandbox, _ agent.EventStreamSpec, _ notify.Backend) error {
		called = true
		return nil
	}

	stop := startNotifyWatcher(
		context.Background(),
		msb.NewMockSandbox(msb.SandboxOpts{}),
		notify.Config{
			Desktop: true,
			Audio:   notify.AudioOff,
			OnInput: true,
			OnDone:  true,
			OnError: true,
		},
		ui,
		nil,
		"slug",
	)
	stop()
	if called {
		t.Error("startNotifyWatcher should be a no-op without an EventStreamSpec")
	}
}

func TestStartNotifyWatcherUsesSpec(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	origWatch := notifyWatch
	defer func() { notifyWatch = origWatch }()
	got := make(chan agent.EventStreamSpec, 1)
	notifyWatch = func(_ context.Context, _ msb.Sandbox, spec agent.EventStreamSpec, _ notify.Backend) error {
		got <- spec
		return nil
	}
	spec := agent.EventStreamSpec{
		StreamCommand: "curl -N -s http://127.0.0.1:9999/events",
		BusyEvents:    []string{"busy.evt"},
		AwaitingInput: []string{"ask.evt"},
		IdleEvents:    []string{"idle.evt"},
		ErrorEvents:   []string{"fail.evt"},
		Name:          "pi",
	}
	stop := startNotifyWatcher(
		context.Background(),
		msb.NewMockSandbox(msb.SandboxOpts{}),
		notify.Config{
			Desktop: true,
			Audio:   notify.AudioOff,
			OnInput: true,
			OnDone:  true,
			OnError: true,
		},
		ui,
		&spec,
		"slug",
	)
	defer stop()
	select {
	case received := <-got:
		if received.StreamCommand != spec.StreamCommand || received.Name != spec.Name {
			t.Errorf("notifyWatch spec = %+v, want %+v", received, spec)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("notifyWatch was not called")
	}
}

func TestRunLogsNotifyWatcherErrorAfterStop(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	origPrepare := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: msb.NewMockSandbox(msb.SandboxOpts{AttachCode: 0})}, nil
	}
	defer func() { prepareSandbox = origPrepare }()

	origWatch := notifyWatch
	defer func() { notifyWatch = origWatch }()
	notifyWatch = func(_ context.Context, _ msb.Sandbox, _ agent.EventStreamSpec, _ notify.Backend) error {
		return errors.New("watch boom")
	}

	err := Run(context.Background(), options.RunOptions{
		ReapPolicy: options.NewReapPolicy(true, 5),
		Notify: notify.Config{
			Desktop: true, Audio: notify.AudioOff,
			OnInput: true, OnDone: true, OnError: true,
		},
	}, ui)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, v := range ui.VerboseCalls {
		if strings.Contains(v, "notify watcher") && strings.Contains(v, "watch boom") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected notify watcher error logged via Verbosef after stop")
	}
}

func TestRunDoesNotStartNotifyWatcherWithoutEventStreamProvider(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	origPrepare := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: msb.NewMockSandbox(msb.SandboxOpts{AttachCode: 0})}, nil
	}
	defer func() { prepareSandbox = origPrepare }()

	origWatch := notifyWatch
	defer func() { notifyWatch = origWatch }()
	called := false
	notifyWatch = func(_ context.Context, _ msb.Sandbox, _ agent.EventStreamSpec, _ notify.Backend) error {
		called = true
		return nil
	}

	err := Run(context.Background(), options.RunOptions{
		Agent:      "pi",
		ReapPolicy: options.NewReapPolicy(true, 5),
		Notify: notify.Config{
			Desktop: true, Audio: notify.AudioOff,
			OnInput: true, OnDone: true, OnError: true,
		},
	}, ui)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called {
		t.Error("notify watcher should not start for an agent without EventStreamProvider")
	}
}
