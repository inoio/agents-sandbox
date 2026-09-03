package msb

import (
	"context"
	"errors"
	"testing"
)

// streamHandleStub is a hand-rolled StreamHandle used where MockSandbox's
// ShellStream needs a configured handle; it records the events to return.
type streamHandleStub struct {
	events   []StreamEvent
	closeErr error
	recvIdx  int
}

func (s *streamHandleStub) Recv(_ context.Context) (StreamEvent, error) {
	if s.recvIdx >= len(s.events) {
		return StreamEvent{}, errors.New("stream closed")
	}
	e := s.events[s.recvIdx]
	s.recvIdx++
	return e, nil
}
func (s *streamHandleStub) Close() error { return s.closeErr }

func TestMockSandboxShellStream(t *testing.T) {
	m := NewMockSandbox(SandboxOpts{})
	got, err := m.ShellStream(context.Background(), "curl -N http://x")
	if err != nil {
		t.Fatalf("ShellStream() error = %v", err)
	}
	if got == nil {
		t.Fatal("ShellStream() returned nil handle")
	}
	// The default (no StreamHandle_ configured) reports stream-closed on Recv.
	if _, err := got.Recv(context.Background()); err == nil {
		t.Error("empty handle Recv should report stream closed")
	}
}

func TestMockSandboxShellStreamReturnsConfiguredHandle(t *testing.T) {
	var cmds []string
	stub := &streamHandleStub{events: []StreamEvent{{Kind: StreamEventStdout, Data: []byte("x")}}}
	m := NewMockSandbox(SandboxOpts{StreamHandle_: stub, ShellStreamCmds: &cmds})
	got, err := m.ShellStream(context.Background(), "curl -N http://y")
	if err != nil {
		t.Fatalf("ShellStream() error = %v", err)
	}
	if got != stub {
		t.Fatal("ShellStream() did not return the configured StreamHandle_")
	}
	if len(cmds) != 1 || cmds[0] != "curl -N http://y" {
		t.Errorf("ShellStreamCmds = %v, want the command recorded", cmds)
	}
	ev, err := got.Recv(context.Background())
	if err != nil || ev.Kind != StreamEventStdout || string(ev.Data) != "x" {
		t.Errorf("Recv = %+v, %v; want the configured stdout event", ev, err)
	}
}
