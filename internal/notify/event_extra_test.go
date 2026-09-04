package notify

import "testing"

func TestHandleEventType(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	if n := tr.HandleEventType("message.part.updated"); n != nil {
		t.Fatalf("busy event should not notify, got %+v", n)
	}
	n := tr.HandleEventType("session.idle")
	if n == nil || n.Trigger != TriggerDone {
		t.Fatalf("expected done notification, got %+v", n)
	}
}

func TestTrackerAwaitingInputFromIdle(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	n := tr.Handle(Event{Type: "question.asked"})
	if n == nil || n.Trigger != TriggerInput {
		t.Fatalf("expected input notification from idle, got %+v", n)
	}
}

func TestTrackerAwaitingInputIsIdempotent(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	tr.Handle(Event{Type: "message.part.updated"}) // -> busy
	tr.Handle(Event{Type: "question.asked"})       // -> awaiting input, fires
	if n := tr.Handle(Event{Type: "question.asked"}); n != nil {
		t.Fatalf("second awaiting-input should be deduped, got %+v", n)
	}
}

func TestTrackerStuckOnError(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	tr.Handle(Event{Type: "session.error"}) // -> error
	if n := tr.Handle(Event{Type: "message.part.updated"}); n != nil {
		t.Fatalf("busy event after error should not notify, got %+v", n)
	}
	if tr.states[""] != stateError {
		t.Fatalf("state should stay error, got %v", tr.states[""])
	}
}

func TestTrackerDoneFromAwaitingInput(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	tr.Handle(Event{Type: "question.asked"}) // idle -> awaiting input
	n := tr.Handle(Event{Type: "session.idle"})
	if n == nil || n.Trigger != TriggerDone {
		t.Fatalf("expected done notification from awaiting input, got %+v", n)
	}
}

func TestApplyOverrideOn(t *testing.T) {
	base := Config{Desktop: false, Audio: AudioOff, OnInput: true, OnDone: true, OnError: true}
	got := ApplyOverride(base, OverrideOn)
	if !got.Desktop || got.Audio != AudioSystem {
		t.Errorf("OverrideOn should enable desktop + system audio, got %+v", got)
	}
	if !got.OnInput || !got.OnDone || !got.OnError {
		t.Errorf("OverrideOn should preserve triggers, got %+v", got)
	}
}
