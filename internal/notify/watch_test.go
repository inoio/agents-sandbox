package notify

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// sseBlock wraps typ in the envelope shape opencode's /global/event stream
// emits: a single data line whose type lives under payload.type.
func sseBlock(typ string) []byte {
	return []byte(
		"data: {\"directory\":\"/workspace\",\"project\":\"cb78d2f0026e9b81b438a362c1fc4dd57422a98d\",\"payload\":{\"id\":\"evt\",\"type\":\"" + typ + "\",\"properties\":{}}}\n\n",
	)
}

// streamStub implements msb.StreamHandle by replaying buffered events. When
// out of events it blocks until ctx is cancelled and returns ctx.Err(), so the
// stream stays open until the test decides to end it.
type streamStub struct {
	events []msb.StreamEvent
	idx    int
}

func (s *streamStub) Recv(ctx context.Context) (msb.StreamEvent, error) {
	if s.idx >= len(s.events) {
		<-ctx.Done()
		return msb.StreamEvent{}, ctx.Err()
	}
	e := s.events[s.idx]
	s.idx++
	return e, nil
}
func (s *streamStub) Close() error { return nil }

// recordBackend records notifications for assertions. The mutex makes it safe
// to read while the watcher goroutine is still appending.
type recordBackend struct {
	mu  sync.Mutex
	got []Notification
}

func (r *recordBackend) Notify(n Notification) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, n)
}

func (r *recordBackend) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.got)
}

func (r *recordBackend) notification(i int) Notification {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.got[i]
}

func TestWatchDrivesBackend(t *testing.T) {
	stream := &streamStub{
		events: []msb.StreamEvent{
			{Kind: msb.StreamEventStdout, Data: sseBlock("message.part.updated")},
			{Kind: msb.StreamEventStdout, Data: sseBlock("session.idle")},
		},
	}
	backend := &recordBackend{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- Watch(ctx, stubStreams{handle: stream}, opencodeSpec(), backend) }()

	// Wait for the done notification, then stop the watcher.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if backend.count() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Watch() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not return after cancel")
	}
	if backend.count() != 1 || backend.notification(0).Trigger != TriggerDone {
		t.Fatalf("expected one done notification, got %+v", backend.got)
	}
}

func TestWatchDedupClearsOnBusy(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	stream := &streamStub{
		events: []msb.StreamEvent{
			{Kind: msb.StreamEventStdout, Data: sseBlockFor("message.part.updated", "ses_A")}, // busy
			{Kind: msb.StreamEventStdout, Data: sseBlockFor("session.idle", "ses_A")},         // done -> notify
			{Kind: msb.StreamEventStdout, Data: sseBlockFor("message.part.updated", "ses_A")}, // busy -> clear claim
			{Kind: msb.StreamEventStdout, Data: sseBlockFor("session.idle", "ses_A")},         // done -> notify again
		},
	}
	sb := msb.NewMockSandbox(msb.SandboxOpts{StreamHandle_: stream})
	inner := &recordBackend{}
	dedup := NewDedup("watchslug-a", StateClaimer{}, inner)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- Watch(ctx, SandboxStreamOpener{Sandbox: sb}, opencodeSpec(), dedup) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if inner.count() == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Watch() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not return after cancel")
	}
	if inner.count() != 2 {
		t.Fatalf("expected 2 notifications after clear-on-busy, got %d", inner.count())
	}
}

func TestWatchOnceSeedsChildrenFromListCommand(t *testing.T) {
	spec := opencodeSpec()
	spec.SessionList = agent.NewSessionList("seed-cmd", func(payload []byte) []string {
		if string(payload) != `{"data":[{"id":"ses_child","parentID":"ses_top"}]}` {
			t.Errorf("SessionListParse got %q", payload)
		}
		return []string{"ses_child"}
	})
	stream := &streamStub{events: []msb.StreamEvent{
		{Kind: msb.StreamEventStdout, Data: sseBlockFor("message.part.updated", "ses_child")},
		{Kind: msb.StreamEventStdout, Data: sseBlockFor("session.idle", "ses_child")},
		{Kind: msb.StreamEventExited, ExitCode: 0},
	}}
	sb := msb.NewMockSandbox(msb.SandboxOpts{
		ShellOut: map[string]msb.ShellResult{
			"seed-cmd": msb.NewTestResult(true, 0, `{"data":[{"id":"ses_child","parentID":"ses_top"}]}`, "", nil),
		},
		StreamHandle_: stream,
	})
	backend := &recordBackend{}
	// The scripted StreamEventExited ends the stream with an error; the
	// assertion is on notifications, not the error.
	_ = watchOnce(context.Background(), SandboxStreamOpener{Sandbox: sb}, NewTracker(spec), backend, spec)
	if backend.count() != 0 {
		t.Fatalf("seeded child notified: %+v", backend.got)
	}
}

// seedFailureStream is a top-level turn whose only notification is the closing
// done, so any test asserting exactly one TriggerDone notification proves a
// failed seed did not block the stream.
func seedFailureStream() *streamStub {
	return &streamStub{events: []msb.StreamEvent{
		{Kind: msb.StreamEventStdout, Data: sseBlockFor("message.part.updated", "ses_top")},
		{Kind: msb.StreamEventStdout, Data: sseBlockFor("session.idle", "ses_top")},
		{Kind: msb.StreamEventExited, ExitCode: 0},
	}}
}

