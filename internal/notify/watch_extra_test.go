package notify

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

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
	if !strings.Contains(err.Error(), "dropped") {
		t.Errorf("unexpected error: %v", err)
	}
	if attempts.Load() < 5 {
		t.Errorf("expected >=5 attempts before cancel, got %d", attempts.Load())
	}
}

// queuedHandle returns a fixed sequence of events then a terminal error.
type queuedHandle struct {
	events []msb.StreamEvent
	err    error
}

func (q *queuedHandle) Recv(context.Context) (msb.StreamEvent, error) {
	if len(q.events) == 0 {
		return msb.StreamEvent{}, q.err
	}
	e := q.events[0]
	q.events = q.events[1:]
	return e, nil
}

func (q *queuedHandle) Close() error { return nil }

func TestStreamReaderIgnoresStderr(t *testing.T) {
	h := &queuedHandle{
		events: []msb.StreamEvent{
			{Kind: msb.StreamEventStderr, Data: []byte("noise")},
			{Kind: msb.StreamEventStdout, Data: []byte("hello")},
		},
		err: errors.New("done"),
	}
	r := &streamReader{ctx: context.Background(), handle: h}
	buf := make([]byte, 16)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(buf[:n]) != "hello" {
		t.Errorf("got %q, want %q", buf[:n], "hello")
	}
}

func TestStreamReaderExitedReturnsError(t *testing.T) {
	h := &queuedHandle{events: []msb.StreamEvent{{Kind: msb.StreamEventExited, ExitCode: 3}}}
	r := &streamReader{ctx: context.Background(), handle: h}
	_, err := r.Read(make([]byte, 16))
	if err == nil || !strings.Contains(err.Error(), "code 3") {
		t.Errorf("expected exit-code error, got %v", err)
	}
}

func TestStreamReaderFailedReturnsError(t *testing.T) {
	h := &queuedHandle{events: []msb.StreamEvent{{Kind: msb.StreamEventFailed}}}
	r := &streamReader{ctx: context.Background(), handle: h}
	_, err := r.Read(make([]byte, 16))
	if err == nil || !strings.Contains(err.Error(), "failed") {
		t.Errorf("expected failed error, got %v", err)
	}
}

func TestStreamReaderSplitsChunks(t *testing.T) {
	h := &queuedHandle{
		events: []msb.StreamEvent{{Kind: msb.StreamEventStdout, Data: []byte("abcdef")}},
		err:    io.EOF,
	}
	r := &streamReader{ctx: context.Background(), handle: h}
	buf := make([]byte, 2)
	var got strings.Builder
	for {
		n, err := r.Read(buf)
		got.Write(buf[:n])
		if err != nil {
			break
		}
	}
	if got.String() != "abcdef" {
		t.Errorf("got %q, want %q", got.String(), "abcdef")
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

// gateHandle blocks on Recv until release fires, then returns event/err. It is
// used to hold a stream open past the longLived threshold in tests.
type gateHandle struct {
	release chan struct{}
	event   msb.StreamEvent
	err     error
}

func (g *gateHandle) Recv(ctx context.Context) (msb.StreamEvent, error) {
	select {
	case <-g.release:
		return g.event, g.err
	case <-ctx.Done():
		return msb.StreamEvent{}, ctx.Err()
	}
}

func (g *gateHandle) Close() error { return nil }

// failHandle fails immediately on Recv, simulating a stream that drops at once.
type failHandle struct{}

func (failHandle) Recv(context.Context) (msb.StreamEvent, error) {
	return msb.StreamEvent{}, errors.New("stream failed")
}

func (failHandle) Close() error { return nil }

func TestWatchBackoffResetsAfterLongLived(t *testing.T) {
	// longLived must sit well above a failing call's latency and well below the
	// time the long-lived connection is held: a failing call that takes longer
	// than longLived looks long-lived and would wrongly reset the backoff,
	// skipping the wait this test measures.
	orig := watchReconnectPolicy
	watchReconnectPolicy = reconnectPolicy{
		fastRetries:  2,
		backoffStart: time.Second,
		backoffMax:   4 * time.Second,
		longLived:    100 * time.Millisecond,
		jitter:       func(d time.Duration) time.Duration { return d },
	}
	defer func() { watchReconnectPolicy = orig }()

	release := make(chan struct{})
	invoked := make(chan time.Time, 8)
	var call int
	sb := msb.NewMockSandbox(msb.SandboxOpts{})
	sb.(*msb.MockSandbox).ShellStreamFn = func(string) (msb.StreamHandle, error) {
		invoked <- time.Now()
		call++
		switch {
		case call <= 3:
			return failHandle{}, nil
		case call == 4:
			// Long-lived connection: held open past longLived, then drops.
			return &gateHandle{release: release, event: msb.StreamEvent{Kind: msb.StreamEventExited, ExitCode: 0}}, nil
		default:
			return failHandle{}, nil
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
	record("call 4")                   // long-lived connection now being held
	time.Sleep(250 * time.Millisecond) // exceed longLived (100ms)
	close(release)
	record("call 5") // reconnect after the long-lived drop
	cancel()
	<-done

	if len(times) != 5 {
		t.Fatalf("expected 5 invocations, got %d", len(times))
	}
	// gap(3->4) reflects backoffStart after the 3rd consecutive failure.
	gapBackoff := times[3].Sub(times[2])
	// After the long-lived reset, the next drop reconnects immediately.
	gapAfterReset := times[4].Sub(times[3])
	if gapAfterReset >= gapBackoff {
		t.Errorf("expected backoff reset after long-lived connection: gapBackoff=%v gapAfterReset=%v",
			gapBackoff, gapAfterReset)
	}
}

// TestWatchStopsDuringReconnectBackoff exercises cancelling the context while
// Watch waits out a reconnect backoff. The other Watch tests use a zero
// backoff, where the "wait" races an immediate reconnect, so whether the
// ctx.Done case of that select runs is timing-dependent (and coverage flaky).
// Here jitter fires just before the wait and cancels the context, so the
// select deterministically takes the ctx.Done branch.
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

	sb := msb.NewMockSandbox(msb.SandboxOpts{StreamHandle_: failHandle{}})
	err := Watch(ctx, SandboxStreamOpener{Sandbox: sb}, opencodeSpec(), &recordBackend{})
	if err == nil || !strings.Contains(err.Error(), "dropped") {
		t.Fatalf("Watch() error = %v, want a drop summary", err)
	}
}
