package notify

import (
	"testing"

	"github.com/inoio/opencode-sandbox/internal/agent"
)

// opencodeSpec returns the EventStreamSpec the opencode profile provides, so
// notify tests exercise the same event vocabulary production uses.
func opencodeSpec() agent.EventStreamSpec {
	return agent.EventStreamSpec{
		StreamCommand: "curl -N -s http://127.0.0.1:4096/global/event",
		BusyEvents:    []string{"message.part.updated", "session.updated"},
		AwaitingInput: []string{"permission.updated", "question.asked"},
		IdleEvents:    []string{"session.idle"},
		ErrorEvents:   []string{"session.error"},
		Name:          "opencode",
	}
}

func TestTrackerBusyToIdleFiresDone(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	if n := tr.Handle(Event{Type: "message.part.updated"}); n != nil {
		t.Fatalf("unexpected notify before busy: %+v", n)
	}
	n := tr.Handle(Event{Type: "session.idle"})
	if n == nil || n.Trigger != TriggerDone {
		t.Fatalf("expected done notification, got %+v", n)
	}
}

func TestTrackerAwaitingInputFiresInput(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	tr.Handle(Event{Type: "message.part.updated"}) // -> busy
	n := tr.Handle(Event{Type: "permission.updated"})
	if n == nil || n.Trigger != TriggerInput {
		t.Fatalf("expected input notification, got %+v", n)
	}
}

func TestTrackerErrorFiresError(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	n := tr.Handle(Event{Type: "session.error"})
	if n == nil || n.Trigger != TriggerError {
		t.Fatalf("expected error notification, got %+v", n)
	}
}

func TestTrackerDedupesRepeatedIdle(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	tr.Handle(Event{Type: "message.part.updated"}) // busy
	tr.Handle(Event{Type: "session.idle"})         // -> idle, fires
	if n := tr.Handle(Event{Type: "session.idle"}); n != nil {
		t.Fatalf("second idle should be deduped, got %+v", n)
	}
}

func TestTrackerTitlesUseSpecName(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	tr.Handle(Event{Type: "message.part.updated"}) // busy
	n := tr.Handle(Event{Type: "session.idle"})
	if n == nil || n.Title != "opencode: done" {
		t.Fatalf("expected title 'opencode: done', got %+v", n)
	}
	tr.Handle(Event{Type: "message.part.updated"}) // busy
	n = tr.Handle(Event{Type: "question.asked"})
	if n == nil || n.Title != "opencode: input needed" {
		t.Fatalf("expected title 'opencode: input needed', got %+v", n)
	}
	tr.Handle(Event{Type: "message.part.updated"}) // busy
	n = tr.Handle(Event{Type: "session.error"})
	if n == nil || n.Title != "opencode: error" {
		t.Fatalf("expected title 'opencode: error', got %+v", n)
	}
}

func TestTrackerIgnoresEventsOutsideSpec(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	if n := tr.Handle(Event{Type: "some.unknown.event"}); n != nil {
		t.Fatalf("unknown event should not notify, got %+v", n)
	}
	if tr.state != stateIdle {
		t.Fatalf("unknown event should not change state, got %v", tr.state)
	}
}

func TestTrackerHonorsCustomSpec(t *testing.T) {
	spec := agent.EventStreamSpec{
		StreamCommand: "curl -N -s http://127.0.0.1:9999/events",
		BusyEvents:    []string{"busy.evt"},
		AwaitingInput: []string{"ask.evt"},
		IdleEvents:    []string{"idle.evt"},
		ErrorEvents:   []string{"fail.evt"},
		Name:          "pi",
	}
	tr := NewTracker(spec)
	tr.Handle(Event{Type: "busy.evt"})
	n := tr.Handle(Event{Type: "idle.evt"})
	if n == nil || n.Trigger != TriggerDone || n.Title != "pi: done" {
		t.Fatalf("expected 'pi: done' done notification, got %+v", n)
	}
}
