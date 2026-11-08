package session

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/opener"
	"github.com/inoio/agents-sandbox/internal/sandbox/lifecycle"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/prepare"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

func TestStartNotifyWatcherReturnsWatchError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	origWatch := notifyWatch
	defer func() { notifyWatch = origWatch }()
	notifyWatch = func(_ context.Context, _ notify.StreamOpener, _ agent.EventStreamSpec, _ notify.Backend) error {
		return errors.New("watch boom")
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
		notify.Config{Desktop: true, Audio: notify.AudioOff, OnInput: true, OnDone: true, OnError: true},
		ui,
		&spec,
		"slug",
	)
	err := stop()
	if err == nil || !strings.Contains(err.Error(), "watch boom") {
		t.Fatalf("stop() error = %v, want 'watch boom'", err)
	}
}

// activeNotifyConfig enables notifications on the desktop for every trigger.
func activeNotifyConfig() notify.Config {
	return notify.Config{Desktop: true, Audio: notify.AudioOff, OnInput: true, OnDone: true, OnError: true}
}

// TestStartNotifyWatcherNeedsASandboxConfigAndSpec pins that the watcher
// starts only with a sandbox, an active notify config and an event stream
// spec; without any of them the returned stop does nothing.
func TestStartNotifyWatcherNeedsASandboxConfigAndSpec(t *testing.T) {
	spec := &agent.EventStreamSpec{Name: "pi"}
	cases := []struct {
		name string
		sb   msb.Sandbox
		cfg  notify.Config
		spec *agent.EventStreamSpec
	}{
		{"no sandbox", nil, activeNotifyConfig(), spec},
		{"inactive config", msb.NewMockSandbox(msb.SandboxOpts{}), notify.Config{}, spec},
		{"no spec", msb.NewMockSandbox(msb.SandboxOpts{}), activeNotifyConfig(), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configpathstest.WithMockConfigPaths(t)
			origWatch := notifyWatch
			t.Cleanup(func() { notifyWatch = origWatch })
			notifyWatch = func(context.Context, notify.StreamOpener, agent.EventStreamSpec, notify.Backend) error {
				t.Error("the notify watcher started")
				return nil
			}

			stop := startNotifyWatcher(context.Background(), tc.sb, tc.cfg, &termiotest.Mock{}, tc.spec, "slug")
			if err := stop(); err != nil {
				t.Errorf("stop() = %v, want nil", err)
			}
		})
	}
}

// TestStartNotifyWatcherDedupsForAKnownProject pins that a watcher for a known
// project slug notifies through the cross-client dedup, so two clients of one
// project do not both alert, and that stop cancels the running watch.
func TestStartNotifyWatcherDedupsForAKnownProject(t *testing.T) {
	for _, tc := range []struct {
		slug      string
		wantDedup bool
	}{{"slug", true}, {"", false}} {
		t.Run("slug="+tc.slug, func(t *testing.T) {
			configpathstest.WithMockConfigPaths(t)
			origWatch := notifyWatch
			t.Cleanup(func() { notifyWatch = origWatch })
			backends := make(chan notify.Backend, 1)
			notifyWatch = func(ctx context.Context, _ notify.StreamOpener, _ agent.EventStreamSpec, b notify.Backend) error {
				backends <- b
				<-ctx.Done()
				return nil
			}

			stop := startNotifyWatcher(context.Background(), msb.NewMockSandbox(msb.SandboxOpts{}),
				activeNotifyConfig(), &termiotest.Mock{}, &agent.EventStreamSpec{Name: "pi"}, tc.slug)
			_, isDedup := (<-backends).(*notify.Dedup)
			stopped := make(chan error, 1)
			go func() { stopped <- stop() }()
			select {
			case <-stopped:
			case <-time.After(2 * time.Second):
				t.Fatal("stop() did not cancel the watch")
			}
			if isDedup != tc.wantDedup {
				t.Errorf("dedup backend = %v, want %v", isDedup, tc.wantDedup)
			}
		})
	}
}

