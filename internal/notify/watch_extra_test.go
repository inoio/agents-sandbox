package notify

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
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
	go func() { done <- Watch(ctx, sb, opencodeSpec(), &recordBackend{}) }()

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

func TestParseSSEBlock(t *testing.T) {
	// Fixtures are data payloads copied verbatim from a captured notify.log of
	// opencode's /global/event SSE stream. Every block is a single "data:"
	// line; the event type always lives under "payload.type".
	sessionDiff := `{"directory":"/workspace","project":"cb78d2f0026e9b81b438a362c1fc4dd57422a98d","payload":{"id":"evt_0678bb9bd0018FCXVXQrtX4I3g","type":"session.diff","properties":{"sessionID":"ses_f987482bcffeiws2F2jQHsI18v","diff":[]}}}`
	sessionStatusBusy := `{"directory":"/workspace","project":"cb78d2f0026e9b81b438a362c1fc4dd57422a98d","payload":{"id":"evt_0678bba0f001hCPf5iyi5kLegH","type":"session.status","properties":{"sessionID":"ses_f987482bcffeiws2F2jQHsI18v","status":{"type":"busy"}}}}`
	sessionStatusIdle := `{"directory":"/workspace","project":"cb78d2f0026e9b81b438a362c1fc4dd57422a98d","payload":{"id":"evt_0678bc91b001QnqxY7PC9c2Wum","type":"session.status","properties":{"sessionID":"ses_f987482bcffeiws2F2jQHsI18v","status":{"type":"idle"}}}}`
	sessionIdle := `{"directory":"/workspace","project":"cb78d2f0026e9b81b438a362c1fc4dd57422a98d","payload":{"id":"evt_0678bc91b0026VCCESi2adL2qk","type":"session.idle","properties":{"sessionID":"ses_f987482bcffeiws2F2jQHsI18v"}}}`
	serverConnected := `{"payload":{"id":"evt_0678b601c001E0tYa1LEd8TOcp","type":"server.connected","properties":{}}}`
	syncUpdated := `{"directory":"/workspace","project":"cb78d2f0026e9b81b438a362c1fc4dd57422a98d","payload":{"type":"sync","syncEvent":{"id":"evt_0678b7d66001s60UiMgCEv2U81","type":"session.updated.1","seq":3,"aggregateID":"ses_f987482bcffeiws2F2jQHsI18v","data":{"sessionID":"ses_f987482bcffeiws2F2jQHsI18v","info":{"id":"ses_f987482bcffeiws2F2jQHsI18v","slug":"crisp-nebula","projectID":"cb78d2f0026e9b81b438a362c1fc4dd57422a98d","directory":"/workspace","path":"","title":"New session - 2026-09-03T13:53:09.955Z","agent":"build","model":{"id":"deepseek-v4-flash-0731-aki","providerID":"litellm","variant":"low"},"version":"1.18.26","cost":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1788443589955,"updated":1788443589989}}}},"id":"evt_0678b7d66001s60UiMgCEv2U81"}}`

	cases := []struct {
		name  string
		lines []string
		want  Event
		ok    bool
	}{
		{
			"session diff",
			[]string{"data: " + sessionDiff},
			Event{Type: "session.diff", SessionID: "ses_f987482bcffeiws2F2jQHsI18v", Data: []byte(sessionDiff)},
			true,
		},
		{
			"session status busy",
			[]string{"data: " + sessionStatusBusy},
			Event{Type: "session.status", SessionID: "ses_f987482bcffeiws2F2jQHsI18v", Data: []byte(sessionStatusBusy)},
			true,
		},
		{
			"session status idle",
			[]string{"data: " + sessionStatusIdle},
			Event{Type: "session.status", SessionID: "ses_f987482bcffeiws2F2jQHsI18v", Data: []byte(sessionStatusIdle)},
			true,
		},
		{
			"session idle",
			[]string{"data: " + sessionIdle},
			Event{Type: "session.idle", SessionID: "ses_f987482bcffeiws2F2jQHsI18v", Data: []byte(sessionIdle)},
			true,
		},
		{
			"bare envelope",
			[]string{"data: " + serverConnected},
			Event{Type: "server.connected", Data: []byte(serverConnected)},
			true,
		},
		{"sync envelope", []string{"data: " + syncUpdated}, Event{Type: "sync", Data: []byte(syncUpdated)}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseSSEBlock(tc.lines)
			if ok != tc.ok {
				t.Fatalf("parseSSEBlock ok = %v, want %v", ok, tc.ok)
			}
			if ok &&
				(got.Type != tc.want.Type || got.SessionID != tc.want.SessionID || string(got.Data) != string(tc.want.Data)) {
				t.Errorf("parseSSEBlock = %+v, want %+v", got, tc.want)
			}
		})
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
	orig := watchReconnectPolicy
	watchReconnectPolicy = reconnectPolicy{
		fastRetries:  2,
		backoffStart: 100 * time.Millisecond,
		backoffMax:   400 * time.Millisecond,
		longLived:    5 * time.Millisecond,
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
	go func() { done <- Watch(ctx, sb, opencodeSpec(), &recordBackend{}) }()

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
	record("call 4")                  // long-lived connection now being held
	time.Sleep(10 * time.Millisecond) // exceed longLived (5ms)
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
