package msb

import (
	"context"
	"errors"
	"io"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
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

func TestMapExecEvent(t *testing.T) {
	tests := []struct {
		name string
		ev   msbSdk.ExecEvent
		want StreamEvent
		ok   bool
		err  error
	}{
		{
			name: "stdout",
			ev:   msbSdk.ExecEvent{Kind: msbSdk.ExecEventStdout, Data: []byte("x")},
			want: StreamEvent{Kind: StreamEventStdout, Data: []byte("x")},
			ok:   true,
		},
		{
			name: "stderr",
			ev:   msbSdk.ExecEvent{Kind: msbSdk.ExecEventStderr, Data: []byte("e")},
			want: StreamEvent{Kind: StreamEventStderr, Data: []byte("e")},
			ok:   true,
		},
		{
			name: "exited",
			ev:   msbSdk.ExecEvent{Kind: msbSdk.ExecEventExited, ExitCode: 3},
			want: StreamEvent{Kind: StreamEventExited, ExitCode: 3},
			ok:   true,
		},
		{
			name: "failed",
			ev:   msbSdk.ExecEvent{Kind: msbSdk.ExecEventFailed},
			want: StreamEvent{Kind: StreamEventFailed},
			ok:   true,
		},
		{
			name: "done is io.EOF",
			ev:   msbSdk.ExecEvent{Kind: msbSdk.ExecEventDone},
			err:  io.EOF,
		},
		{
			name: "started is skipped",
			ev:   msbSdk.ExecEvent{Kind: msbSdk.ExecEventStarted},
			ok:   false,
		},
		{
			name: "stdin error is skipped",
			ev:   msbSdk.ExecEvent{Kind: msbSdk.ExecEventStdinError},
			ok:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := mapExecEvent(tt.ev)
			if !errors.Is(err, tt.err) {
				t.Fatalf("mapExecEvent() err = %v, want %v", err, tt.err)
			}
			if ok != tt.ok {
				t.Fatalf("mapExecEvent() ok = %v, want %v", ok, tt.ok)
			}
			if got.Kind != tt.want.Kind || string(got.Data) != string(tt.want.Data) ||
				got.ExitCode != tt.want.ExitCode {
				t.Errorf("mapExecEvent() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
