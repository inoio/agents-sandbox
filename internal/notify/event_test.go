package notify

import (
	"encoding/json"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

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

func TestTrackerBusyToIdleFiresDone(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	if n := tr.Handle(agent.StreamEvent{Type: "message.part.updated"}); n != nil {
		t.Fatalf("unexpected notify before busy: %+v", n)
	}
	n := tr.Handle(agent.StreamEvent{Type: "session.idle"})
	if n == nil || n.Trigger != TriggerDone {
		t.Fatalf("expected done notification, got %+v", n)
	}
}

func TestTrackerAwaitingInputFiresInput(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	tr.Handle(agent.StreamEvent{Type: "message.part.updated"}) // -> busy
	n := tr.Handle(agent.StreamEvent{Type: "permission.asked"})
	if n == nil || n.Trigger != TriggerInput {
		t.Fatalf("expected input notification, got %+v", n)
	}
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

// TestTrackerSameStepInputRequestsBothFire guards the v2 gap where a session
// that has asked for input stays in stateAwaitingInput. A live probe showed a
// turn asking for two approvals back to back in one step (two permission.asked
// events with no step boundary between them), so the second request matched
// neither the busy nor the idle arm of Handle and produced no notification. The
// two events are told apart by their envelope ids, not by a step boundary.
func TestTrackerSameStepInputRequestsBothFire(t *testing.T) {
	tr := NewTracker(opencode2Spec())
	sid := "ses_top"
	tr.Handle(agent.StreamEvent{Type: "session.execution.started", SessionID: sid})
	first := tr.Handle(agent.StreamEvent{Type: "permission.asked", SessionID: sid, EventID: "evt_1"})
	if first == nil || first.Trigger != TriggerInput {
		t.Fatalf("expected first input notification, got %+v", first)
	}
	second := tr.Handle(agent.StreamEvent{Type: "permission.asked", SessionID: sid, EventID: "evt_2"})
	if second == nil || second.Trigger != TriggerInput {
		t.Fatalf("expected second same-step input notification, got %+v", second)
	}
}

// TestTrackerRepeatedInputEventFiresOnce guards the other direction: a replayed
// or re-delivered input event (same envelope id) must not notify again.
func TestTrackerRepeatedInputEventFiresOnce(t *testing.T) {
	tr := NewTracker(opencode2Spec())
	sid := "ses_top"
	tr.Handle(agent.StreamEvent{Type: "session.execution.started", SessionID: sid})
	if n := tr.Handle(agent.StreamEvent{Type: "permission.asked", SessionID: sid, EventID: "evt_1"}); n == nil {
		t.Fatalf("expected first input notification")
	}
	if n := tr.Handle(agent.StreamEvent{Type: "permission.asked", SessionID: sid, EventID: "evt_1"}); n != nil {
		t.Fatalf("same input event should not notify again, got %+v", n)
	}
}

func TestTrackerErrorFiresError(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	n := tr.Handle(agent.StreamEvent{Type: "session.error"})
	if n == nil || n.Trigger != TriggerError {
		t.Fatalf("expected error notification, got %+v", n)
	}
}

func TestTrackerDedupesRepeatedIdle(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	tr.Handle(agent.StreamEvent{Type: "message.part.updated"}) // busy
	tr.Handle(agent.StreamEvent{Type: "session.idle"})         // -> idle, fires
	if n := tr.Handle(agent.StreamEvent{Type: "session.idle"}); n != nil {
		t.Fatalf("second idle should be deduped, got %+v", n)
	}
}

func TestTrackerTitlesUseSpecName(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	tr.Handle(agent.StreamEvent{Type: "message.part.updated"}) // busy
	n := tr.Handle(agent.StreamEvent{Type: "session.idle"})
	if n == nil || n.Title != "opencode: done" {
		t.Fatalf("expected title 'opencode: done', got %+v", n)
	}
	tr.Handle(agent.StreamEvent{Type: "message.part.updated"}) // busy
	n = tr.Handle(agent.StreamEvent{Type: "question.asked"})
	if n == nil || n.Title != "opencode: input needed" {
		t.Fatalf("expected title 'opencode: input needed', got %+v", n)
	}
	tr.Handle(agent.StreamEvent{Type: "message.part.updated"}) // busy
	n = tr.Handle(agent.StreamEvent{Type: "session.error"})
	if n == nil || n.Title != "opencode: error" {
		t.Fatalf("expected title 'opencode: error', got %+v", n)
	}
}

func TestTrackerIgnoresEventsOutsideSpec(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	if n := tr.Handle(agent.StreamEvent{Type: "some.unknown.event"}); n != nil {
		t.Fatalf("unknown event should not notify, got %+v", n)
	}
	if tr.states[""] != stateIdle {
		t.Fatalf("unknown event should not change state, got %v", tr.states[""])
	}
}

func TestTrackerPerSessionIsolation(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	// Session A busy; session B idle with no prior busy state must not fire.
	tr.Handle(agent.StreamEvent{Type: "message.part.updated", SessionID: "A"})
	if n := tr.Handle(agent.StreamEvent{Type: "session.idle", SessionID: "B"}); n != nil {
		t.Fatalf("idle for never-busy session B should not notify, got %+v", n)
	}
	// B goes busy then idle -> fires for B only, carrying B's sessionID.
	tr.Handle(agent.StreamEvent{Type: "message.part.updated", SessionID: "B"})
	n := tr.Handle(agent.StreamEvent{Type: "session.idle", SessionID: "B"})
	if n == nil || n.Trigger != TriggerDone || n.SessionID != "B" {
		t.Fatalf("expected done for session B, got %+v", n)
	}
	// A is still busy; its subsequent idle fires independently.
	n = tr.Handle(agent.StreamEvent{Type: "session.idle", SessionID: "A"})
	if n == nil || n.SessionID != "A" {
		t.Fatalf("expected done for session A, got %+v", n)
	}
}

func TestTrackerSuppressesChildSessions(t *testing.T) {
	spec := opencodeSpec()
	spec.SessionEvents = []string{"session.created", "session.updated"}
	tr := NewTracker(spec)

	// Child announced by session.created: its busy->idle turn must not notify.
	tr.Handle(agent.StreamEvent{Type: "session.created", SessionID: "ses_child", ParentID: "ses_root"})
	if n := tr.Handle(agent.StreamEvent{Type: "message.part.updated", SessionID: "ses_child"}); n != nil {
		t.Fatalf("child busy notified: %+v", n)
	}
	if n := tr.Handle(agent.StreamEvent{Type: "session.idle", SessionID: "ses_child"}); n != nil {
		t.Fatalf("child idle notified: %+v", n)
	}

	// Top-level in the same tracker still notifies.
	tr.Handle(agent.StreamEvent{Type: "message.part.updated", SessionID: "ses_root"})
	n := tr.Handle(agent.StreamEvent{Type: "session.idle", SessionID: "ses_root"})
	if n == nil || n.Trigger != TriggerDone {
		t.Fatalf("top-level idle did not notify: %+v", n)
	}
}

func TestTrackerSeededChildIsSuppressed(t *testing.T) {
	spec := opencodeSpec()
	spec.SessionEvents = []string{"session.created", "session.updated"}
	tr := NewTracker(spec)
	tr.SeedChildren([]string{"ses_seeded", ""})

	tr.Handle(agent.StreamEvent{Type: "message.part.updated", SessionID: "ses_seeded"})
	if n := tr.Handle(agent.StreamEvent{Type: "session.idle", SessionID: "ses_seeded"}); n != nil {
		t.Fatalf("seeded child notified: %+v", n)
	}
}

func TestTrackerTopLevelSessionUpdatedStillBusy(t *testing.T) {
	spec := opencodeSpec()
	spec.SessionEvents = []string{"session.created", "session.updated"}
	tr := NewTracker(spec)

	// session.updated is both a lifecycle event and a busy event; top-level must mark busy.
	tr.Handle(agent.StreamEvent{Type: "session.updated", SessionID: "ses_root"})
	n := tr.Handle(agent.StreamEvent{Type: "session.idle", SessionID: "ses_root"})
	if n == nil || n.Trigger != TriggerDone {
		t.Fatalf("top-level session.updated did not mark busy: %+v", n)
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
	tr.Handle(agent.StreamEvent{Type: "busy.evt"})
	n := tr.Handle(agent.StreamEvent{Type: "idle.evt"})
	if n == nil || n.Trigger != TriggerDone || n.Title != "pi: done" {
		t.Fatalf("expected 'pi: done' done notification, got %+v", n)
	}
}

// TestTrackerErrorStickiness pins the sticky/turn-scoped split. v2's execution
// model is per-turn, so a failed turn must not mute the session; v1's
// session.error latches as before.
func TestTrackerErrorStickiness(t *testing.T) {
	turnScoped := opencodeSpec()
	turnScoped.BusyEvents = []string{"session.execution.started"}
	turnScoped.IdleEvents = []string{"session.execution.succeeded"}
	turnScoped.ErrorEvents = []string{"session.error", "session.execution.failed"}
	turnScoped.StickyErrorEvents = nil

	sticky := turnScoped
	sticky.StickyErrorEvents = []string{"session.error"}

	type step struct {
		event string
		want  string // "input"/"done"/"error", or "" for no notification
	}
	tests := []struct {
		name  string
		spec  agent.EventStreamSpec
		steps []step
	}{
		{
			name: "turn-scoped error does not mute the next turn",
			spec: turnScoped,
			steps: []step{
				{event: "session.execution.started"},
				{event: "session.execution.failed", want: "error"},
				{event: "session.execution.started"},
				{event: "session.execution.succeeded", want: "done"},
			},
		},
		{
			name: "sticky error latches the session",
			spec: sticky,
			steps: []step{
				{event: "session.error", want: "error"},
				{event: "session.execution.started"},
				{event: "session.execution.succeeded"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := NewTracker(tc.spec)
			for _, s := range tc.steps {
				n := tr.Handle(agent.StreamEvent{Type: s.event, SessionID: "ses_1"})
				got := ""
				if n != nil {
					switch n.Trigger {
					case TriggerInput:
						got = "input"
					case TriggerDone:
						got = "done"
					case TriggerError:
						got = "error"
					}
				}
				if got != s.want {
					t.Fatalf("%s: got %q, want %q", s.event, got, s.want)
				}
			}
		})
	}
}
