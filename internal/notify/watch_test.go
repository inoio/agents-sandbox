package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// The per-connect seeding runs through the seam too: a fake whose Shell answers
// the session-list command suppresses a child session, with no msb.Sandbox
// anywhere in the watcher.
func TestStubStreamsDrivesChildSeeding(t *testing.T) {
	const sessionListBody = `{"children":["ses_child"]}`
	spec := opencodeSpec()
	spec.SessionList = agent.NewSessionList("seed-cmd", func(payload []byte) []string {
		if string(payload) != sessionListBody {
			t.Errorf("seed payload = %q, want %q", payload, sessionListBody)
		}
		return []string{"ses_child"}
	})
	stream := &streamStub{events: []msb.StreamEvent{
		{Kind: msb.StreamEventStdout, Data: sseBlockFor("message.part.updated", "ses_child")},
		{Kind: msb.StreamEventStdout, Data: sseBlockFor("session.idle", "ses_child")},
		{Kind: msb.StreamEventExited, ExitCode: 0},
	}}
	streams := stubStreams{
		handle: stream,
		shellOut: map[string]msb.ShellResult{
			"seed-cmd": msb.NewTestResult(true, 0, sessionListBody, "", nil),
		},
	}
	backend := &recordBackend{}

	// The scripted exit ends the stream with an error; the assertion is on
	// notifications, not the error. Without the seed the child's busy/idle pair
	// would fire a done notification.
	_ = watchOnce(context.Background(), streams, NewTracker(spec), backend, spec)
	if backend.count() != 0 {
		t.Fatalf("seeded child notified: %+v", backend.got)
	}
}

func TestNextBackoffPhases(t *testing.T) {
	orig := watchReconnectPolicy
	watchReconnectPolicy = reconnectPolicy{
		fastRetries:  2,
		backoffStart: time.Second,
		backoffMax:   4 * time.Second,
		jitter:       func(d time.Duration) time.Duration { return d },
	}
	defer func() { watchReconnectPolicy = orig }()

	cases := []struct {
		fails int
		prev  time.Duration
		want  time.Duration
	}{
		{1, 0, 0},               // fast phase: 1st drop reconnects immediately
		{2, 0, 0},               // fast phase: 2nd drop reconnects immediately
		{3, 0, 1 * time.Second}, // backoff starts after fastRetries
		{4, 1 * time.Second, 2 * time.Second},
		{5, 2 * time.Second, 4 * time.Second}, // capped at backoffMax
		{6, 4 * time.Second, 4 * time.Second}, // stays capped
	}
	for _, tc := range cases {
		if got := nextBackoff(tc.fails, tc.prev); got != tc.want {
			t.Errorf("nextBackoff(%d, %v) = %v, want %v", tc.fails, tc.prev, got, tc.want)
		}
	}
}

func TestWatchNilSinkReturnsImmediately(t *testing.T) {
	if err := Watch(context.Background(), nil, opencodeSpec(), nil); err != nil {
		t.Fatalf("Watch with nil sink should return nil, got %v", err)
	}
}