func assertOneDoneNotification(t *testing.T, sb *msb.MockSandbox, spec agent.EventStreamSpec) {
	t.Helper()
	backend := &recordBackend{}
	_ = watchOnce(context.Background(), SandboxStreamOpener{Sandbox: sb}, NewTracker(spec), backend, spec)
	if len(*sb.ShellCalls) != 1 || (*sb.ShellCalls)[0] != "seed-cmd" {
		t.Fatalf("expected the seed command to run, got %v", *sb.ShellCalls)
	}
	if backend.count() != 1 || backend.notification(0).Trigger != TriggerDone {
		t.Fatalf("expected one done notification despite seed failure, got %+v", backend.got)
	}
}

func TestWatchOnceSeedShellErrorDoesNotBlockNotifications(t *testing.T) {
	spec := opencodeSpec()
	spec.SessionList = agent.NewSessionList("seed-cmd", func([]byte) []string {
		t.Fatal("SessionListParse ran despite the shell error")
		return nil
	})
	sb := &msb.MockSandbox{
		ShellErr:      errors.New("boom"),
		ShellCalls:    &[]string{},
		StreamHandle_: seedFailureStream(),
	}
	assertOneDoneNotification(t, sb, spec)
}

// A seed command that runs but exits non-zero (e.g. a missing tool in a custom
// base, where jq's absence makes the list command fail) must not block the
// stream either.
func TestWatchOnceSeedNonSuccessDoesNotBlockNotifications(t *testing.T) {
	spec := opencodeSpec()
	spec.SessionList = agent.NewSessionList("seed-cmd", func([]byte) []string {
		t.Fatal("SessionListParse ran despite the failing command")
		return nil
	})
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			"seed-cmd": msb.NewTestResult(false, 1, "", "jq: command not found", nil),
		},
		ShellCalls:    &[]string{},
		StreamHandle_: seedFailureStream(),
	}
	assertOneDoneNotification(t, sb, spec)
}

// A body the parser cannot read yields no child IDs; that must not block the
// stream, and the empty seed must not suppress the top-level session.
func TestWatchOnceSeedUnparsableDoesNotBlockNotifications(t *testing.T) {
	spec := opencodeSpec()
	spec.SessionList = agent.NewSessionList("seed-cmd", func([]byte) []string { return nil })
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			"seed-cmd": msb.NewTestResult(true, 0, "not json", "", nil),
		},
		ShellCalls:    &[]string{},
		StreamHandle_: seedFailureStream(),
	}
	assertOneDoneNotification(t, sb, spec)
}

func TestWatchReconnectsOnStreamExit(t *testing.T) {
	origPolicy := watchReconnectPolicy
	defer func() { watchReconnectPolicy = origPolicy }()
	watchReconnectPolicy = reconnectPolicy{
		fastRetries:  100,
		backoffStart: time.Millisecond,
		backoffMax:   time.Millisecond,
		longLived:    0,
		jitter:       func(d time.Duration) time.Duration { return d },
	}

	var attempts atomic.Int32
	secondAttempt := make(chan struct{})
	spec := opencodeSpec()
	sb := msb.NewMockSandbox(msb.SandboxOpts{})
	sb.(*msb.MockSandbox).ShellStreamFn = func(command string) (msb.StreamHandle, error) {
		n := attempts.Add(1)
		if command != spec.StreamCommand {
			t.Errorf("ShellStream command = %q, want %q", command, spec.StreamCommand)
		}
		var events []msb.StreamEvent
		if n == 1 {
			// busy then idle so the tracker fires "done" on the first attempt.
			events = append(events,
				msb.StreamEvent{Kind: msb.StreamEventStdout, Data: sseBlock("message.part.updated")},
				msb.StreamEvent{Kind: msb.StreamEventStdout, Data: sseBlock("session.idle")},
			)
		}
		events = append(events, msb.StreamEvent{Kind: msb.StreamEventExited, ExitCode: 0})
		if n == 2 {
			close(secondAttempt)
		}
		return &streamStub{events: events}, nil
	}
	backend := &recordBackend{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-secondAttempt
		cancel()
	}()
	streams := SandboxStreamOpener{Sandbox: sb}
	if err := Watch(ctx, streams, spec, backend); err != nil && !strings.Contains(err.Error(), "dropped") {
		t.Fatalf("Watch() error = %v", err)
	}
	if attempts.Load() < 2 {
		t.Errorf("expected reconnect (>=2 ShellStream calls), got %d", attempts.Load())
	}
	if backend.count() != 1 || backend.notification(0).Trigger != TriggerDone {
		t.Fatalf("expected one done notification, got %+v", backend.got)
	}
}

// eofStreamHandle ends the stream cleanly: Recv reports io.EOF immediately,
// modelling the in-VM stream command exiting without an error.
type eofStreamHandle struct{}

func (eofStreamHandle) Recv(context.Context) (msb.StreamEvent, error) {
	return msb.StreamEvent{}, io.EOF
}

func (eofStreamHandle) Close() error { return nil }

// TestWatchOnceReportsCleanStreamEnd verifies a stream that ends cleanly is
// reported as a drop reason so Watch reconnects; unlike an exit code or a read
// error, a clean end is not an error in itself.
func TestWatchOnceReportsCleanStreamEnd(t *testing.T) {
	sb := msb.NewMockSandbox(msb.SandboxOpts{StreamHandle_: eofStreamHandle{}})
	spec := opencodeSpec()

	err := watchOnce(context.Background(), SandboxStreamOpener{Sandbox: sb}, NewTracker(spec), &recordBackend{}, spec)
	if err == nil || err.Error() != "stream ended cleanly" {
		t.Fatalf("watchOnce() error = %v, want %q", err, "stream ended cleanly")
	}
}
