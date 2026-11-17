package notify

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// messagePrefix is the fixed product-name prefix on every notification title.
// The agent's own name is deliberately not part of the message, so v1 and v2
// read the same.
const messagePrefix = "agents-sandbox: "

// Tracker derives notification transitions from a stream of events, tracking
// each session independently plus the parent links, titles, and running
// background shells the subtree-aware done model needs.
type Tracker struct {
	spec     agent.EventStreamSpec
	states   map[string]state
	parentOf map[string]string
	titles   map[string]string
	// shells is, per session, the set of its running background shell ids.
	shells map[string]map[string]bool
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
		parentOf:  map[string]string{},
		titles:    map[string]string{},
		shells:    map[string]map[string]bool{},
		lastInput: map[string]string{},
	}
}

// Seed records the sessions from an agent's seed list: a non-empty parent ID
// marks the session as a child (the empty session id is skipped, so a malformed
// seed can never mark it a child), and a title names the session in later
// messages. Both are learned from the seed so a watcher that connected after
// creation still classifies and names its sessions.
func (t *Tracker) Seed(refs []agent.SessionRef) {
	for _, ref := range refs {
		if ref.ID == "" {
			continue
		}
		if ref.ParentID != "" {
			t.parentOf[ref.ID] = ref.ParentID
		}
		if ref.Title != "" {
			t.titles[ref.ID] = ref.Title
		}
	}
}

// ResetTransient clears the state that a fresh stream cannot replay: every
// child session's state and every running background shell. Top-level states
// persist, so a reconnect never loses a main session's busy/done transition;
// the cost is at most one plain done if a child was live at reconnect.
func (t *Tracker) ResetTransient() {
	for sessionID := range t.parentOf {
		delete(t.states, sessionID)
	}
	clear(t.shells)
}

// Handle feeds an event and returns a Notification when a configured
// transition fires for that event's session, else nil.
//
// Learnings (title, parent link, running shells) are recorded first so a
// lifecycle event that is also a busy event still marks its session busy. A
// child session never notifies for its own done/error/interrupt, but its input
// request surfaces as its top-level ancestor's input.
func (t *Tracker) Handle(e agent.StreamEvent) *Notification {
	t.observe(e)

	// An interrupt settles the session with no alert, for a child or a
	// top-level session: a child must settle or its parent's done never drains.
	if eventTypeIn(e.Type, t.spec.InterruptedEvents) {
		t.states[e.SessionID] = stateIdle
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
			return t.inputNotification(e)
		}
	case eventTypeIn(e.Type, t.spec.IdleEvents):
		if st == stateBusy || st == stateAwaitingInput {
			t.states[e.SessionID] = stateIdle
			if !t.isChild(e.SessionID) {
				return t.doneNotification(e.SessionID)
			}
		}
	case eventTypeIn(e.Type, t.spec.ErrorEvents):
		if eventTypeIn(e.Type, t.spec.StickyErrorEvents) {
			t.states[e.SessionID] = stateError
		} else {
			t.states[e.SessionID] = stateIdle
		}
		if !t.isChild(e.SessionID) {
			return &Notification{
				SessionID:   e.SessionID,
				Trigger:     TriggerError,
				EventID:     "",
				Title:       messagePrefix + "session error",
				Body:        t.body(e.SessionID, "The session reported an error."),
				DoneReasons: nil,
			}
		}
	}
	return nil
}

// observe records what an event teaches the tracker, independent of whether it
// notifies: a session title, a child's parent link, and a background shell's
// start or end.
func (t *Tracker) observe(e agent.StreamEvent) {
	if e.SessionID == "" {
		return
	}
	if e.Title != "" {
		t.titles[e.SessionID] = e.Title
	}
	if e.ParentID != "" && eventTypeIn(e.Type, t.spec.SessionEvents) {
		t.parentOf[e.SessionID] = e.ParentID
	}
	switch {
	case eventTypeIn(e.Type, t.spec.ShellStartedEvents):
		if e.ShellBackground && e.ShellID != "" {
			if t.shells[e.SessionID] == nil {
				t.shells[e.SessionID] = map[string]bool{}
			}
			t.shells[e.SessionID][e.ShellID] = true
		}
	case eventTypeIn(e.Type, t.spec.ShellEndedEvents):
		delete(t.shells[e.SessionID], e.ShellID)
	}
}

