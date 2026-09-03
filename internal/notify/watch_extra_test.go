package notify

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
)

func TestWatchNilSinkReturnsImmediately(t *testing.T) {
	if err := Watch(context.Background(), nil, opencodeSpec(), nil); err != nil {
		t.Fatalf("Watch with nil sink should return nil, got %v", err)
	}
}

func TestWatchGivesUpAfterMaxAttempts(t *testing.T) {
	origBackoff := watchBackoff
	defer func() { watchBackoff = origBackoff }()
	watchBackoff = 0

	var attempts atomic.Int32
	sb := msb.NewMockSandbox(msb.SandboxOpts{})
	sb.(*msb.MockSandbox).ShellStreamFn = func(string) (msb.StreamHandle, error) {
		attempts.Add(1)
		return nil, errors.New("shell failed")
	}

	err := Watch(context.Background(), sb, opencodeSpec(), &recordBackend{})
	if err == nil {
		t.Fatal("expected an error after giving up")
	}
	if !strings.Contains(err.Error(), "giving up") {
		t.Errorf("unexpected error message: %v", err)
	}
	if attempts.Load() != maxStreamAttempts {
		t.Errorf("expected %d attempts, got %d", maxStreamAttempts, attempts.Load())
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
