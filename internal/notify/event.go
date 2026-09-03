package notify

// Event is a parsed opencode SSE event.
type Event struct {
	Type string
	Data []byte
}

// opencode SSE event type names the tracker reacts to.
const (
	eventPartUpdated = "message.part.updated"
	eventSessionIdle = "session.idle"
)

// Tracker derives notification transitions from a stream of events.
type Tracker struct {
	state state
}

type state int

const (
	stateIdle state = iota
	stateBusy
	stateAwaitingInput
	stateError
)

// NewTracker returns a Tracker starting in the idle state.
func NewTracker() *Tracker { return &Tracker{state: stateIdle} }

// Handle feeds an event and returns a Notification when a configured
// transition fires, else nil.
func (t *Tracker) Handle(e Event) *Notification {
	switch e.Type {
	case eventPartUpdated, "session.updated":
		if t.state != stateError {
			t.state = stateBusy
		}
	case "permission.updated", "question.asked":
		if t.state == stateBusy || t.state == stateIdle {
			t.state = stateAwaitingInput
			return &Notification{
				Trigger: TriggerInput,
				Title:   "opencode: input needed",
				Body:    "The agent is waiting for your input.",
			}
		}
	case eventSessionIdle:
		if t.state == stateBusy || t.state == stateAwaitingInput {
			t.state = stateIdle
			return &Notification{Trigger: TriggerDone, Title: "opencode: done", Body: "The agent finished."}
		}
	case "session.error":
		t.state = stateError
		return &Notification{Trigger: TriggerError, Title: "opencode: error", Body: "The session reported an error."}
	}
	return nil
}

// HandleEventType is a convenience wrapper for tests.
func (t *Tracker) HandleEventType(typ string) *Notification {
	return t.Handle(Event{Type: typ, Data: nil})
}
