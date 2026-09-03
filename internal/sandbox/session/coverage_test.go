package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inoio/opencode-sandbox/internal/agent"
	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/git"
	"github.com/inoio/opencode-sandbox/internal/notify"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	sandbox "github.com/inoio/opencode-sandbox/internal/sandbox/vm"
	"github.com/inoio/opencode-sandbox/internal/termio"
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

	// A client reattaches 100ms into the 2s poll interval, so the second poll
	// sees an active client and aborts the reaper.
	hold := make(chan struct{})
	defer close(hold)
	go func() {
		time.Sleep(100 * time.Millisecond)
		release, _ := state.AcquireClientLease(slug)
		<-hold
		if release != nil {
			release()
		}
	}()

	err := reapOnLastClient(context.Background(), opencodeAgent(t), slug, sb, options.ReapPolicy{}, ui)
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

// --- startNotifyWatcher: logs when the notify watcher returns an error ---

func TestStartNotifyWatcherLogsError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	origWatch := notifyWatch
	defer func() { notifyWatch = origWatch }()
	notifyWatch = func(_ context.Context, _ msb.Sandbox, _ agent.EventStreamSpec, _ notify.Backend) error {
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
	)
	stop()

	found := false
	for _, v := range ui.VerboseCalls {
		if strings.Contains(v, "notify watcher stopped") && strings.Contains(v, "watch boom") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected notify watcher error to be logged, got %v", ui.VerboseCalls)
	}
}

// --- Run serve-only: client lease acquisition fails ---

func TestRunServeOnlyClientLeaseFailureWarns(t *testing.T) {
	slug := git.ProjectSlug()
	blockClientLease(t, slug)
	ui := &termio.Mock{}
	sb := msb.NewMockSandbox(msb.SandboxOpts{})
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: sb}, nil
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

// --- Run serve-only: runServeOnly returns a non-cancel error (deadline) ---

func TestRunServeOnlyReturnsDeadlineExceeded(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	sb := msb.NewMockSandbox(msb.SandboxOpts{})
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{sb: sb}, nil
	}
	defer func() { prepareSandbox = orig }()

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(100*time.Millisecond))
	defer cancel()

	err := Run(ctx, options.RunOptions{ServeOnly: true, ReapPolicy: options.NewReapPolicy(true, 5)}, ui)
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

	err := runAttach(context.Background(), sb, slug, ui,
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