// inputNotification attributes an input request to the session's top-level
// ancestor: a request from a subagent is the whole tree blocking on the user.
func (t *Tracker) inputNotification(e agent.StreamEvent) *Notification {
	sessionID := e.SessionID
	if t.isChild(sessionID) {
		sessionID = t.topLevelAncestor(sessionID)
	}
	return &Notification{
		SessionID:   sessionID,
		Trigger:     TriggerInput,
		Title:       messagePrefix + "input requested",
		Body:        t.body(sessionID, "The agent is waiting for your input."),
		EventID:     e.EventID,
		DoneReasons: nil,
	}
}

// doneNotification classifies a top-level done by what is still pending in the
// session tree. Every top-level idle transition emits it — the tracker never
// holds or synthesizes; the backend gates the alert. Off SubtreeDoneStates
// (v1), it is always the plain done.
func (t *Tracker) doneNotification(sessionID string) *Notification {
	subagents, commands := 0, 0
	if t.spec.SubtreeDoneStates {
		subagents = t.liveDescendants(sessionID)
		commands = len(t.shells[sessionID])
	}

	var reasons []DoneReason
	var parts []string
	if subagents > 0 {
		reasons = append(reasons, DoneSubagentsRunning)
		parts = append(parts, countLabel(subagents, "subagent"))
	}
	if commands > 0 {
		reasons = append(reasons, DoneCommandsRunning)
		parts = append(parts, countLabel(commands, "command")+" running")
	}

	title := messagePrefix + "agent done"
	if len(parts) > 0 {
		title += " (" + strings.Join(parts, ", ") + ")"
	}
	return &Notification{
		SessionID:   sessionID,
		Trigger:     TriggerDone,
		Title:       title,
		Body:        t.body(sessionID, "The agent finished."),
		DoneReasons: reasons,
		EventID:     "",
	}
}

// body names the session when its title is known, else falls back to the
// trigger's original sentence. An unknown title never produces an empty
// session name.
func (t *Tracker) body(sessionID, fallback string) string {
	if title := t.titles[sessionID]; title != "" {
		return "in session '" + title + "'"
	}
	return fallback
}

// isChild reports whether the session is a subagent (has a recorded parent).
func (t *Tracker) isChild(sessionID string) bool {
	return t.parentOf[sessionID] != ""
}

// topLevelAncestor walks the parent chain to the session with no parent.
func (t *Tracker) topLevelAncestor(sessionID string) string {
	for parent := t.parentOf[sessionID]; parent != ""; parent = t.parentOf[sessionID] {
		sessionID = parent
	}
	return sessionID
}

// liveDescendants counts the descendant sessions that are still live (busy or
// awaiting input), so a done with one pending subagent can name it.
func (t *Tracker) liveDescendants(sessionID string) int {
	count := 0
	for candidate := range t.parentOf {
		if candidate == sessionID || !t.descendsFrom(candidate, sessionID) {
			continue
		}
		if st := t.states[candidate]; st == stateBusy || st == stateAwaitingInput {
			count++
		}
	}
	return count
}

// descendsFrom reports whether sessionID's parent chain reaches ancestor.
func (t *Tracker) descendsFrom(sessionID, ancestor string) bool {
	for parent := t.parentOf[sessionID]; parent != ""; parent = t.parentOf[parent] {
		if parent == ancestor {
			return true
		}
	}
	return false
}

// countLabel renders a count with a pluralized noun, e.g. "1 subagent",
// "2 subagents".
func countLabel(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func eventTypeIn(typ string, eventTypes []string) bool {
	return slices.Contains(eventTypes, typ)
}
