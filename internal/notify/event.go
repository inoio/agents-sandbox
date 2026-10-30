package notify

import (
	"slices"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// Tracker derives notification transitions from a stream of events, tracking
// each session independently.
type Tracker struct {
	spec     agent.EventStreamSpec
	states   map[string]state
	children map[string]bool
	// lastInput records, per session, the envelope id of the input event that
	// last notified. A turn can ask for two approvals back to back in one step,
	// so the session is already awaiting input when the second arrives; its
	// distinct event id is what lets it notify again without a step boundary.
	lastInput map[string]string
}

type state int

const (
	stateIdle state = iota
	stateBusy
	stateAwaitingInput
	stateError
)

// NewTracker returns a Tracker starting with no known sessions and driven by
// spec. An absent session is treated as stateIdle.
func NewTracker(spec agent.EventStreamSpec) *Tracker {
	return &Tracker{
		spec:      spec,
		states:    map[string]state{},
		children:  map[string]bool{},
		lastInput: map[string]string{},
	}
}

// SeedChildren marks each non-empty id as a child session, so a subagent
// created before the watcher connected is suppressed. Empty ids are skipped so
// a malformed seed can never mark the empty session id a child.
func (t *Tracker) SeedChildren(ids []string) {
	for _, id := range ids {
		if id != "" {
			t.children[id] = true
		}
	}
}

// Handle feeds an event and returns a Notification when a configured
// transition fires for that event's session, else nil. A session whose parent
// ID is seen on a lifecycle event becomes a child; no notification ever fires
// for a child session.
func (t *Tracker) Handle(e agent.StreamEvent) *Notification {
	if e.SessionID != "" && eventTypeIn(e.Type, t.spec.SessionEvents) && e.ParentID != "" {
		t.children[e.SessionID] = true
	}
	if t.children[e.SessionID] {
		return nil
	}
	st := t.states[e.SessionID]
	switch {
	case eventTypeIn(e.Type, t.spec.BusyEvents):
		if st != stateError {
			t.states[e.SessionID] = stateBusy
		}
	case eventTypeIn(e.Type, t.spec.AwaitingInput):
		// A request notifies from busy or idle as before. From awaiting input it
		// notifies only when its envelope id is new, so a second request in the
		// same step (no step boundary to re-arm busy) still alerts while a
		// repeat of the same event does not.
		newRequest := e.EventID != "" && e.EventID != t.lastInput[e.SessionID]
		if st != stateError && (st == stateBusy || st == stateIdle || newRequest) {
			t.lastInput[e.SessionID] = e.EventID
			t.states[e.SessionID] = stateAwaitingInput
			return &Notification{
				SessionID: e.SessionID,
				Trigger:   TriggerInput,
				EventID:   e.EventID,
				Title:     t.spec.Name + ": input needed",
				Body:      "The agent is waiting for your input.",
			}
		}
	case eventTypeIn(e.Type, t.spec.IdleEvents):
		if st == stateBusy || st == stateAwaitingInput {
			t.states[e.SessionID] = stateIdle
			return &Notification{
				SessionID: e.SessionID,
				Trigger:   TriggerDone,
				EventID:   "",
				Title:     t.spec.Name + ": done",
				Body:      "The agent finished.",
			}
		}
	case eventTypeIn(e.Type, t.spec.ErrorEvents):
		if eventTypeIn(e.Type, t.spec.StickyErrorEvents) {
			t.states[e.SessionID] = stateError
		} else {
			t.states[e.SessionID] = stateIdle
		}
		return &Notification{
			SessionID: e.SessionID,
			Trigger:   TriggerError,
			EventID:   "",
			Title:     t.spec.Name + ": error",
			Body:      "The session reported an error.",
		}
	}
	return nil
}

func eventTypeIn(typ string, eventTypes []string) bool {
	return slices.Contains(eventTypes, typ)
}
