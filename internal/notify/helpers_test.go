package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"sync"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// fakeCmd wraps exec.Command so buildCmd/Notify can run a real (harmless)
// command while still recording the arguments it was given.
type fakeCmd struct {
	got []string
}

func (f *fakeCmd) cmd(name string, args ...string) *exec.Cmd {
	f.got = append([]string{}, append([]string{name}, args...)...)
	return exec.Command("/bin/true")
}

// recordingBackend records the notifications it receives.
type recordingBackend struct {
	got []Notification
}

func (r *recordingBackend) Notify(n Notification) { r.got = append(r.got, n) }

// sharedSink records notifications from multiple Dedup instances backed by the
// same underlying sink.
type sharedSink struct {
	mu  sync.Mutex
	got []Notification
}

func (s *sharedSink) Notify(n Notification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, n)
}

func (s *sharedSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.got)
}

// alwaysClaimer is a filesystem-free claimer that always wins.
type alwaysClaimer struct{}

func (alwaysClaimer) Acquire(string, string) (func(), bool, error) {
	return func() {}, true, nil
}

// errorClaimer simulates a claim backend that is unavailable.
type errorClaimer struct{}

func (errorClaimer) Acquire(string, string) (func(), bool, error) {
	return nil, false, errors.New("claim backend unavailable")
}

// opencodeSpec returns a representative v1-shaped EventStreamSpec used to drive
// the tracker: it carries the event vocabulary the opencode (v1) profile wires
// for the transitions the tests exercise, not a copy of production's spec.
func opencodeSpec() agent.EventStreamSpec {
	return agent.EventStreamSpec{
		StreamCommand:     "curl -N -s http://127.0.0.1:4096/global/event",
		BusyEvents:        []string{"message.part.updated", "session.updated"},
		AwaitingInput:     []string{"permission.asked", "question.asked"},
		IdleEvents:        []string{"session.idle"},
		ErrorEvents:       []string{"session.error"},
		StickyErrorEvents: []string{"session.error"},
		Name:              "opencode",
		Parse:             parseTestEvent,
	}
}

// parseTestEvent decodes the v1-shaped payloads notify's tests build (see
// sseBlock/sseBlockFor), mirroring agent.parseOpencodeV1Event without the
// parentID field these tests don't exercise.
func parseTestEvent(payload []byte) (agent.StreamEvent, bool) {
	var raw struct {
		Payload struct {
			Type       string `json:"type"`
			Properties struct {
				SessionID string `json:"sessionID"`
			} `json:"properties"`
		} `json:"payload"`
	}
	if json.Unmarshal(payload, &raw) != nil || raw.Payload.Type == "" {
		return agent.StreamEvent{}, false
	}
	return agent.StreamEvent{Type: raw.Payload.Type, SessionID: raw.Payload.Properties.SessionID}, true
}

// opencode2Spec mirrors the v2 profile's transition lists: a turn is one
// execution pair, each step start re-arms busy, and input requests arrive as
// permission.asked / form.created.
func opencode2Spec() agent.EventStreamSpec {
	return agent.EventStreamSpec{
		BusyEvents:    []string{"session.execution.started", "session.step.started"},
		AwaitingInput: []string{"permission.asked", "form.created"},
		IdleEvents:    []string{"session.execution.succeeded"},
		ErrorEvents:   []string{"session.error", "session.execution.failed"},
		Name:          "opencode2",
		Parse:         parseTestEvent,
	}
}

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

// eofStreamHandle ends the stream cleanly: Recv reports io.EOF immediately,
// modelling the in-VM stream command exiting without an error.
type eofStreamHandle struct{}

func (eofStreamHandle) Recv(context.Context) (msb.StreamEvent, error) {
	return msb.StreamEvent{}, io.EOF
}

func (eofStreamHandle) Close() error { return nil }
