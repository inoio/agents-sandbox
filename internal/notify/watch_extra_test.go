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
	cases := []struct {
		name  string
		lines []string
		want  Event
		ok    bool
	}{
		{"event+data", []string{"event: session.idle", "data: {}"},
			Event{Type: "session.idle", Data: []byte("{}")}, true},
		{"data json type", []string{"data: {\"type\":\"session.error\"}"},
			Event{Type: "session.error", Data: []byte("{\"type\":\"session.error\"}")}, true},
		{"comment ignored", []string{": note", "event: x", "data: {}"},
			Event{Type: "x", Data: []byte("{}")}, true},
		{"empty block", nil, Event{}, false},
		{"no payload", []string{"event: x"}, Event{}, false},
		{"data no type", []string{"data: {}"}, Event{}, false},
		{"data invalid json", []string{"data: not-json"}, Event{}, false},
		{"whitespace trimmed", []string{"event:  y  ", "data:  {}  "}, Event{Type: "y", Data: []byte("{}")}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseSSEBlock(tc.lines)
			if ok != tc.ok {
				t.Fatalf("parseSSEBlock ok = %v, want %v", ok, tc.ok)
			}
			if ok && (got.Type != tc.want.Type || string(got.Data) != string(tc.want.Data)) {
				t.Errorf("parseSSEBlock = %+v, want %+v", got, tc.want)
			}
		})
	}
}
