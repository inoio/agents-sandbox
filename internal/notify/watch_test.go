package notify

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
)

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
			{Kind: msb.StreamEventStdout, Data: []byte("event: message.part.updated\ndata: {}\n\n")},
			{Kind: msb.StreamEventStdout, Data: []byte("event: session.idle\ndata: {}\n\n")},
		},
	}
	sb := msb.NewMockSandbox(msb.SandboxOpts{StreamHandle_: stream})
	backend := &recordBackend{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- Watch(ctx, sb, opencodeSpec(), backend) }()

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

func TestWatchReconnectsOnStreamExit(t *testing.T) {
	origBackoff := watchBackoff
	defer func() { watchBackoff = origBackoff }()
	watchBackoff = time.Millisecond

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
				msb.StreamEvent{Kind: msb.StreamEventStdout, Data: []byte("event: message.part.updated\ndata: {}\n\n")},
				msb.StreamEvent{Kind: msb.StreamEventStdout, Data: []byte("event: session.idle\ndata: {}\n\n")},
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
	if err := Watch(ctx, sb, spec, backend); err != nil {
		t.Fatalf("Watch() error = %v", err)
	}
	if attempts.Load() < 2 {
		t.Errorf("expected reconnect (>=2 ShellStream calls), got %d", attempts.Load())
	}
	if backend.count() != 1 || backend.notification(0).Trigger != TriggerDone {
		t.Fatalf("expected one done notification, got %+v", backend.got)
	}
}
