package notify

import (
	"slices"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// Event is a parsed SSE event.
type Event struct {
	Type      string
	SessionID string
	Data      []byte
}

// Tracker derives notification transitions from a stream of events, tracking
// each session independently.
type Tracker struct {
	spec   agent.EventStreamSpec
	states map[string]state
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
	return &Tracker{spec: spec, states: map[string]state{}}
}

// Handle feeds an event and returns a Notification when a configured
// transition fires for that event's session, else nil.
func (t *Tracker) Handle(e Event) *Notification {
	st := t.states[e.SessionID]
	switch {
	case eventTypeIn(e.Type, t.spec.BusyEvents):
		if st != stateError {
			t.states[e.SessionID] = stateBusy
		}
	case eventTypeIn(e.Type, t.spec.AwaitingInput):
		if st == stateBusy || st == stateIdle {
			t.states[e.SessionID] = stateAwaitingInput
			return &Notification{
				SessionID: e.SessionID,
				Trigger:   TriggerInput,
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
				Title:     t.spec.Name + ": done",
				Body:      "The agent finished.",
			}
		}
	case eventTypeIn(e.Type, t.spec.ErrorEvents):
		t.states[e.SessionID] = stateError
		return &Notification{
			SessionID: e.SessionID,
			Trigger:   TriggerError,
			Title:     t.spec.Name + ": error",
			Body:      "The session reported an error.",
		}
	}
	return nil
}

// HandleEventType is a convenience wrapper for tests.
func (t *Tracker) HandleEventType(typ string) *Notification {
	return t.Handle(Event{Type: typ, SessionID: "", Data: nil})
}

func eventTypeIn(typ string, eventTypes []string) bool {
	return slices.Contains(eventTypes, typ)
}