func TestWatchRetriesUntilCancelled(t *testing.T) {
	orig := watchReconnectPolicy
	watchReconnectPolicy = reconnectPolicy{
		fastRetries:  100,
		backoffStart: time.Millisecond,
		backoffMax:   time.Millisecond,
		longLived:    0,
		jitter:       func(d time.Duration) time.Duration { return d },
	}
	defer func() { watchReconnectPolicy = orig }()

	var attempts atomic.Int32
	sb := msb.NewMockSandbox(msb.SandboxOpts{})
	sb.(*msb.MockSandbox).ShellStreamFn = func(string) (msb.StreamHandle, error) {
		attempts.Add(1)
		return nil, errors.New("shell failed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Watch(ctx, SandboxStreamOpener{Sandbox: sb}, opencodeSpec(), &recordBackend{}) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if attempts.Load() >= 5 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	err := <-done
	if err == nil {
		t.Fatal("expected a compound error after drops")
	}
	if !strings.Contains(err.Error(), "dropped") ||
		strings.Contains(err.Error(), "attempt 0: ") ||
		!strings.Contains(err.Error(), "attempt 1: ") || !strings.Contains(err.Error(), "attempt 2: ") {
		t.Errorf("error = %v, want the drops numbered from attempt 1", err)
	}
	if attempts.Load() < 5 {
		t.Errorf("expected >=5 attempts before cancel, got %d", attempts.Load())
	}
}

// fakeWatchClock is a self-advancing watchClockSource: wait moves the clock
// forward by the backoff instead of blocking, so a test measures the reconnect
// gaps without waiting them out. now reads the current instant; the test can
// advance it by hand to simulate a connection held open past longLived.
type fakeWatchClock struct {
	mu      sync.Mutex
	current time.Time
}

func (c *fakeWatchClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

func (c *fakeWatchClock) wait(ctx context.Context, d time.Duration) bool {
	c.advance(d)
	return ctx.Err() == nil
}

func (c *fakeWatchClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.current = c.current.Add(d)
}

func TestWatchBackoffResetsAfterLongLived(t *testing.T) {
	// longLived must sit well above a failing call's latency and well below the
	// time the long-lived connection is held: a failing call that takes longer
	// than longLived looks long-lived and would wrongly reset the backoff,
	// skipping the wait this test measures.
	//
	// The reset is a strict comparison, so a connection held for exactly
	// longLived is not long-lived and must not reset the backoff.
	cases := []struct {
		name      string
		held      time.Duration
		wantReset bool
	}{
		{"held past longLived", 250 * time.Millisecond, true},
		{"held exactly longLived", 100 * time.Millisecond, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gapBackoff, gapAfterReset := longLivedBackoffGaps(t, tc.held)
			if reset := gapAfterReset < gapBackoff; reset != tc.wantReset {
				t.Errorf("backoff reset = %v, want %v (gapBackoff=%v gapAfterReset=%v)",
					reset, tc.wantReset, gapBackoff, gapAfterReset)
			}
		})
	}
}

// longLivedBackoffGaps drives a watch through three fast failures and then a
// long-lived connection held for exactly held, and returns the reconnect gap
// before that connection and the one after it. The backoff is reset only when
// held exceeds longLived, making the second gap the fast reconnection, smaller
// than the first.
func longLivedBackoffGaps(t *testing.T, held time.Duration) (gapBackoff, gapAfterReset time.Duration) {
	t.Helper()
	orig := watchReconnectPolicy
	watchReconnectPolicy = reconnectPolicy{
		fastRetries:  2,
		backoffStart: time.Second,
		backoffMax:   4 * time.Second,
		longLived:    100 * time.Millisecond,
		jitter:       func(d time.Duration) time.Duration { return d },
	}
	defer func() { watchReconnectPolicy = orig }()

	clock := &fakeWatchClock{current: time.Unix(0, 0)}
	origClock := watchClock
	watchClock = watchClockSource{now: clock.now, wait: clock.wait}
	defer func() { watchClock = origClock }()

	release := make(chan struct{})
	invoked := make(chan time.Time, 8)
	var call int
	sb := msb.NewMockSandbox(msb.SandboxOpts{})
	sb.(*msb.MockSandbox).ShellStreamFn = func(string) (msb.StreamHandle, error) {
		invoked <- clock.now()
		call++
		switch {
		case call <= 3:
			return failHandle{}, nil
		case call == 4:
			// Long-lived connection: held open for held, then drops.
			return &gateHandle{release: release, event: msb.StreamEvent{Kind: msb.StreamEventExited, ExitCode: 0}}, nil
		default:
			// The reconnect after the drop stays open until the test cancels.
			return &streamStub{}, nil
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Watch(ctx, SandboxStreamOpener{Sandbox: sb}, opencodeSpec(), &recordBackend{}) }()

	// Record each ShellStream invocation time as it happens.
	var times []time.Time
	record := func(when string) time.Time {
		select {
		case ts := <-invoked:
			times = append(times, ts)
			return ts
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout waiting for %s invocation", when)
			return time.Time{}
		}
	}
	record("call 1")
	record("call 2")
	record("call 3")
	record("call 4") // long-lived connection now being held
	clock.advance(held)
	close(release)
	record("call 5") // reconnect after the long-lived drop
	cancel()
	<-done

	if len(times) != 5 {
		t.Fatalf("expected 5 invocations, got %d", len(times))
	}
	// gap(3->4) reflects backoffStart after the 3rd consecutive failure.
	gapBackoff = times[3].Sub(times[2])
	gapAfterReset = times[4].Sub(times[3])
	return gapBackoff, gapAfterReset
}

// TestWatchStopsDuringReconnectBackoff pins that cancelling the context while
// Watch waits out a reconnect backoff ends the watch. Jitter fires just before
// the wait and cancels the context, so the cancellation lands inside the wait
// rather than racing an immediate reconnect. The stream is opened exactly once:
// a watch that continued past the cancelled wait would reconnect and open it
// again.
func TestWatchStopsDuringReconnectBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	orig := watchReconnectPolicy
	watchReconnectPolicy = reconnectPolicy{
		fastRetries:  0,
		backoffStart: time.Hour,
		backoffMax:   time.Hour,
		longLived:    time.Hour,
		jitter: func(d time.Duration) time.Duration {
			cancel()
			return d
		},
	}
	defer func() { watchReconnectPolicy = orig }()

	var opened []string
	sb := msb.NewMockSandbox(msb.SandboxOpts{StreamHandle_: failHandle{}, ShellStreamCmds: &opened})
	err := Watch(ctx, SandboxStreamOpener{Sandbox: sb}, opencodeSpec(), &recordBackend{})
	if err == nil || !strings.Contains(err.Error(), "dropped") {
		t.Fatalf("Watch() error = %v, want a drop summary", err)
	}
	if len(opened) != 1 {
		t.Fatalf("ShellStream opened %d times after cancel during backoff, want exactly 1", len(opened))
	}
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

// TestHandleEventSameStepInputRequestsBothNotify drives the full path for the
// v2 gap: two approvals asked back to back in one step, with no step boundary
// between them. The first request claims the input token; the second carries a
// distinct envelope id, claims its own token, and notifies too.
func TestHandleEventSameStepInputRequestsBothNotify(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	sink := &sharedSink{}
	dedup := NewDedup("dedupinturn-b", StateClaimer{}, sink)
	tr := NewTracker(opencode2Spec())
	sid := "ses_top"

	handleEvent(tr, dedup, agent.StreamEvent{Type: "session.execution.started", SessionID: sid})
	handleEvent(tr, dedup, agent.StreamEvent{Type: "permission.asked", SessionID: sid, EventID: "evt_1"})
	handleEvent(tr, dedup, agent.StreamEvent{Type: "permission.asked", SessionID: sid, EventID: "evt_2"})

	if sink.count() != 2 {
		t.Fatalf("expected both same-step input requests to notify, got %d", sink.count())
	}
}

// TestHandleEventRepeatedInputEventNotifiesOnce is the other direction: the
// same envelope id (a replayed event) claims the same token and notifies once.
func TestHandleEventRepeatedInputEventNotifiesOnce(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	sink := &sharedSink{}
	dedup := NewDedup("dedupinturn-c", StateClaimer{}, sink)
	tr := NewTracker(opencode2Spec())
	sid := "ses_top"

	handleEvent(tr, dedup, agent.StreamEvent{Type: "session.execution.started", SessionID: sid})
	handleEvent(tr, dedup, agent.StreamEvent{Type: "permission.asked", SessionID: sid, EventID: "evt_1"})
	handleEvent(tr, dedup, agent.StreamEvent{Type: "permission.asked", SessionID: sid, EventID: "evt_1"})

	if sink.count() != 1 {
		t.Fatalf("expected a repeat of the same input event to notify once, got %d", sink.count())
	}
}

func TestParseSSEBlockDelegatesToSpecParse(t *testing.T) {
	var seen []byte
	parse := func(payload []byte) (agent.StreamEvent, bool) {
		seen = payload
		return agent.StreamEvent{Type: "x"}, true
	}
	got, ok := parseSSEBlock(parse, []string{"event: ping", "data:  {\"a\":1}  "})
	if !ok || got.Type != "x" {
		t.Fatalf("got %+v ok=%v, want delegated event", got, ok)
	}
	if string(seen) != `{"a":1}` {
		t.Errorf("parse payload = %q, want trimmed data line", seen)
	}
	if _, ok := parseSSEBlock(parse, []string{"event: ping", ": comment"}); ok {
		t.Error("a block with no data line must not be an event")
	}
}

func TestSandboxStreamOpenerDelegatesToSandbox(t *testing.T) {
	handle := &streamStub{}
	seed := msb.NewTestResult(true, 0, `{"data":[]}`, "", nil)
	commands := &[]string{}
	sb := &msb.MockSandbox{
		StreamHandle_:   handle,
		ShellStreamCmds: commands,
		ShellOut:        map[string]msb.ShellResult{"seed-cmd": seed},
		ShellCalls:      &[]string{},
	}
	streams := SandboxStreamOpener{Sandbox: sb}

	got, err := streams.ShellStream(context.Background(), "stream-cmd")
	if err != nil || got != handle {
		t.Fatalf("ShellStream() = %v, %v, want the sandbox handle", got, err)
	}
	result, err := streams.Shell(context.Background(), "seed-cmd")
	if err != nil || result != seed {
		t.Fatalf("Shell() = %v, %v, want the sandbox result", result, err)
	}
	if len(*commands) != 1 || (*commands)[0] != "stream-cmd" {
		t.Errorf("ShellStream command = %v, want stream-cmd", *commands)
	}
	if len(*sb.ShellCalls) != 1 || (*sb.ShellCalls)[0] != "seed-cmd" {
		t.Errorf("Shell command = %v, want seed-cmd", *sb.ShellCalls)
	}
}

func TestSandboxStreamOpenerPropagatesErrors(t *testing.T) {
	boom := errors.New("boom")
	sb := &msb.MockSandbox{
		ShellErr:      boom,
		ShellCalls:    &[]string{},
		ShellStreamFn: func(string) (msb.StreamHandle, error) { return nil, boom },
	}
	streams := SandboxStreamOpener{Sandbox: sb}

	if _, err := streams.ShellStream(context.Background(), "stream-cmd"); !errors.Is(err, boom) {
		t.Errorf("ShellStream() error = %v, want %v", err, boom)
	}
	if _, err := streams.Shell(context.Background(), "seed-cmd"); !errors.Is(err, boom) {
		t.Errorf("Shell() error = %v, want %v", err, boom)
	}
}

// TestStreamReader pins how a shell stream becomes an io.Reader: stdout is
// read across calls in pieces as small as the caller's buffer, stderr is
// skipped, an exited or failed stream ends the read with an error naming it, and
// a receive error is returned as is.
func TestStreamReader(t *testing.T) {
	recvFailed := errors.New("recv failed")
	cases := []struct {
		name    string
		events  []msb.StreamEvent
		recvErr error
		bufSize int
		want    string
		wantErr func(error) bool
	}{
		{
			name:    "stdout in pieces",
			events:  []msb.StreamEvent{{Kind: msb.StreamEventStdout, Data: []byte("abcdef")}},
			recvErr: io.EOF,
			bufSize: 2,
			want:    "abcdef",
			wantErr: func(err error) bool { return errors.Is(err, io.EOF) },
		},
		{
			name: "stderr skipped",
			events: []msb.StreamEvent{
				{Kind: msb.StreamEventStderr, Data: []byte("noise")},
				{Kind: msb.StreamEventStdout, Data: []byte("hello")},
			},
			recvErr: recvFailed,
			bufSize: 16,
			want:    "hello",
			wantErr: func(err error) bool { return errors.Is(err, recvFailed) },
		},
		{
			name:    "exited",
			events:  []msb.StreamEvent{{Kind: msb.StreamEventExited, ExitCode: 3}},
			bufSize: 16,
			wantErr: func(err error) bool { return err != nil && err.Error() == "stream exited with code 3" },
		},
		{
			name:    "failed",
			events:  []msb.StreamEvent{{Kind: msb.StreamEventFailed}},
			bufSize: 16,
			wantErr: func(err error) bool { return err != nil && err.Error() == "stream failed" },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &streamReader{ctx: context.Background(), handle: &queuedHandle{events: tc.events, err: tc.recvErr}}
			buf := make([]byte, tc.bufSize)
			var got strings.Builder
			var err error
			for {
				var n int
				n, err = r.Read(buf)
				got.Write(buf[:n])
				if err != nil {
					break
				}
			}
			if got.String() != tc.want || !tc.wantErr(err) {
				t.Errorf("read %q, ended with %v; want %q", got.String(), err, tc.want)
			}
		})
	}
}

func TestDropSummaryBounded(t *testing.T) {
	s := &dropSummary{}
	for i := 1; i <= 10; i++ {
		s.record(i, "reason")
	}
	if s.count != 10 {
		t.Fatalf("count = %d, want 10", s.count)
	}
	if len(s.first) != summaryFirstDrops {
		t.Fatalf("first len = %d, want %d", len(s.first), summaryFirstDrops)
	}
	err := s.err()
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"dropped 10 times", "attempt 10: reason", "7 more"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestDropSummaryNilWhenNoDrops(t *testing.T) {
	if err := (&dropSummary{}).err(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

// sseBlockFor builds an SSE block carrying a sessionID, so dedup is exercised.
func sseBlockFor(typ, sessionID string) []byte {
	return []byte(
		"data: {\"directory\":\"/workspace\",\"project\":\"p\",\"payload\":{\"id\":\"evt\",\"type\":\"" + typ + "\",\"properties\":{\"sessionID\":\"" + sessionID + "\"}}}\n\n",
	)
}

// sseBlock wraps typ in the envelope shape opencode's /global/event stream
// emits: a single data line whose type lives under payload.type.
func sseBlock(typ string) []byte {
	return []byte(
		"data: {\"directory\":\"/workspace\",\"project\":\"cb78d2f0026e9b81b438a362c1fc4dd57422a98d\",\"payload\":{\"id\":\"evt\",\"type\":\"" + typ + "\",\"properties\":{}}}\n\n",
	)
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

// TestWatchOnceReportsWhyTheStreamEnded pins the reason a stream drop carries:
// a stream that cannot be opened or read keeps its cause, a cancelled watch is
// not a drop.
func TestWatchOnceReportsWhyTheStreamEnded(t *testing.T) {
	spec := opencodeSpec()
	readFailed := errors.New("read failed")
	t.Run("read failure", func(t *testing.T) {
		streams := stubStreams{handle: &queuedHandle{err: readFailed}}
		err := watchOnce(context.Background(), streams, NewTracker(spec), &recordBackend{}, spec)
		if !errors.Is(err, readFailed) || !strings.Contains(err.Error(), "reading stream") {
			t.Fatalf("watchOnce() error = %v, want the read failure wrapping %v", err, readFailed)
		}
	})
	t.Run("open failure", func(t *testing.T) {
		openFailed := errors.New("open failed")
		sb := msb.NewMockSandbox(msb.SandboxOpts{ShellStreamFn: func(string) (msb.StreamHandle, error) {
			return nil, openFailed
		}})
		err := watchOnce(
			context.Background(),
			SandboxStreamOpener{Sandbox: sb},
			NewTracker(spec),
			&recordBackend{},
			spec,
		)
		if !errors.Is(err, openFailed) || !strings.Contains(err.Error(), "opening stream") {
			t.Fatalf("watchOnce() error = %v, want the open failure wrapping %v", err, openFailed)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		streams := stubStreams{handle: &queuedHandle{err: readFailed}}
		if err := watchOnce(ctx, streams, NewTracker(spec), &recordBackend{}, spec); err != nil {
			t.Fatalf("watchOnce() on a cancelled watch = %v, want nil", err)
		}
	})
}

// TestDropSummaryAtTheBound pins that exactly summaryFirstDrops drops are all
// listed with no "more" marker, and that the elapsed time runs from the first
// drop.
func TestDropSummaryAtTheBound(t *testing.T) {
	s := &dropSummary{}
	for i := 1; i <= summaryFirstDrops; i++ {
		s.record(i, "reason")
	}
	err := s.err()
	if err == nil || strings.Contains(err.Error(), "more") || !strings.Contains(err.Error(), "over 0s") ||
		!strings.Contains(err.Error(), fmt.Sprintf("dropped %d times", summaryFirstDrops)) {
		t.Fatalf("summary = %v, want %d drops over 0s with no more marker", err, summaryFirstDrops)
	}
	for i := 1; i <= summaryFirstDrops; i++ {
		if !strings.Contains(err.Error(), fmt.Sprintf("attempt %d: reason", i)) {
			t.Errorf("summary = %v, want attempt %d listed", err, i)
		}
	}
}

// busyRecorder is a sink that records the sessions it is told are busy.
type busyRecorder struct {
	recordBackend

	busy []string
}

func (b *busyRecorder) SessionBusy(sessionID string) { b.busy = append(b.busy, sessionID) }

// TestHandleEventReleasesClaimsOnlyForBusySessions pins that a busy event with
// a session tells a dedup sink the session is busy again, and that a busy event
// without a session or any other event does not.
func TestHandleEventReleasesClaimsOnlyForBusySessions(t *testing.T) {
	tracker := NewTracker(opencodeSpec())
	sink := &busyRecorder{}
	handleEvent(tracker, sink, agent.StreamEvent{Type: "message.part.updated"})
	handleEvent(tracker, sink, agent.StreamEvent{Type: "session.idle", SessionID: "ses_1"})
	handleEvent(tracker, sink, agent.StreamEvent{Type: "message.part.updated", SessionID: "ses_1"})
	if !slices.Equal(sink.busy, []string{"ses_1"}) {
		t.Errorf("busy sessions = %v, want only ses_1 from its busy event", sink.busy)
	}
}

// TestWatchOnceReadsLongEventLines pins that an event line longer than the
// scanner's default 64 KiB token is still read, so a large payload does not end
// the stream.
func TestWatchOnceReadsLongEventLines(t *testing.T) {
	spec := opencodeSpec()
	pad := strings.Repeat("x", 100*1024)
	long := "data: {\"payload\":{\"type\":\"session.idle\",\"properties\":{\"sessionID\":\"ses_1\"},\"pad\":\"" + pad + "\"}}\n\n"
	stream := &queuedHandle{
		events: []msb.StreamEvent{
			{Kind: msb.StreamEventStdout, Data: sseBlockFor("message.part.updated", "ses_1")},
			{Kind: msb.StreamEventStdout, Data: []byte(long)},
		},
		err: io.EOF,
	}
	sink := &recordBackend{}
	err := watchOnce(context.Background(), stubStreams{handle: stream}, NewTracker(spec), sink, spec)
	if err == nil || err.Error() != "stream ended cleanly" {
		t.Fatalf("watchOnce() = %v, want the stream read to its end", err)
	}
	if sink.count() != 1 {
		t.Errorf("notifications = %d, want the done from the long event", sink.count())
	}
}
