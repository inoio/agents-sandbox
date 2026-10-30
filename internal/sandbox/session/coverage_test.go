package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// blockClientLease makes state.AcquireClientLease fail for slug by placing a
// regular file where the slug's state directory would live, so MkdirAll of the
// nested clients dir fails.
func blockClientLease(t *testing.T, slug string) {
	t.Helper()
	configpaths.WithMockConfigPaths(t)
	root := configpaths.Get().UserStateDir()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("mkdir state root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, slug), nil, 0o600); err != nil {
		t.Fatalf("block client lease for %q: %v", slug, err)
	}
}

// --- waitQuiescent: client reattaches during the wait ---

func TestReapOnLastClient_ClientReattachesDuringWait(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	origInterval := waitQuiescentPollInterval
	waitQuiescentPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { waitQuiescentPollInterval = origInterval })

	slug := "reattachproj"
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{
		Name_: "test-vm",
		ShellOut: map[string]msb.ShellResult{
			"curl -sf http://127.0.0.1:4096/session/status": msb.NewTestResult(
				true, 0, `{"s1":{"type":"busy","attempt":0}}`, "", nil,
			),
		},
	}
	sb.ExecOut = map[string]msb.ShellResult{
		"sleep 1h": msb.NewTestResult(true, 0, "", "", nil),
	}

	// A client reattaches 20ms into the wait, so a later poll sees an active
	// client and aborts the reaper.
	hold := make(chan struct{})
	defer close(hold)
	go func() {
		time.Sleep(20 * time.Millisecond)
		release, _ := state.AcquireClientLease(state.Key{Slug: slug, Agent: "opencode"})
		<-hold
		if release != nil {
			release()
		}
	}()

	err := reapOnLastClient(
		context.Background(),
		opencodeAgent(t),
		state.Key{Slug: slug, Agent: "opencode"},
		sb,
		options.ReapPolicy{},
		ui,
	)
	if err != nil {
		t.Fatalf("reapOnLastClient: expected no error on client reattach, got %v", err)
	}

	found := false
	for _, v := range ui.VerboseCalls {
		if strings.Contains(v, "client reattached during wait") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected reattach verbose log, got %v", ui.VerboseCalls)
	}
}

// --- pendingQuestionSessionIDs: malformed response decodes to an error ---

func TestPendingQuestionSessionIDs_DecodeError(t *testing.T) {
	sb := &msb.MockSandbox{
		Name_: "test-vm",
		ShellOut: map[string]msb.ShellResult{
			"curl -sf http://127.0.0.1:4096/question": msb.NewTestResult(true, 0, `{invalid`, "", nil),
		},
	}
	_, err := pendingQuestionSessionIDs(
		context.Background(),
		sb,
		"curl -sf http://127.0.0.1:4096/question",
	)
	if err == nil {
		t.Fatal("expected error when question response is malformed")
	}
}

// --- startNotifyWatcher: returns the watcher's error from stop() ---

func TestStartNotifyWatcherReturnsWatchError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
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

// --- Run serve-only: client lease acquisition fails ---

func TestRunServeOnlyClientLeaseFailureWarns(t *testing.T) {
	key := state.Key{Slug: "serveonlyleasefail", Agent: "opencode"}
	blockClientLease(t, key.Slug)
	ui := &termio.Mock{}
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
		done <- Run(ctx, options.RunOptions{ServeOnly: true, ReapPolicy: options.NewReapPolicy(true, 5)}, options.ModeRun, ui)
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

// --- Run: the client lease is keyed by the session's prepared key ---

// TestRunUsesSessionKeyForLease pins the session-key wiring: Run keys the client
// lease (and therefore the reap) off the key PrepareSandbox prepared instead of
// re-resolving identity from the cwd, so the lease lands under the session's
// checkout slug. The lease file is removed on release, but its clients/
// directory remains, which is what the assertion observes.
func TestRunUsesSessionKeyForLease(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	key := state.Key{Slug: "sessionkeyslug", Agent: "opencode"}
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
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: sb, target: "/workspace", key: key}, nil
	}
	defer func() { prepareSandbox = orig }()

	if err := Run(
		context.Background(),
		options.RunOptions{Agent: "opencode", ReapPolicy: options.NewReapPolicy(true, 5)},
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

// --- Run serve-only: runServeOnly returns a non-cancel error (deadline) ---

func TestRunServeOnlyReturnsDeadlineExceeded(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
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
		options.RunOptions{ServeOnly: true, ReapPolicy: options.NewReapPolicy(true, 5)},
		options.ModeRun,
		ui,
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

// --- runAttach: client lease acquisition fails ---

func TestRunAttachClientLeaseFailureWarns(t *testing.T) {
	slug := "attachleasefail"
	blockClientLease(t, slug)
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{AttachCode: 0}

	err := runAttach(context.Background(), sb, slug, "/workspace", ui,
		options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)}, "-l")
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

// --- prepareSandbox: the real seam delegates to sandbox.PrepareSandbox ---

// TestPrepareSandboxSeamDelegatesToVM calls the real, unstubbed prepareSandbox
// so its delegation to sandbox.PrepareSandbox is covered. An unknown agent name
// makes the real path fail deterministically before it can touch host state,
// the mocked config paths, or the fail-fast msb client.
func TestPrepareSandboxSeamDelegatesToVM(t *testing.T) {
	t.Chdir(t.TempDir())

	_, err := prepareSandbox(
		context.Background(),
		options.RunOptions{Agent: "no-such-agent"},
		false,
		&termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected the real PrepareSandbox to error for an unknown agent")
	}
	if !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("error = %v, want an unknown-agent error from the real path", err)
	}
}
