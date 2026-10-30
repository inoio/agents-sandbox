package notify

import (
	"context"
	"errors"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// stubStreams implements StreamOpener and nothing else: no msb.Sandbox is
// behind it, which is the point of the seam. The two operations the watcher
// uses are enough to drive it, on both the stream and the seed path.
type stubStreams struct {
	handle   msb.StreamHandle
	shellOut map[string]msb.ShellResult
}

func (s stubStreams) ShellStream(context.Context, string) (msb.StreamHandle, error) {
	return s.handle, nil
}

func (s stubStreams) Shell(_ context.Context, command string) (msb.ShellResult, error) {
	if result, ok := s.shellOut[command]; ok {
		return result, nil
	}
	return nil, errors.New("stubStreams.Shell not scripted")
}

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