func TestStartNotifyWatcherUsesSpec(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	origWatch := notifyWatch
	defer func() { notifyWatch = origWatch }()
	got := make(chan agent.EventStreamSpec, 1)
	notifyWatch = func(_ context.Context, _ notify.StreamOpener, spec agent.EventStreamSpec, _ notify.Backend) error {
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

func TestBuildAttachCommand(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	got := buildAttachCommand(agent.Resolve(a), "/workspace", []string{"foo"})
	if !strings.Contains(got, "opencode attach") {
		t.Errorf("expected 'opencode attach' in command, got %q", got)
	}
	if !strings.Contains(got, "http://127.0.0.1:4096") {
		t.Errorf("expected daemon URL in command, got %q", got)
	}
	if !strings.Contains(got, "--dir '/workspace'") {
		t.Errorf("expected --dir '/workspace' in command, got %q", got)
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
	got := buildAttachCommand(agent.Resolve(a), "/home/dev/.local/share/opencode/worktree/abc/feat", nil)
	if !strings.Contains(got, "--dir '/home/dev/.local/share/opencode/worktree/abc/feat'") {
		t.Errorf("expected worktree dir in command, got %q", got)
	}
}

func TestBuildAttachCommandQuotesTargetWithSpaces(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	got := buildAttachCommand(agent.Resolve(a), "/workspace/my notes", nil)
	if !strings.Contains(got, "--dir '/workspace/my notes'") {
		t.Errorf("expected the quoted target to keep the space, got %q", got)
	}
}

func TestBuildAttachCommandOpencode(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	got := buildAttachCommand(agent.Resolve(a), "/workspace/x", nil)
	want := "opencode attach http://127.0.0.1:4096 --dir '/workspace/x'"
	if got != want {
		t.Errorf("attach = %q, want %q", got, want)
	}
}

func TestBuildAttachCommandNoAttachRunner(t *testing.T) {
	got := buildAttachCommand(agent.Resolve(nonAttachAgent{}), "/workspace/x", nil)
	if got != "" {
		t.Errorf("attach = %q, want empty string for non-AttachRunner agent", got)
	}
}

func TestServeOnlyMessage(t *testing.T) {
	opencode, _ := agent.Lookup("opencode")
	msg := serveOnlyMessage(agent.Resolve(opencode), "127.0.0.1", "4096")
	for _, want := range []string{"Connect a client to", "http://127.0.0.1:4096", "OPENCODE_SERVER_PASSWORD", "Ctrl-D"} {
		if !strings.Contains(msg, want) {
			t.Errorf("serveOnlyMessage missing %q in:\n%s", want, msg)
		}
	}
}

// TestServeOnlyMessageOpencode2StatesCredential verifies the serve-only guidance
// tells an external client how to authenticate to opencode2: its basic auth is
// mandatory with a baked credential, so the old opt-in/env wording would be
// wrong and would leave the client unable to connect.
func TestServeOnlyMessageOpencode2StatesCredential(t *testing.T) {
	opencode2, _ := agent.Lookup("opencode2")
	msg := serveOnlyMessage(agent.Resolve(opencode2), "127.0.0.1", "4096")
	for _, want := range []string{"http://127.0.0.1:4096", `user "opencode"`, `password "opencode"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("serveOnlyMessage(opencode2) missing %q in:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "Optional:") {
		t.Errorf("serveOnlyMessage(opencode2) must not offer optional auth:\n%s", msg)
	}
}

func TestRunServeOnlyBlocksUntilCancel(t *testing.T) {
	sb := msb.NewMockSandbox(msb.SandboxOpts{})
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan error, 1)
	ui := &termiotest.Mock{}
	go func() { got <- runServeOnly(ctx, opencodeResolved(t), sb, ui, 4096) }()
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

func TestRunServeOnlyClientLeaseFailureWarns(t *testing.T) {
	key := state.Key{Slug: "serveonlyleasefail", Agent: "opencode"}
	blockClientLease(t, key.Slug)
	ui := &termiotest.Mock{}
	sb := msb.NewMockSandbox(msb.SandboxOpts{})
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: sb, key: key}, nil
	}
	defer func() { prepareSandbox = orig }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, resolvedRunOptions(t, options.RunOptions{ServeOnly: true, ReapPolicy: options.NewReapPolicy(true, 5)}), options.ModeRun, ui)
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
		if strings.Contains(w, "client lease failed") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected client lease warning, got %v", ui.WarnCalls)
	}
}

// terminalStdoutUI is a termiotest.Mock whose stdout is a pty, so the attach path
// sees a real terminal and emits its reset.
type terminalStdoutUI struct {
	*termiotest.Mock

	stdout *os.File
}

func (u terminalStdoutUI) StdOut() io.Writer { return u.stdout }

// newTerminalStdoutUI returns a UI writing stdout to a pty and a function that
// closes the terminal side and returns everything written to it.
func newTerminalStdoutUI(t *testing.T) (terminalStdoutUI, func() string) {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	t.Cleanup(func() { _ = master.Close(); _ = slave.Close() })
	return terminalStdoutUI{Mock: &termiotest.Mock{}, stdout: slave}, func() string {
		_ = slave.Close()
		written, _ := io.ReadAll(master)
		return string(written)
	}
}

// TestRunAttachEmitsTerminalResetAfterAbnormalEnd pins that a killed harness
// (exit code -1) leaves no emulator-mode residue: the reset reaches the terminal.
func TestRunAttachEmitsTerminalResetAfterAbnormalEnd(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	// A pty-backed stdout exercises the production TTY predicate end to end;
	// the forced-true seam path is covered by TestResetTerminalWritesSequenceToTerminal.
	ui, written := newTerminalStdoutUI(t)
	sb := &msb.MockSandbox{AttachCode: -1}

	release, err := state.AcquireClientLease(state.Key{Slug: "resetattach", Agent: "opencode"})
	if err != nil {
		t.Fatalf("state.AcquireClientLease: %v", err)
	}
	release()

	_ = runAttach(context.Background(), state.Key{Slug: "resetattach", Agent: "opencode"}, sb, "/workspace/sub", ui,
		resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(false, 5)}), "-l")
	if got := written(); !strings.Contains(got, terminalReset) {
		t.Fatalf("terminal received %q, want the terminal reset sequence", got)
	}
}

// TestRunAttachSkipsTerminalResetAfterCleanExit pins that a harness that exits
// normally restored its own terminal modes, so no reset is sent: leaving the
// alternate screen restores a saved cursor (DECRC) on xterm-family terminals,
// which could move the cursor over earlier output.
func TestRunAttachSkipsTerminalResetAfterCleanExit(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui, written := newTerminalStdoutUI(t)
	sb := &msb.MockSandbox{AttachCode: 0}

	if err := runAttach(context.Background(), state.Key{Slug: "resetclean", Agent: "opencode"}, sb, "/workspace", ui,
		resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(false, 5)}), "-l"); err != nil {
		t.Fatalf("runAttach failed: %v", err)
	}
	if got := written(); strings.Contains(got, terminalReset) {
		t.Fatalf("terminal received the reset %q after a clean exit, want none", got)
	}
}

// TestRunAttachSkipsTerminalResetForNonTerminal pins the Greptile fix at the
// attach boundary: when stdout is not a terminal (a piped or captured run) the
// attach path must not write raw escape bytes into it.
func TestRunAttachSkipsTerminalResetForNonTerminal(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	stubIsTerminalWriter(t, false)

	ui := &termiotest.Mock{}
	sb := &msb.MockSandbox{AttachCode: -1}

	_ = runAttach(context.Background(), state.Key{Slug: "resetpipe", Agent: "opencode"}, sb, "/workspace/sub", ui,
		resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(false, 5)}), "-l")
	if got := ui.StdOutBuffer.String(); got != "" {
		t.Fatalf("StdOut = %q, want no reset for a non-terminal", got)
	}
}

// TestRunUsesSessionKeyForLease pins the session-key wiring: Run keys the client
// lease (and therefore the reap) off the key PrepareSandbox prepared instead of
// re-resolving identity from the cwd, so the lease lands under the session's
// checkout slug. The lease file is removed on release, but its clients/
// directory remains, which is what the assertion observes.
func TestRunUsesSessionKeyForLease(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	key := state.Key{Slug: "sessionkeyslug", Agent: "opencode"}
	ui := &termiotest.Mock{}
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
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: sb, target: "/workspace", key: key}, nil
	}
	defer func() { prepareSandbox = orig }()

	if err := Run(
		context.Background(),
		resolvedRunOptions(t, options.RunOptions{Agent: "opencode", ReapPolicy: options.NewReapPolicy(true, 5)}),
		options.ModeRun,
		ui,
	); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(state.KeyDir(key), "clients")); statErr != nil {
		t.Errorf("client lease did not land under the session key dir %s: %v", state.KeyDir(key), statErr)
	}

	ambientSlug := git.MustCheckoutSlug()
	if ambientSlug == key.Slug {
		t.Fatalf("fixture session slug %q must differ from the ambient slug", key.Slug)
	}
	ambientKeyDir := filepath.Join(state.KeyDir(state.Key{Slug: ambientSlug, Agent: "opencode"}), "clients")
	if _, statErr := os.Stat(ambientKeyDir); statErr == nil {
		t.Errorf("client lease landed under the ambient checkout slug %q; Run re-resolved identity", ambientSlug)
	}
}

func TestRunServeOnlyReturnsDeadlineExceeded(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	sb := msb.NewMockSandbox(msb.SandboxOpts{})
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: sb, serveHostPort: 4096}, nil
	}
	defer func() { prepareSandbox = orig }()

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(100*time.Millisecond))
	defer cancel()

	err := Run(
		ctx,
		resolvedRunOptions(t, options.RunOptions{ServeOnly: true, ReapPolicy: options.NewReapPolicy(true, 5)}),
		options.ModeRun,
		ui,
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestRunDryRunRendersPreview(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	stubPreviewSandbox(t, prepare.Preview{
		AgentVersion: "1.0.0",
		ImageTag:     "agents-sandbox-runner-x",
		BuildImage:   true,
		BuildReasons: []string{"runner image not present locally"},
		CreateHome:   true,
	})

	err := Run(context.Background(), resolvedRunOptions(t, options.RunOptions{}), options.ModePreview, ui)
	if err != nil {
		t.Fatalf("Run dry-run: %v", err)
	}
	assertInfoCalls(t, ui, []string{
		"dry-run: agent version 1.0.0",
		"dry-run: would build runner image agents-sandbox-runner-x (runner image not present locally)",
		"dry-run: would create home volume",
		"dry-run: would start agent session",
	})
}

func TestRunDryRunRendersReusePreview(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	stubPreviewSandbox(t, prepare.Preview{
		AgentVersion: "1.2.3",
		ImageTag:     "agents-sandbox-runner-y",
		HomeVolume:   "agents-sandbox-home-abc",
	})

	err := Run(context.Background(), resolvedRunOptions(t, options.RunOptions{}), options.ModePreview, ui)
	if err != nil {
		t.Fatalf("Run dry-run: %v", err)
	}
	assertInfoCalls(t, ui, []string{
		"dry-run: agent version 1.2.3",
		"dry-run: would reuse runner image agents-sandbox-runner-y",
		"dry-run: would reuse home volume agents-sandbox-home-abc",
		"dry-run: would start agent session",
	})
}

func TestRunDryRunRendersUpgradeCheckSkippedSuffix(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	stubPreviewSandbox(t, prepare.Preview{
		AgentVersion:        "1.0.0",
		UpgradeCheckSkipped: true,
		ImageTag:            "agents-sandbox-runner-y",
		HomeVolume:          "agents-sandbox-home-abc",
	})

	err := Run(context.Background(), resolvedRunOptions(t, options.RunOptions{}), options.ModePreview, ui)
	if err != nil {
		t.Fatalf("Run dry-run: %v", err)
	}
	assertInfoCalls(t, ui, []string{
		"dry-run: agent version 1.0.0 (upgrade check not run)",
		"dry-run: would reuse runner image agents-sandbox-runner-y",
		"dry-run: would reuse home volume agents-sandbox-home-abc",
		"dry-run: would start agent session",
	})
}

func TestRunDryRunResolveAtBuildMessage(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	stubPreviewSandbox(t, prepare.Preview{
		ResolveVersionAtBuild: true,
		ImageTag:              "agents-sandbox-runner-z",
		HomeVolume:            "agents-sandbox-home-abc",
	})

	err := Run(context.Background(), resolvedRunOptions(t, options.RunOptions{}), options.ModePreview, ui)
	if err != nil {
		t.Fatalf("Run dry-run: %v", err)
	}
	assertInfoCalls(t, ui, []string{
		"dry-run: agent version resolved at build time",
		"dry-run: would reuse runner image agents-sandbox-runner-z",
		"dry-run: would reuse home volume agents-sandbox-home-abc",
		"dry-run: would start agent session",
	})
}

func TestRunDryRunJoinsMultipleBuildReasons(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	stubPreviewSandbox(t, prepare.Preview{
		AgentVersion: "1.0.0",
		ImageTag:     "agents-sandbox-runner-x",
		BuildImage:   true,
		BuildReasons: []string{"runner image not present locally", "agent version changed"},
		HomeVolume:   "agents-sandbox-home-abc",
	})

	err := Run(context.Background(), resolvedRunOptions(t, options.RunOptions{}), options.ModePreview, ui)
	if err != nil {
		t.Fatalf("Run dry-run: %v", err)
	}
	assertInfoCalls(t, ui, []string{
		"dry-run: agent version 1.0.0",
		"dry-run: would build runner image agents-sandbox-runner-x (runner image not present locally; agent version changed)",
		"dry-run: would reuse home volume agents-sandbox-home-abc",
		"dry-run: would start agent session",
	})
}

func TestRunDryRunDoesNotPrepareSandbox(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	stubPreviewSandbox(t, prepare.Preview{AgentVersion: "1.0.0", ImageTag: "tag", HomeVolume: "vol"})
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		t.Error("prepareSandbox must not run in dry-run mode")
		return &fakePrepared{}, nil
	}
	t.Cleanup(func() { prepareSandbox = orig })

	if err := Run(
		context.Background(),
		resolvedRunOptions(t, options.RunOptions{}),
		options.ModePreview,
		ui,
	); err != nil {
		t.Fatalf("Run dry-run: %v", err)
	}
}

func TestRunDryRunPropagatesPreviewError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	orig := previewSandbox
	previewSandbox = func(context.Context, options.RunOptions, termio.Logger) (prepare.Preview, error) {
		return prepare.Preview{}, errors.New("preview boom")
	}
	t.Cleanup(func() { previewSandbox = orig })

	err := Run(context.Background(), resolvedRunOptions(t, options.RunOptions{}), options.ModePreview, ui)
	if err == nil || !strings.Contains(err.Error(), "preview boom") {
		t.Errorf("err = %v, want preview error", err)
	}
}

func TestRunDryRunVM(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	var gotSkipVM bool
	orig := prepareSandbox
	prepareSandbox = func(_ context.Context, _ options.RunOptions, skipVM bool, _ termio.UI) (preparedSandbox, error) {
		gotSkipVM = skipVM
		return &fakePrepared{}, nil
	}
	defer func() { prepareSandbox = orig }()

	err := Run(context.Background(), resolvedRunOptions(t, options.RunOptions{}), options.ModeSkipVM, ui)
	if err != nil {
		t.Fatalf("Run dry-run-vm: %v", err)
	}
	if !gotSkipVM {
		t.Error("prepareSandbox got skipVM=false under --dry-run-vm, want true")
	}
	if len(ui.InfoCalls) == 0 || ui.InfoCalls[0] != "dry-run: Would start agent session in VM" {
		t.Errorf("info = %v, want dry-run-vm message", ui.InfoCalls)
	}
}

func TestRunServeOnlyPath(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: msb.NewMockSandbox(msb.SandboxOpts{})}, nil
	}
	defer func() { prepareSandbox = orig }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, resolvedRunOptions(t, options.RunOptions{ServeOnly: true, ReapPolicy: options.NewReapPolicy(true, 5)}), options.ModeRun, ui)
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
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
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
		done <- Run(ctx, resolvedRunOptions(t, options.RunOptions{ServeOnly: true, ReapPolicy: options.NewReapPolicy(true, 5)}), options.ModeRun, ui)
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
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return nil, errors.New("prepare boom")
	}
	defer func() { prepareSandbox = orig }()

	err := Run(context.Background(), resolvedRunOptions(t, options.RunOptions{}), options.ModeRun, ui)
	if err == nil || !strings.Contains(err.Error(), "prepare boom") {
		t.Errorf("err = %v, want prepareSandbox error", err)
	}
}

// TestRunPrepareErrorDoesNotEmitTerminalReset pins that the reset is scoped to
// the attach path: a failure before any attach ran must not write control bytes
// into captured output (e.g. serve-only or a piped run).
func TestRunPrepareErrorDoesNotEmitTerminalReset(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return nil, errors.New("prepare boom")
	}
	defer func() { prepareSandbox = orig }()

	err := Run(context.Background(), resolvedRunOptions(t, options.RunOptions{}), options.ModeRun, ui)
	if err == nil || !strings.Contains(err.Error(), "prepare boom") {
		t.Errorf("err = %v, want prepareSandbox error", err)
	}
	if got := ui.StdOutBuffer.String(); got != "" {
		t.Fatalf("StdOut = %q, want no terminal reset before any attach", got)
	}
}

// TestShellAttachEmitsTerminalReset pins that Shell shares the attach path's
// reset emission after an abnormal end with Run.
func TestShellAttachEmitsTerminalReset(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui, written := newTerminalStdoutUI(t)
	sb := &msb.MockSandbox{AttachCode: -1, Name_: "test-vm"}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: sb, target: "/workspace"}, nil
	}
	defer func() { prepareSandbox = orig }()

	err := Shell(
		context.Background(),
		resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)}),
		options.ModeRun,
		ui,
	)
	var exitErr *sandbox.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != -1 {
		t.Fatalf("Shell = %v, want ExitError code -1", err)
	}
	if got := written(); !strings.Contains(got, terminalReset) {
		t.Fatalf("terminal received %q, want the terminal reset sequence", got)
	}
}

func TestRunServeOnlyReapFailureWarns(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	sb := &msb.MockSandbox{Name_: "test-vm"}
	sb.ShellErr = errors.New("shell failed")
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: sb, key: state.Key{Slug: "serveonlyreapfail", Agent: "opencode"}}, nil
	}
	defer func() { prepareSandbox = orig }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, resolvedRunOptions(t, options.RunOptions{
			Agent:      "opencode",
			ServeOnly:  true,
			ReapPolicy: options.NewReapPolicy(false, 5),
		}), options.ModeRun, ui)
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

// TestRunServeOnlyWarnsAboutContainersAfterCancel covers the canceled exit path
// end to end: serve-only exits by canceling ctx, and the last-client reaper must
// still probe for running containers (and warn) rather than let the canceled
// context suppress the probe. Without the probe's own context this yields no
// warning, which is the regression greptile flagged.
func TestRunServeOnlyWarnsAboutContainersAfterCancel(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	sb := canceledContextSandbox{Sandbox: msb.NewMockSandbox(msb.SandboxOpts{
		ShellOut: map[string]msb.ShellResult{
			runningContainersProbeCmd: msb.NewTestResult(true, 0, "abc123\n", "", nil),
		},
	})}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: sb, key: state.Key{Slug: "warncontainers", Agent: "opencode"}}, nil
	}
	defer func() { prepareSandbox = orig }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, resolvedRunOptions(t, options.RunOptions{
			Agent:      "opencode",
			ServeOnly:  true,
			ReapPolicy: options.ReapPolicy{AutoStopOnActiveSessions: true},
		}), options.ModeRun, ui)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run serve-only did not return after cancel")
	}
	found := false
	for _, w := range ui.WarnCalls {
		if strings.Contains(w, "container") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a running-container warning on serve-only exit, got %v", ui.WarnCalls)
	}
}

func TestRunNormalCallsRunAttach(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
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
	var gotSkipVM bool
	prepareSandbox = func(_ context.Context, _ options.RunOptions, skipVM bool, _ termio.UI) (preparedSandbox, error) {
		gotSkipVM = skipVM
		return &fakePrepared{sb: sb, target: "/workspace", key: state.Key{Slug: "normalattach", Agent: "opencode"}}, nil
	}
	defer func() { prepareSandbox = orig }()

	trace := &lifecycle.Trace{}
	err := Run(context.Background(), resolvedRunOptions(t, options.RunOptions{
		Agent:      "opencode",
		ReapPolicy: options.NewReapPolicy(true, 5),
		Trace:      trace,
	}), options.ModeRun, ui)
	if err != nil {
		t.Fatalf("Run normal: %v", err)
	}
	if gotSkipVM {
		t.Error("prepareSandbox got skipVM=true on a real run, want false")
	}
	if hasDryRunMessage(ui.InfoCalls) {
		t.Errorf("a real run must not take the dry-run-vm path, got info calls %v", ui.InfoCalls)
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
	pressureReported := false
	for _, v := range ui.VerboseCalls {
		if strings.Contains(v, "memory pressure during attach") {
			pressureReported = true
			break
		}
	}
	if !pressureReported {
		t.Errorf("expected the pressure summary in verbose output, got %v", ui.VerboseCalls)
	}
	if got, want := trace.Stages(), []lifecycle.StageID{lifecycle.Attach}; !slices.Equal(got, want) {
		t.Errorf("trace = %v, want %v (attach recorded once)", got, want)
	}
}

func TestRunStartsNotifyWatcher(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	origPrepare := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: msb.NewMockSandbox(msb.SandboxOpts{AttachCode: 0})}, nil
	}
	defer func() { prepareSandbox = origPrepare }()

	origWatch := notifyWatch
	defer func() { notifyWatch = origWatch }()
	started := make(chan struct{})
	notifyWatch = func(_ context.Context, _ notify.StreamOpener, _ agent.EventStreamSpec, _ notify.Backend) error {
		close(started)
		return nil
	}

	err := Run(context.Background(), resolvedRunOptions(t, options.RunOptions{
		ReapPolicy: options.NewReapPolicy(true, 5),
		Notify: notify.Config{
			Desktop: true, Audio: notify.AudioOff,
			OnInput: true, OnDone: true, OnError: true,
		},
	}), options.ModeRun, ui)
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
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	origPrepare := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: msb.NewMockSandbox(msb.SandboxOpts{AttachCode: 0})}, nil
	}
	defer func() { prepareSandbox = origPrepare }()

	origWatch := notifyWatch
	defer func() { notifyWatch = origWatch }()
	called := false
	notifyWatch = func(_ context.Context, _ notify.StreamOpener, _ agent.EventStreamSpec, _ notify.Backend) error {
		called = true
		return nil
	}

	err := Run(context.Background(), resolvedRunOptions(t, options.RunOptions{
		ReapPolicy: options.NewReapPolicy(true, 5),
	}), options.ModeRun, ui)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called {
		t.Error("notify watcher should not start when notify config is inactive")
	}
}

func TestRunLogsNotifyWatcherErrorAfterStop(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	origPrepare := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: msb.NewMockSandbox(msb.SandboxOpts{AttachCode: 0})}, nil
	}
	defer func() { prepareSandbox = origPrepare }()

	origWatch := notifyWatch
	defer func() { notifyWatch = origWatch }()
	notifyWatch = func(_ context.Context, _ notify.StreamOpener, _ agent.EventStreamSpec, _ notify.Backend) error {
		return errors.New("watch boom")
	}

	err := Run(context.Background(), resolvedRunOptions(t, options.RunOptions{
		ReapPolicy: options.NewReapPolicy(true, 5),
		Notify: notify.Config{
			Desktop: true, Audio: notify.AudioOff,
			OnInput: true, OnDone: true, OnError: true,
		},
	}), options.ModeRun, ui)
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
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	origPrepare := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: msb.NewMockSandbox(msb.SandboxOpts{AttachCode: 0})}, nil
	}
	defer func() { prepareSandbox = origPrepare }()

	origWatch := notifyWatch
	defer func() { notifyWatch = origWatch }()
	called := false
	notifyWatch = func(_ context.Context, _ notify.StreamOpener, _ agent.EventStreamSpec, _ notify.Backend) error {
		called = true
		return nil
	}

	err := Run(context.Background(), resolvedRunOptions(t, options.RunOptions{
		Agent:      "pi",
		ReapPolicy: options.NewReapPolicy(true, 5),
		Notify: notify.Config{
			Desktop: true, Audio: notify.AudioOff,
			OnInput: true, OnDone: true, OnError: true,
		},
	}), options.ModeRun, ui)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called {
		t.Error("notify watcher should not start for an agent without EventStreamProvider")
	}
}

func TestRunDryRunRendersNoRebuildReuseWithSkippedReasons(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	stubPreviewSandbox(t, prepare.Preview{
		AgentVersion: "1.2.3",
		ImageTag:     "agents-sandbox-runner-y",
		BuildReasons: []string{"project Dockerfile changed"},
		HomeVolume:   "agents-sandbox-home-abc",
	})

	err := Run(context.Background(), resolvedRunOptions(t, options.RunOptions{}), options.ModePreview, ui)
	if err != nil {
		t.Fatalf("Run dry-run: %v", err)
	}
	assertInfoCalls(t, ui, []string{
		"dry-run: agent version 1.2.3",
		"dry-run: would reuse runner image agents-sandbox-runner-y despite changes (--no-rebuild): project Dockerfile changed",
		"dry-run: would reuse home volume agents-sandbox-home-abc",
		"dry-run: would start agent session",
	})
}

func TestWithClientSessionHandsResolvedAgentToBody(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}

	var got string
	err := withClientSession(
		context.Background(),
		state.Key{Slug: "withsessionagent", Agent: "opencode"},
		&msb.MockSandbox{Name_: "test-vm"},
		options.ReapPolicy{AutoStopOnActiveSessions: true},
		ui,
		alwaysReap,
		opencodeResolved(t),
		func(a agent.Resolved) error {
			got = a.Profile.Name()
			return nil
		},
	)
	if err != nil {
		t.Fatalf("withClientSession: %v", err)
	}
	if got != "opencode" {
		t.Errorf("body agent = %q, want %q", got, "opencode")
	}
}

// TestWithClientSessionReleasesLeaseBeforeReaping pins the rule that the lease is
// released before the last-client reap, so state.CountActiveClients reflects only
// OTHER live clients while the reaper decides. The container probe is the reaper's
// first observable step and runs only when no client is active, so its warning
// proves this session's own lease was already gone.
func TestWithClientSessionReleasesLeaseBeforeReaping(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "withsessionrelease", Agent: "opencode"}
	ui := &termiotest.Mock{}
	sb := &msb.MockSandbox{
		Name_: "test-vm",
		ShellOut: map[string]msb.ShellResult{
			runningContainersProbeCmd: msb.NewTestResult(true, 0, "abc123\n", "", nil),
		},
	}

	err := withClientSession(
		context.Background(),
		k,
		sb,
		options.ReapPolicy{},
		ui,
		alwaysReap,
		opencodeResolved(t),
		func(agent.Resolved) error {
			if got := state.CountActiveClients(k); got != 1 {
				t.Errorf("body ran with %d active clients, want 1 (its own lease)", got)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("withClientSession: %v", err)
	}
	if got := state.CountActiveClients(k); got != 0 {
		t.Errorf("CountActiveClients after withClientSession = %d, want 0", got)
	}
	if !containerProbeWarn(ui) {
		t.Errorf("expected the last-client reap to probe containers (lease released first), got %v", ui.WarnCalls)
	}
}

// TestWithClientSessionReturnsBodyErrorAfterReaping pins that a body error is
// returned unchanged and the reap still runs: the attach path reaps even when the
// attach itself fails.
func TestWithClientSessionReturnsBodyErrorAfterReaping(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "withsessionbodyerr", Agent: "opencode"}
	ui := &termiotest.Mock{}
	sb := &msb.MockSandbox{
		Name_: "test-vm",
		ShellOut: map[string]msb.ShellResult{
			runningContainersProbeCmd: msb.NewTestResult(true, 0, "abc123\n", "", nil),
		},
	}
	sentinel := errors.New("body boom")

	err := withClientSession(
		context.Background(),
		k,
		sb,
		options.ReapPolicy{},
		ui,
		alwaysReap,
		opencodeResolved(t),
		func(agent.Resolved) error {
			return sentinel
		},
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("withClientSession = %v, want the body error", err)
	}
	if got := state.CountActiveClients(k); got != 0 {
		t.Errorf("CountActiveClients = %d, want 0 after the deferred release", got)
	}
	if !containerProbeWarn(ui) {
		t.Errorf("expected the reap to run despite the body error, got %v", ui.WarnCalls)
	}
}

// TestWithClientSessionLeaseFailureStillRunsBody pins that a failed lease
// acquisition only warns: the session still runs and the reap still fires (the
// running-container probe proves it), with no lease of its own to release.
func TestWithClientSessionLeaseFailureStillRunsBody(t *testing.T) {
	slug := "withsessionleasefail"
	blockClientLease(t, slug)
	ui := &termiotest.Mock{}
	sb := &msb.MockSandbox{
		Name_: "test-vm",
		ShellOut: map[string]msb.ShellResult{
			runningContainersProbeCmd: msb.NewTestResult(true, 0, "abc123\n", "", nil),
		},
	}
	ran := false

	err := withClientSession(
		context.Background(),
		state.Key{Slug: slug, Agent: "opencode"},
		sb,
		options.ReapPolicy{AutoStopOnActiveSessions: true},
		ui,
		alwaysReap,
		opencodeResolved(t),
		func(agent.Resolved) error {
			ran = true
			return nil
		},
	)
	if err != nil {
		t.Fatalf("withClientSession: %v", err)
	}
	if !ran {
		t.Error("body must run even when the client lease cannot be acquired")
	}
	found := false
	for _, w := range ui.WarnCalls {
		if strings.Contains(w, "client lease failed") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a client lease warning, got %v", ui.WarnCalls)
	}
	if !containerProbeWarn(ui) {
		t.Errorf("expected the reap to probe containers despite the lease failure, got %v", ui.WarnCalls)
	}
}

// TestWithClientSessionReleasesLeaseOnPanic covers the deferred release: a
// panicking body unwinds past the explicit release, so only the deferred
// best-effort release drops the lease.
func TestWithClientSessionReleasesLeaseOnPanic(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "withsessionpanic", Agent: "opencode"}
	ui := &termiotest.Mock{}

	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected the body panic to propagate")
			}
		}()
		_ = withClientSession(
			context.Background(),
			k,
			&msb.MockSandbox{Name_: "test-vm"},
			options.ReapPolicy{AutoStopOnActiveSessions: true},
			ui,
			alwaysReap,
			opencodeResolved(t),
			func(agent.Resolved) error {
				panic("body boom")
			},
		)
	}()

	if got := state.CountActiveClients(k); got != 0 {
		t.Errorf("CountActiveClients after a panicking body = %d, want 0 (deferred release)", got)
	}
}

// TestServeOnlyShouldReap pins the serve-only predicate directly, including the
// nil clean-exit case the runServeOnlySession path cannot reach: runServeOnly
// always returns its ctx.Err(), so no session error observed through it is nil.
func TestServeOnlyShouldReap(t *testing.T) {
	cases := []struct {
		name       string
		sessionErr error
		want       bool
	}{
		{"clean exit", nil, true},
		{"canceled", context.Canceled, true},
		{"deadline exceeded", context.DeadlineExceeded, false},
		{"other error", errors.New("boom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := serveOnlyShouldReap(tc.sessionErr); got != tc.want {
				t.Errorf("serveOnlyShouldReap(%v) = %v, want %v", tc.sessionErr, got, tc.want)
			}
		})
	}
}

// TestRunServeOnlySessionSkipsReapOnNonCanceledError pins the serve-only reap
// predicate: a payload error other than context.Canceled (here a deadline) is
// returned without reaping, so the VM is left to its idle timeout. Only a clean
// or canceled exit reaps.
func TestRunServeOnlySessionSkipsReapOnNonCanceledError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "serveonlyskipreap", Agent: "opencode"}
	ui := &termiotest.Mock{}
	sb := &msb.MockSandbox{
		Name_: "test-vm",
		ShellOut: map[string]msb.ShellResult{
			runningContainersProbeCmd: msb.NewTestResult(true, 0, "abc123\n", "", nil),
		},
	}

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(50*time.Millisecond))
	defer cancel()

	err := runServeOnlySession(ctx, opencodeResolved(t), k, sb, options.ReapPolicy{}, ui, 4096)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runServeOnlySession = %v, want context.DeadlineExceeded", err)
	}
	if containerProbeWarn(ui) {
		t.Errorf("serve-only must not reap on a non-canceled error, got %v", ui.WarnCalls)
	}
	if got := state.CountActiveClients(k); got != 0 {
		t.Errorf("CountActiveClients = %d, want 0 after the deferred release", got)
	}
}

// TestShellAndRunServeHostOpenerWhileAttached covers the wiring: both session
// kinds start the host opener for the session's home volume when open-links is
// on, and stop it when the session ends.
func TestShellAndRunServeHostOpenerWhileAttached(t *testing.T) {
	for _, session := range []struct {
		name  string
		start func(context.Context, options.RunOptions, options.Mode, termio.UI) error
	}{{"shell", Shell}, {"run", Run}} {
		t.Run(session.name, func(t *testing.T) {
			configpathstest.WithMockConfigPaths(t)
			watched := stubHostOpener(t, "/host/volumes/home-vol", opener.Report{}, nil)
			sb := &msb.MockSandbox{AttachCode: 0, Name_: "test-vm"}
			sb.ShellOut = map[string]msb.ShellResult{
				"curl -sf http://127.0.0.1:4096/session/status": msb.NewTestResult(true, 0, `{}`, "", nil),
			}
			sb.ExecOut = map[string]msb.ShellResult{"sleep 1h": msb.NewTestResult(true, 0, "", "", nil)}
			original := prepareSandbox
			prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
				return &fakePrepared{sb: sb, target: "/workspace", homeVolume: "home-vol"}, nil
			}
			t.Cleanup(func() { prepareSandbox = original })

			opts := options.RunOptions{OpenLinks: true, ReapPolicy: options.NewReapPolicy(true, 5)}
			if err := session.start(context.Background(), opts, options.ModeRun, &termiotest.Mock{}); err != nil {
				t.Fatalf("%s: %v", session.name, err)
			}
			if len(*watched) != 1 {
				t.Fatalf("host opener started %d times, want 1", len(*watched))
			}
		})
	}
}

func TestShellPrepareSandboxError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return nil, errors.New("prepare boom")
	}
	defer func() { prepareSandbox = orig }()

	err := Shell(context.Background(), resolvedRunOptions(t, options.RunOptions{}), options.ModeRun, ui)
	if err == nil || !strings.Contains(err.Error(), "prepare boom") {
		t.Errorf("err = %v, want prepareSandbox error", err)
	}
}

func TestShellDryRunRendersPreview(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	stubPreviewSandbox(t, prepare.Preview{
		AgentVersion: "1.0.0",
		ImageTag:     "agents-sandbox-runner-x",
		BuildImage:   true,
		BuildReasons: []string{"runner image not present locally"},
		HomeVolume:   "agents-sandbox-home-abc",
	})

	err := Shell(context.Background(), resolvedRunOptions(t, options.RunOptions{}), options.ModePreview, ui)
	if err != nil {
		t.Fatalf("Shell dry-run: %v", err)
	}
	assertInfoCalls(t, ui, []string{
		"dry-run: agent version 1.0.0",
		"dry-run: would build runner image agents-sandbox-runner-x (runner image not present locally)",
		"dry-run: would reuse home volume agents-sandbox-home-abc",
		"dry-run: would start interactive shell session",
	})
}

func TestShellDryRunDoesNotPrepareSandbox(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	stubPreviewSandbox(t, prepare.Preview{AgentVersion: "1.0.0", ImageTag: "tag", HomeVolume: "vol"})
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		t.Error("prepareSandbox must not run in dry-run mode")
		return &fakePrepared{}, nil
	}
	t.Cleanup(func() { prepareSandbox = orig })

	if err := Shell(
		context.Background(),
		resolvedRunOptions(t, options.RunOptions{}),
		options.ModePreview,
		ui,
	); err != nil {
		t.Fatalf("Shell dry-run: %v", err)
	}
}

func TestShellDryRunPropagatesPreviewError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	orig := previewSandbox
	previewSandbox = func(context.Context, options.RunOptions, termio.Logger) (prepare.Preview, error) {
		return prepare.Preview{}, errors.New("preview boom")
	}
	t.Cleanup(func() { previewSandbox = orig })

	err := Shell(context.Background(), resolvedRunOptions(t, options.RunOptions{}), options.ModePreview, ui)
	if err == nil || !strings.Contains(err.Error(), "preview boom") {
		t.Errorf("err = %v, want preview error", err)
	}
}

func TestShellDryRunVM(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	var gotSkipVM bool
	orig := prepareSandbox
	prepareSandbox = func(_ context.Context, _ options.RunOptions, skipVM bool, _ termio.UI) (preparedSandbox, error) {
		gotSkipVM = skipVM
		return &fakePrepared{}, nil
	}
	defer func() { prepareSandbox = orig }()

	err := Shell(context.Background(), resolvedRunOptions(t, options.RunOptions{}), options.ModeSkipVM, ui)
	if err != nil {
		t.Fatalf("Shell dry-run-vm: %v", err)
	}
	if !gotSkipVM {
		t.Error("prepareSandbox got skipVM=false under --dry-run-vm, want true")
	}
	if len(ui.InfoCalls) == 0 || ui.InfoCalls[0] != "dry-run: Would start interactive shell session" {
		t.Errorf("info = %v, want dry-run shell message", ui.InfoCalls)
	}
}

func TestShellNormalCallsRunAttach(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
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
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: sb, target: "/workspace"}, nil
	}
	defer func() { prepareSandbox = orig }()

	trace := &lifecycle.Trace{}
	err := Shell(
		context.Background(),
		resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5), Trace: trace}),
		options.ModeRun,
		ui,
	)
	if err != nil {
		t.Fatalf("Shell normal: %v", err)
	}
	if hasDryRunMessage(ui.InfoCalls) {
		t.Errorf("a real shell run must not take the dry-run-vm path, got info calls %v", ui.InfoCalls)
	}
	if got, want := trace.Stages(), []lifecycle.StageID{lifecycle.Attach}; !slices.Equal(got, want) {
		t.Errorf("trace = %v, want %v (attach recorded once)", got, want)
	}
}

func TestShellUsesGuestCWD(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
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
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: sb, target: "/workspace", guestCWD: "/workspace/sub"}, nil
	}
	defer func() { prepareSandbox = orig }()

	err := Shell(
		context.Background(),
		resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)}),
		options.ModeRun,
		ui,
	)
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if sb.AttachCwd != "/workspace/sub" {
		t.Errorf("AttachCwd = %q, want %q", sb.AttachCwd, "/workspace/sub")
	}
}

func TestFinalizeRun(t *testing.T) {
	if err := finalizeRun(nil, 0); err != nil {
		t.Errorf("finalizeRun(nil, 0) = %v, want nil", err)
	}
	boom := errors.New("boom")
	if err := finalizeRun(boom, 1); !errors.Is(err, boom) || !strings.Contains(err.Error(), "agent session failed") {
		t.Errorf("finalizeRun error = %v, want the session failure wrapping %v", err, boom)
	}
	err := finalizeRun(nil, 3)
	var exitErr *sandbox.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Errorf("finalizeRun(nil, 3) = %v, want ExitError code 3", err)
	}
}

func TestRunAttachClientLeaseFailureWarns(t *testing.T) {
	slug := "attachleasefail"
	blockClientLease(t, slug)
	ui := &termiotest.Mock{}
	sb := &msb.MockSandbox{AttachCode: 0}

	err := runAttach(context.Background(), state.Key{Slug: slug, Agent: "opencode"}, sb, "/workspace", ui,
		resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)}), "-l")
	if err != nil {
		t.Fatalf("runAttach: expected success despite lease failure, got %v", err)
	}

	found := false
	for _, w := range ui.WarnCalls {
		if strings.Contains(w, "client lease failed") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected client lease warning, got %v", ui.WarnCalls)
	}
}

func TestRunAttachUsesAttachWithForRoot(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	ui := &termiotest.Mock{}
	sb := &msb.MockSandbox{AttachCode: 0}

	release, err := state.AcquireClientLease(state.Key{Slug: "roottest", Agent: "opencode"})
	if err != nil {
		t.Fatalf("state.AcquireClientLease: %v", err)
	}
	release()

	err = runAttach(context.Background(), state.Key{Slug: "roottest", Agent: "opencode"}, sb, "/workspace/sub", ui,
		resolvedRunOptions(t, options.RunOptions{Root: true, ReapPolicy: options.NewReapPolicy(false, 5)}), "-l")
	if err != nil {
		t.Fatalf("runAttach failed: %v", err)
	}
	if sb.AttachUser != "root" {
		t.Errorf("AttachUser = %q, want %q", sb.AttachUser, "root")
	}
	if sb.AttachCwd != "/workspace/sub" {
		t.Errorf("AttachCwd = %q, want %q", sb.AttachCwd, "/workspace/sub")
	}
}

func TestRunAttachDefaultDoesNotUseRoot(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	ui := &termiotest.Mock{}
	sb := &msb.MockSandbox{AttachCode: 0}

	release, err := state.AcquireClientLease(state.Key{Slug: "defaulttest", Agent: "opencode"})
	if err != nil {
		t.Fatalf("state.AcquireClientLease: %v", err)
	}
	release()

	err = runAttach(context.Background(), state.Key{Slug: "defaulttest", Agent: "opencode"}, sb, "/workspace/sub", ui,
		resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(false, 5)}), "-l")
	if err != nil {
		t.Fatalf("runAttach failed: %v", err)
	}
	if sb.AttachUser != "" {
		t.Errorf("AttachUser = %q, want empty for default (non-root) attach", sb.AttachUser)
	}
	if sb.AttachCwd != "/workspace/sub" {
		t.Errorf("AttachCwd = %q, want %q", sb.AttachCwd, "/workspace/sub")
	}
}

func TestRunAttachPropagatesAttachError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	sb := &msb.MockSandbox{AttachErr: errors.New("attach boom")}

	err := runAttach(context.Background(), state.Key{Slug: "errproj", Agent: "opencode"}, sb, "/workspace", ui,
		resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)}), "-l")
	if err == nil {
		t.Fatal("expected error when attach fails")
	}
	if !strings.Contains(err.Error(), "agent session failed") {
		t.Errorf("err = %q, want wrapped attach failure", err.Error())
	}
}

func TestRunAttachReturnsExitErrorForNonZeroCode(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	sb := &msb.MockSandbox{AttachCode: 3}

	err := runAttach(context.Background(), state.Key{Slug: "exitproj", Agent: "opencode"}, sb, "/workspace", ui,
		resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)}), "-l")
	var exitErr *sandbox.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Errorf("err = %v, want ExitError code 3", err)
	}
}

func TestRunAttachWarnsOnReapFailure(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	sb := &msb.MockSandbox{AttachCode: 0, Name_: "test-vm"}
	sb.ShellErr = errors.New("shell failed")

	// A cancelled context makes waitQuiescent abort on its next poll, so
	// reapOnLastClient returns an error that runAttach must warn about.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := runAttach(ctx, state.Key{Slug: "reaperrproj", Agent: "opencode"}, sb, "/workspace", ui,
		resolvedRunOptions(t, options.RunOptions{Agent: "opencode", ReapPolicy: options.NewReapPolicy(false, 5)}), "-l")
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

// TestPrepareSandboxSeamDelegatesToVM calls the real, unstubbed prepareSandbox
// so its delegation to prepare.PrepareSandbox is covered. An unknown agent name
// makes the real path fail deterministically before it can touch host state,
// the mocked config paths, or the fail-fast msb client.
func TestPrepareSandboxSeamDelegatesToVM(t *testing.T) {
	t.Chdir(t.TempDir())

	_, err := prepareSandbox(
		context.Background(),
		options.RunOptions{Agent: "no-such-agent"},
		false,
		&termiotest.Mock{},
	)
	if err == nil {
		t.Fatal("expected the real PrepareSandbox to error for an unknown agent")
	}
	if !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("error = %v, want an unknown-agent error from the real path", err)
	}
}

// blockClientLease makes state.AcquireClientLease fail for slug by placing a
// regular file where the slug's state directory would live, so MkdirAll of the
// nested clients dir fails.
func blockClientLease(t *testing.T, slug string) {
	t.Helper()
	configpathstest.WithMockConfigPaths(t)
	root := configpaths.Get().UserStateDir()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("mkdir state root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, slug), nil, 0o600); err != nil {
		t.Fatalf("block client lease for %q: %v", slug, err)
	}
}

// resolvedRunOptions returns opts with ResolvedAgent set as runoptions.Build
// would: the profile named by opts.Agent (the default when empty), with its
// capabilities resolved. Tests that construct RunOptions directly use it so the
// session layer reads the run boundary's resolved agent.
func resolvedRunOptions(t *testing.T, opts options.RunOptions) options.RunOptions {
	t.Helper()
	a, ok := agent.Lookup(opts.Agent)
	if !ok {
		return opts
	}
	opts.ResolvedAgent = agent.Resolve(a)
	return opts
}

// stubPreviewSandbox swaps the preview seam for a fixed preview and restores it
// on cleanup. Tests that run with options.ModePreview must use it so the real
// PreviewSandbox never touches host state.
func stubPreviewSandbox(t *testing.T, preview prepare.Preview) {
	t.Helper()
	orig := previewSandbox
	previewSandbox = func(context.Context, options.RunOptions, termio.Logger) (prepare.Preview, error) {
		return preview, nil
	}
	t.Cleanup(func() { previewSandbox = orig })
}

func assertInfoCalls(t *testing.T, ui *termiotest.Mock, want []string) {
	t.Helper()
	if len(ui.InfoCalls) != len(want) {
		t.Fatalf("info calls = %v, want %v", ui.InfoCalls, want)
	}
	for i, line := range want {
		if ui.InfoCalls[i] != line {
			t.Errorf("info call %d = %q, want %q", i, ui.InfoCalls[i], line)
		}
	}
}

// hasDryRunMessage reports whether any info line is the skip-VM dry-run notice
// that Run and Shell print only for a skip-VM mode.
func hasDryRunMessage(infoCalls []string) bool {
	for _, line := range infoCalls {
		if strings.HasPrefix(line, "dry-run: Would start") {
			return true
		}
	}
	return false
}

// containerProbeWarn reports whether the last-client reap probed for running
// containers, which it only does once this session's own lease is gone.
func containerProbeWarn(ui *termiotest.Mock) bool {
	for _, w := range ui.WarnCalls {
		if strings.Contains(w, "running container") {
			return true
		}
	}
	return false
}

// --- Post-attach memory detection ---

// seedSessionHomeState creates the key's state file (empty) so the marker
// writers, which no-op without one, take effect.
func seedSessionHomeState(t *testing.T, k state.Key) {
	t.Helper()
	if err := state.RecoverHomeState(k, func(hs *state.HomeState, _ bool) (bool, error) {
		*hs = state.HomeState{}
		return true, nil
	}); err != nil {
		t.Fatalf("seed home state: %v", err)
	}
}

// sessionOOMLogLine builds a system-log entry carrying a parseable OOM kill.
func sessionOOMLogLine(at time.Time) msb.LogEntry {
	return msb.LogEntry{
		Source:    msb.LogSourceSystem,
		Timestamp: at,
		Data:      []byte("Out of memory: Killed process 7 (opencode) total-vm:1kB"),
	}
}

// TestRunPostAttachOOMOffersAndDeclineRecords pins the post-attach path: an
// abnormal end (signal death) with an OOM line prompts once, and a decline
// records the answered marker before the lease is released.
func TestRunPostAttachOOMOffersAndDeclineRecords(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "postattachoom", Agent: "opencode"}
	seedSessionHomeState(t, k)
	var selects int
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}
	sb := &msb.MockSandbox{
		AttachCode:    -1,
		LogsResult:    []msb.LogEntry{sessionOOMLogLine(time.Now())},
		MetricsResult: &msb.Metrics{MemoryBytes: 1024 << 20, MemoryLimitBytes: 4096 << 20},
	}

	err := runAttach(context.Background(), k, sb, "/workspace", ui,
		resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)}), "-l")

	var exitErr *sandbox.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != -1 {
		t.Fatalf("runAttach = %v, want ExitError code -1", err)
	}
	if selects != 1 {
		t.Fatalf("Select called %d times, want 1", selects)
	}
	hs, readErr := state.ReadState(k)
	if readErr != nil {
		t.Fatalf("ReadState: %v", readErr)
	}
	if hs.ResourcePressure == nil || hs.ResourcePressure.OfferedAt.IsZero() {
		t.Fatalf("marker = %+v, want an answered offer recorded", hs.ResourcePressure)
	}
}

// TestRunAttachSignalSkipsTheOfferAndStillReaps pins the signal scope around
// the attach: SIGINT/SIGTERM during the attach ends it, skips the memory offer
// (the user asked to stop), and the reap still runs on an uncanceled context.
func TestRunAttachSignalSkipsTheOfferAndStillReaps(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	// Keep the test process alive should the signal escape the attach scope.
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, os.Interrupt)
	defer signal.Stop(guard)
	k := state.Key{Slug: "signalattach", Agent: "opencode"}
	seedSessionHomeState(t, k)
	var selects int
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}
	sb := &msb.MockSandbox{
		LogsResult:    []msb.LogEntry{sessionOOMLogLine(time.Now())},
		MetricsResult: &msb.Metrics{MemoryBytes: 1024 << 20, MemoryLimitBytes: 4096 << 20},
		AttachFn: func(ctx context.Context) (int, error) {
			//testenv:allow sends SIGINT to this process to exercise the signal scope
			if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
				return 0, err
			}
			<-ctx.Done()
			return -1, nil
		},
	}

	_ = runAttach(context.Background(), k, sb, "/workspace", ui,
		resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)}), "-l")

	if selects != 0 {
		t.Errorf("Select called %d times, want no offer after an interrupted attach", selects)
	}
	for _, warning := range ui.WarnCalls {
		if strings.Contains(warning, "reap failed") {
			t.Errorf("reap ran on a canceled context: %q", warning)
		}
	}
}

// TestRunAttachReportsWatcherReadErrorsAfterTheAttach pins that a failing
// metrics read is reported once after the attach returns, never while the
// agent's full-screen TUI owns the terminal.
func TestRunAttachReportsWatcherReadErrorsAfterTheAttach(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	stubPressureWatchInterval(t)
	k := state.Key{Slug: "watchererrors", Agent: "opencode"}
	seedSessionHomeState(t, k)
	ui := &termiotest.Mock{}
	watcherLines := func() int {
		count := 0
		for _, call := range ui.VerboseCalls {
			if strings.Contains(call, "metrics read failed") {
				count++
			}
		}
		return count
	}
	linesDuringAttach := -1
	sb := &msb.MockSandbox{
		MetricsErr: errors.New("metrics unsupported"),
		AttachFn: func(context.Context) (int, error) {
			time.Sleep(20 * pressureWatchInterval)
			linesDuringAttach = watcherLines()
			return 0, nil
		},
	}

	_ = runAttach(context.Background(), k, sb, "/workspace", ui,
		resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)}), "-l")

	if linesDuringAttach != 0 {
		t.Errorf("%d watcher lines written during the attach, want none", linesDuringAttach)
	}
	if got := watcherLines(); got != 1 {
		t.Errorf("%d watcher lines after the attach, want one", got)
	}
}

// TestRunAttachPromptsOutsideTheSignalScope pins that the post-attach prompt
// runs after the attach's signal handling is released, so Ctrl-C at the prompt
// ends the launcher as usual: the attach context, which the signal scope cancels
// when it is released, is already done when the prompt runs.
func TestRunAttachPromptsOutsideTheSignalScope(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "promptscope", Agent: "opencode"}
	seedSessionHomeState(t, k)
	var attachDone <-chan struct{}
	promptedInsideScope := false
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		SelectFn: func(string, []termio.Choice, string) (string, error) {
			select {
			case <-attachDone:
			default:
				promptedInsideScope = true
			}
			return "keep", nil
		},
	}
	sb := &msb.MockSandbox{
		LogsResult:    []msb.LogEntry{sessionOOMLogLine(time.Now())},
		MetricsResult: &msb.Metrics{MemoryBytes: 1024 << 20, MemoryLimitBytes: 4096 << 20},
		AttachFn: func(ctx context.Context) (int, error) {
			attachDone = ctx.Done()
			return -1, nil
		},
	}

	_ = runAttach(context.Background(), k, sb, "/workspace", ui,
		resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)}), "-l")

	if attachDone == nil {
		t.Fatal("attach did not run")
	}
	if promptedInsideScope {
		t.Error("the memory prompt ran while the attach's signal handling was still active")
	}
}

// TestRunPostAttachCleanExitNoOffer pins the anti-false-positive rule: a clean
// exit does not prompt even when the system log carries an OOM line.
func TestRunPostAttachCleanExitNoOffer(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "postattachclean", Agent: "opencode"}
	seedSessionHomeState(t, k)
	var selects int
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}
	sb := &msb.MockSandbox{
		AttachCode:    0,
		LogsResult:    []msb.LogEntry{sessionOOMLogLine(time.Now())},
		MetricsResult: &msb.Metrics{MemoryBytes: 1024 << 20, MemoryLimitBytes: 4096 << 20},
	}

	err := runAttach(context.Background(), k, sb, "/workspace", ui,
		resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)}), "-l")
	if err != nil {
		t.Fatalf("runAttach = %v, want nil", err)
	}
	if selects != 0 {
		t.Fatalf("Select called %d times on a clean exit, want 0", selects)
	}
}

// TestRunAttachResetsTheTerminalAfterASignal pins that a signal-ended attach
// resets the terminal even when the guest reports a clean exit code.
func TestRunAttachResetsTheTerminalAfterASignal(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, os.Interrupt)
	defer signal.Stop(guard)
	ui, written := newTerminalStdoutUI(t)
	sb := &msb.MockSandbox{AttachFn: func(ctx context.Context) (int, error) {
		//testenv:allow sends SIGINT to this process to exercise the signal scope
		if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
			return 0, err
		}
		<-ctx.Done()
		return 0, nil
	}}

	_ = runAttach(context.Background(), state.Key{Slug: "signalreset", Agent: "opencode"}, sb, "/workspace", ui,
		resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(false, 5)}), "-l")

	if got := written(); !strings.Contains(got, terminalReset) {
		t.Fatalf("terminal received %q, want the reset after a signal", got)
	}
}

// TestRunAttachProbesDiskAfterEveryEndButASignal pins when the disk probe runs:
// after a clean exit too (disk pressure rarely ends a session), never after a
// signal ended the attach.
func TestRunAttachProbesDiskAfterEveryEndButASignal(t *testing.T) {
	t.Run("clean exit", func(t *testing.T) {
		configpathstest.WithMockConfigPaths(t)
		var execs []string
		sb := &msb.MockSandbox{AttachCode: 0, ExecCalls: &execs}
		_ = runAttach(
			context.Background(),
			state.Key{Slug: "diskclean", Agent: "opencode"},
			sb,
			"/workspace",
			&termiotest.Mock{},
			resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(false, 5)}),
			"-l",
		)
		if !slices.Contains(execs, diskProbeKey()) {
			t.Errorf("execs %q, want the disk probe after a clean exit", execs)
		}
	})
	t.Run("signal", func(t *testing.T) {
		configpathstest.WithMockConfigPaths(t)
		guard := make(chan os.Signal, 1)
		signal.Notify(guard, os.Interrupt)
		defer signal.Stop(guard)
		var execs []string
		sb := &msb.MockSandbox{ExecCalls: &execs, AttachFn: func(ctx context.Context) (int, error) {
			//testenv:allow sends SIGINT to this process to exercise the signal scope
			if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
				return 0, err
			}
			<-ctx.Done()
			return -1, nil
		}}
		_ = runAttach(
			context.Background(),
			state.Key{Slug: "disksignal", Agent: "opencode"},
			sb,
			"/workspace",
			&termiotest.Mock{},
			resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(false, 5)}),
			"-l",
		)
		if slices.Contains(execs, diskProbeKey()) {
			t.Errorf("execs %q, want no disk probe after a signal", execs)
		}
	})
}

// TestRunAttachCanceledParentIsNotASignal pins that an attach ended by its
// parent context, not by a signal to the launcher, is no interruption: the
// memory offer for the abnormal end still runs.
func TestRunAttachCanceledParentIsNotASignal(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "parentcancel", Agent: "opencode"}
	seedSessionHomeState(t, k)
	var selects int
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	sb := &msb.MockSandbox{
		LogsResult:    []msb.LogEntry{sessionOOMLogLine(time.Now())},
		MetricsResult: &msb.Metrics{MemoryBytes: 1024 << 20, MemoryLimitBytes: 4096 << 20},
		AttachFn: func(context.Context) (int, error) {
			cancel()
			return -1, nil
		},
	}

	_ = runAttach(ctx, k, sb, "/workspace", ui,
		resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)}), "-l")

	if selects != 1 {
		t.Fatalf("Select called %d times, want the offer after a parent-canceled abnormal end", selects)
	}
}

// TestRunPostAttachLogReadErrorDoesNotFail pins best-effort detection: a system
// log read failure does not prompt and does not change the session's result.
func TestRunPostAttachLogReadErrorDoesNotFail(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "postattachlogerr", Agent: "opencode"}
	seedSessionHomeState(t, k)
	var selects int
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}
	sb := &msb.MockSandbox{AttachCode: -1, LogsErr: errors.New("logs boom")}

	err := runAttach(context.Background(), k, sb, "/workspace", ui,
		resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)}), "-l")

	var exitErr *sandbox.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != -1 {
		t.Fatalf("runAttach = %v, want the normal ExitError code -1", err)
	}
	if selects != 0 {
		t.Fatalf("Select called %d times after a log read failure, want 0", selects)
	}
	if !verboseContainsSession(ui) {
		t.Errorf("verbose calls %v do not report the log read failure", ui.VerboseCalls)
	}
}

// TestRunPostAttachReadStateErrorLogsAndContinues pins the state-read failure
// path: when no marker can be read the detection logs and continues, leaving
// the session result unchanged.
func TestRunPostAttachReadStateErrorLogsAndContinues(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "postattachstateerr", Agent: "opencode"}
	var selects int
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}
	sb := &msb.MockSandbox{AttachCode: -1}

	err := runAttach(context.Background(), k, sb, "/workspace", ui,
		resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)}), "-l")

	var exitErr *sandbox.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != -1 {
		t.Fatalf("runAttach = %v, want the normal ExitError code -1", err)
	}
	if selects != 0 {
		t.Fatalf("Select called %d times, want 0", selects)
	}
	found := false
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, "reading home state for memory detection failed") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("verbose calls %v do not report the state read failure", ui.VerboseCalls)
	}
}

// verboseContainsSession reports whether any verbose call reports the system-log
// read failure, which is the observable best-effort log message.
func verboseContainsSession(ui *termiotest.Mock) bool {
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, "reading VM system log failed") {
			return true
		}
	}
	return false
}

// TestShellPostAttachOffer pins that Shell shares the post-attach detection
// path: an OOM-killed shell also prompts once.
func TestShellPostAttachOffer(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := state.Key{Slug: "shellpostattach", Agent: "opencode"}
	seedSessionHomeState(t, k)
	var selects int
	ui := &termiotest.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { selects++; return "keep", nil },
	}
	sb := &msb.MockSandbox{
		AttachCode:    -1,
		LogsResult:    []msb.LogEntry{sessionOOMLogLine(time.Now())},
		MetricsResult: &msb.Metrics{MemoryBytes: 1024 << 20, MemoryLimitBytes: 4096 << 20},
	}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: sb, target: "/workspace", key: k}, nil
	}
	defer func() { prepareSandbox = orig }()

	err := Shell(
		context.Background(),
		resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)}),
		options.ModeRun,
		ui,
	)

	var exitErr *sandbox.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != -1 {
		t.Fatalf("Shell = %v, want ExitError code -1", err)
	}
	if selects != 1 {
		t.Fatalf("Select called %d times from Shell, want 1", selects)
	}
}
