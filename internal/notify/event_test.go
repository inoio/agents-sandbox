package notify

import "testing"

func TestTrackerBusyToIdleFiresDone(t *testing.T) {
	tr := NewTracker()
	if n := tr.Handle(Event{Type: eventPartUpdated}); n != nil {
		t.Fatalf("unexpected notify before busy: %+v", n)
	}
	// busy -> idle fires done
	n := tr.Handle(Event{Type: eventSessionIdle})
	if n == nil || n.Trigger != TriggerDone {
		t.Fatalf("expected done notification, got %+v", n)
	}
}

func TestTrackerAwaitingInputFiresInput(t *testing.T) {
	tr := NewTracker()
	tr.Handle(Event{Type: eventPartUpdated}) // -> busy
	n := tr.Handle(Event{Type: "permission.updated"})
	if n == nil || n.Trigger != TriggerInput {
		t.Fatalf("expected input notification, got %+v", n)
	}
}

func TestTrackerErrorFiresError(t *testing.T) {
	tr := NewTracker()
	n := tr.Handle(Event{Type: "session.error"})
	if n == nil || n.Trigger != TriggerError {
		t.Fatalf("expected error notification, got %+v", n)
	}
}

func TestTrackerDedupesRepeatedIdle(t *testing.T) {
	tr := NewTracker()
	tr.Handle(Event{Type: eventPartUpdated}) // busy
	tr.Handle(Event{Type: eventSessionIdle}) // -> idle, fires
	if n := tr.Handle(Event{Type: eventSessionIdle}); n != nil {
		t.Fatalf("second idle should be deduped, got %+v", n)
	}
}
