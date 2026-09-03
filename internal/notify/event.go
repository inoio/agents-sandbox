package notify

import (
	"slices"

	"github.com/inoio/opencode-sandbox/internal/agent"
)

// Event is a parsed SSE event.
type Event struct {
	Type string
	Data []byte
}

// Tracker derives notification transitions from a stream of events.
type Tracker struct {
	spec  agent.EventStreamSpec
	state state
}

type state int

const (
	stateIdle state = iota
	stateBusy
	stateAwaitingInput
	stateError
)

// NewTracker returns a Tracker starting in the idle state and driven by spec.
func NewTracker(spec agent.EventStreamSpec) *Tracker {
	return &Tracker{spec: spec, state: stateIdle}
}

// Handle feeds an event and returns a Notification when a configured
// transition fires, else nil.
func (t *Tracker) Handle(e Event) *Notification {
	switch {
	case eventTypeIn(e.Type, t.spec.BusyEvents):
		if t.state != stateError {
			t.state = stateBusy
		}
	case eventTypeIn(e.Type, t.spec.AwaitingInput):
		if t.state == stateBusy || t.state == stateIdle {
			t.state = stateAwaitingInput
			return &Notification{
				Trigger: TriggerInput,
				Title:   t.spec.Name + ": input needed",
				Body:    "The agent is waiting for your input.",
			}
		}
	case eventTypeIn(e.Type, t.spec.IdleEvents):
		if t.state == stateBusy || t.state == stateAwaitingInput {
			t.state = stateIdle
			return &Notification{Trigger: TriggerDone, Title: t.spec.Name + ": done", Body: "The agent finished."}
		}
	case eventTypeIn(e.Type, t.spec.ErrorEvents):
		t.state = stateError
		return &Notification{
			Trigger: TriggerError,
			Title:   t.spec.Name + ": error",
			Body:    "The session reported an error.",
		}
	}
	return nil
}

// HandleEventType is a convenience wrapper for tests.
func (t *Tracker) HandleEventType(typ string) *Notification {
	return t.Handle(Event{Type: typ, Data: nil})
}

func eventTypeIn(typ string, eventTypes []string) bool {
	return slices.Contains(eventTypes, typ)
}
