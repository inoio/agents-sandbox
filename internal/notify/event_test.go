package notify

import (
	"slices"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// reason-events are the opencode2 event kinds the subtree-aware done tests
// drive directly, named so a test reads as the flow it models.
const (
	execStarted     = "session.execution.started"
	execSucceeded   = "session.execution.succeeded"
	execInterrupted = "session.execution.interrupted"
	sessionCreated  = "session.created"
	permissionAsked = "permission.asked"
	shellStarted    = "session.shell.started"
	shellEnded      = "session.shell.ended"
)

func TestTrackerAwaitingInputFromIdle(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	n := tr.Handle(agent.StreamEvent{Type: "question.asked"})
	if n == nil || n.Trigger != TriggerInput {
		t.Fatalf("expected input notification from idle, got %+v", n)
	}
}

func TestTrackerAwaitingInputIsIdempotent(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	tr.Handle(agent.StreamEvent{Type: "message.part.updated"}) // -> busy
	tr.Handle(agent.StreamEvent{Type: "question.asked"})       // -> awaiting input, fires
	if n := tr.Handle(agent.StreamEvent{Type: "question.asked"}); n != nil {
		t.Fatalf("second awaiting-input should be deduped, got %+v", n)
	}
}

func TestTrackerStuckOnError(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	tr.Handle(agent.StreamEvent{Type: "session.error"}) // -> error
	if n := tr.Handle(agent.StreamEvent{Type: "message.part.updated"}); n != nil {
		t.Fatalf("busy event after error should not notify, got %+v", n)
	}
	if tr.states[""] != stateError {
		t.Fatalf("state should stay error, got %v", tr.states[""])
	}
}

func TestTrackerDoneFromAwaitingInput(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	tr.Handle(agent.StreamEvent{Type: "question.asked"}) // idle -> awaiting input
	n := tr.Handle(agent.StreamEvent{Type: "session.idle"})
	if n == nil || n.Trigger != TriggerDone {
		t.Fatalf("expected done notification from awaiting input, got %+v", n)
	}
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

// TestTrackerMessageWording pins the fixed message text: a product-name title
// prefix (the agent name is dropped), and a body naming the session when its
// title is known, falling back to the original sentence otherwise.
func TestTrackerMessageWording(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	tr.Handle(agent.StreamEvent{Type: "message.part.updated"}) // busy
	n := tr.Handle(agent.StreamEvent{Type: "session.idle"})
	if n == nil || n.Title != "agents-sandbox: agent done" || n.Body != "The agent finished." {
		t.Fatalf("done = %+v, want the fixed title and fallback body", n)
	}
	tr.Handle(agent.StreamEvent{Type: "message.part.updated"}) // busy
	n = tr.Handle(agent.StreamEvent{Type: "question.asked"})
	if n == nil || n.Title != "agents-sandbox: input requested" ||
		n.Body != "The agent is waiting for your input." {
		t.Fatalf("input = %+v, want the fixed title and fallback body", n)
	}
	tr.Handle(agent.StreamEvent{Type: "message.part.updated"}) // busy
	n = tr.Handle(agent.StreamEvent{Type: "session.error"})
	if n == nil || n.Title != "agents-sandbox: session error" ||
		n.Body != "The session reported an error." {
		t.Fatalf("error = %+v, want the fixed title and fallback body", n)
	}
}

// TestTrackerMessageNamesKnownSession pins the body naming a known title: the
// tracker learns it from a lifecycle event's Title (or the seed) and puts it in
// the body.
func TestTrackerMessageNamesKnownSession(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	tr.Handle(agent.StreamEvent{Type: "session.created", SessionID: "ses_1", Title: "my run"})
	tr.Handle(agent.StreamEvent{Type: "message.part.updated", SessionID: "ses_1"})
	n := tr.Handle(agent.StreamEvent{Type: "session.idle", SessionID: "ses_1"})
	if n == nil || n.Body != "in session 'my run'" {
		t.Fatalf("done = %+v, want the session title in the body", n)
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
	tr.Seed([]agent.SessionRef{{ID: "ses_seeded", ParentID: "ses_root"}, {ID: ""}})

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
	if n == nil || n.Trigger != TriggerDone || n.Title != "agents-sandbox: agent done" {
		t.Fatalf("expected 'agents-sandbox: agent done' done notification, got %+v", n)
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
			name: "turn-scoped error leaves the session idle",
			spec: turnScoped,
			steps: []step{
				{event: "session.execution.started"},
				{event: "session.execution.failed", want: "error"},
				{event: "session.execution.succeeded"},
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

// TestTrackerLearnsChildrenOnlyFromSessionEvents pins that a parent id on an
// event outside SessionEvents does not mark the session as a child.
func TestTrackerLearnsChildrenOnlyFromSessionEvents(t *testing.T) {
	spec := opencodeSpec()
	spec.SessionEvents = []string{"session.created"}
	tr := NewTracker(spec)

	tr.Handle(agent.StreamEvent{Type: "message.part.updated", SessionID: "ses_1", ParentID: "ses_root"})
	if n := tr.Handle(
		agent.StreamEvent{Type: "session.idle", SessionID: "ses_1"},
	); n == nil ||
		n.Trigger != TriggerDone {
		t.Fatalf("idle = %+v, want done for a session that is not a child", n)
	}
}

// TestTrackerAwaitingInputNeedsANewEnvelopeID pins that, while a session
// already awaits input, only a request with a new, non-empty envelope id
// notifies again.
func TestTrackerAwaitingInputNeedsANewEnvelopeID(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	tr.Handle(agent.StreamEvent{Type: "message.part.updated", SessionID: "ses_1"})
	if n := tr.Handle(agent.StreamEvent{Type: "permission.asked", SessionID: "ses_1", EventID: "e1"}); n == nil {
		t.Fatal("first request did not notify")
	}
	if n := tr.Handle(agent.StreamEvent{Type: "permission.asked", SessionID: "ses_1"}); n != nil {
		t.Fatalf("request without an envelope id notified again: %+v", n)
	}
}

// TestTrackerLatchedErrorMutesInputRequests pins that a session latched by a
// sticky error does not notify for a later input request, even a new one.
func TestTrackerLatchedErrorMutesInputRequests(t *testing.T) {
	tr := NewTracker(opencodeSpec())
	tr.Handle(agent.StreamEvent{Type: "session.error", SessionID: "ses_1"})
	if n := tr.Handle(agent.StreamEvent{Type: "permission.asked", SessionID: "ses_1", EventID: "e1"}); n != nil {
		t.Fatalf("input request after a sticky error notified: %+v", n)
	}
}

// TestTrackerIgnoresParentOnSessionlessEvents pins that a session event
// without a session id marks nothing as a child, so events that carry no
// session still notify.
func TestTrackerIgnoresParentOnSessionlessEvents(t *testing.T) {
	spec := opencodeSpec()
	spec.SessionEvents = []string{"session.created"}
	tr := NewTracker(spec)

	tr.Handle(agent.StreamEvent{Type: "session.created", ParentID: "ses_root"})
	if n := tr.Handle(agent.StreamEvent{Type: "session.error"}); n == nil || n.Trigger != TriggerError {
		t.Fatalf("session-less error = %+v, want an error notification", n)
	}
}

// TestDoneReasonsSubagents pins the no-hold model: a top-level succeeded while
// a subagent is still live emits a done (it is never held or synthesized)
// carrying the DoneSubagentsRunning reason; once the child settles, the next
// top-level succeeded is a plain done.
func TestDoneReasonsSubagents(t *testing.T) {
	tr := NewTracker(opencode2Spec())
	const root = "ses_root"
	tr.Handle(agent.StreamEvent{Type: execStarted, SessionID: root})
	tr.Handle(agent.StreamEvent{Type: sessionCreated, SessionID: "ses_child", ParentID: root})
	tr.Handle(agent.StreamEvent{Type: execStarted, SessionID: "ses_child"})

	n := tr.Handle(agent.StreamEvent{Type: execSucceeded, SessionID: root})
	if n == nil || n.Trigger != TriggerDone {
		t.Fatalf("root succeeded with a live child = %+v, want a done", n)
	}
	if !slices.Equal(n.DoneReasons, []DoneReason{DoneSubagentsRunning}) {
		t.Fatalf("DoneReasons = %v, want [subagents]", n.DoneReasons)
	}
	if n.Title != "agents-sandbox: agent done (1 subagent)" {
		t.Fatalf("Title = %q, want the subagent reason appended", n.Title)
	}

	tr.Handle(agent.StreamEvent{Type: execSucceeded, SessionID: "ses_child"})
	tr.Handle(agent.StreamEvent{Type: execStarted, SessionID: root})
	n = tr.Handle(agent.StreamEvent{Type: execSucceeded, SessionID: root})
	if n == nil || n.Trigger != TriggerDone || len(n.DoneReasons) != 0 ||
		n.Title != "agents-sandbox: agent done" {
		t.Fatalf("root succeeded after the child settled = %+v, want a plain done", n)
	}
}

// TestDoneReasonsCommands pins that a background shell running for the
// top-level session classifies its done as done-with-a-command-running; once
// the shell ends the reason is gone.
func TestDoneReasonsCommands(t *testing.T) {
	tr := NewTracker(opencode2Spec())
	const root = "ses_root"
	tr.Handle(agent.StreamEvent{Type: execStarted, SessionID: root})
	tr.Handle(agent.StreamEvent{Type: shellStarted, SessionID: root, ShellID: "sh_1", ShellBackground: true})

	n := tr.Handle(agent.StreamEvent{Type: execSucceeded, SessionID: root})
	if !slices.Equal(n.DoneReasons, []DoneReason{DoneCommandsRunning}) {
		t.Fatalf("DoneReasons = %v, want [commands]", n.DoneReasons)
	}
	if n.Title != "agents-sandbox: agent done (1 command running)" {
		t.Fatalf("Title = %q, want the command reason appended", n.Title)
	}

	tr.Handle(agent.StreamEvent{Type: shellEnded, SessionID: root, ShellID: "sh_1"})
	tr.Handle(agent.StreamEvent{Type: execStarted, SessionID: root})
	n = tr.Handle(agent.StreamEvent{Type: execSucceeded, SessionID: root})
	if len(n.DoneReasons) != 0 || n.Title != "agents-sandbox: agent done" {
		t.Fatalf("done after the shell ended = %+v, want a plain done", n)
	}
}

// TestDoneReasonsBoth pins the combined message: a live subagent and a running
// command are one done naming both, subagents first.
func TestDoneReasonsBoth(t *testing.T) {
	tr := NewTracker(opencode2Spec())
	const root = "ses_root"
	tr.Handle(agent.StreamEvent{Type: execStarted, SessionID: root})
	tr.Handle(agent.StreamEvent{Type: sessionCreated, SessionID: "ses_child_a", ParentID: root})
	tr.Handle(agent.StreamEvent{Type: execStarted, SessionID: "ses_child_a"})
	tr.Handle(agent.StreamEvent{Type: sessionCreated, SessionID: "ses_child_b", ParentID: root})
	tr.Handle(agent.StreamEvent{Type: execStarted, SessionID: "ses_child_b"})
	tr.Handle(agent.StreamEvent{Type: shellStarted, SessionID: root, ShellID: "sh_1", ShellBackground: true})

	n := tr.Handle(agent.StreamEvent{Type: execSucceeded, SessionID: root})
	if !slices.Equal(n.DoneReasons, []DoneReason{DoneSubagentsRunning, DoneCommandsRunning}) {
		t.Fatalf("DoneReasons = %v, want [subagents commands]", n.DoneReasons)
	}
	if n.Title != "agents-sandbox: agent done (2 subagents, 1 command running)" {
		t.Fatalf("Title = %q, want both reasons, subagents first", n.Title)
	}
}

// TestInterruptedSettlesChild pins that an interrupted child settles with no
// alert, so the parent's next succeeded is a plain done rather than one still
// naming a live subagent.
func TestInterruptedSettlesChild(t *testing.T) {
	tr := NewTracker(opencode2Spec())
	const root = "ses_root"
	tr.Handle(agent.StreamEvent{Type: execStarted, SessionID: root})
	tr.Handle(agent.StreamEvent{Type: sessionCreated, SessionID: "ses_child", ParentID: root})
	tr.Handle(agent.StreamEvent{Type: execStarted, SessionID: "ses_child"})

	if n := tr.Handle(agent.StreamEvent{Type: execInterrupted, SessionID: "ses_child"}); n != nil {
		t.Fatalf("interrupted child notified: %+v", n)
	}
	n := tr.Handle(agent.StreamEvent{Type: execSucceeded, SessionID: root})
	if n == nil || n.Trigger != TriggerDone || len(n.DoneReasons) != 0 {
		t.Fatalf("root succeeded after the child was interrupted = %+v, want a plain done", n)
	}
}

// TestChildInputSurfacesAsTopLevel pins the one child notification: a child's
// input request is attributed to the top-level ancestor, carrying the child's
// request id and the ancestor's title.
func TestChildInputSurfacesAsTopLevel(t *testing.T) {
	tr := NewTracker(opencode2Spec())
	const root = "ses_root"
	tr.Handle(agent.StreamEvent{Type: sessionCreated, SessionID: root, Title: "my task"})
	tr.Handle(agent.StreamEvent{Type: sessionCreated, SessionID: "ses_child", ParentID: root})

	n := tr.Handle(agent.StreamEvent{Type: permissionAsked, SessionID: "ses_child", EventID: "evt_child"})
	if n == nil || n.Trigger != TriggerInput {
		t.Fatalf("child input = %+v, want an input notification", n)
	}
	if n.SessionID != root || n.EventID != "evt_child" {
		t.Fatalf("attribution = session %q event %q, want the root and the child's request id",
			n.SessionID, n.EventID)
	}
	if n.Title != "agents-sandbox: input requested" || n.Body != "in session 'my task'" {
		t.Fatalf("message = %q / %q, want the fixed title and the root's title", n.Title, n.Body)
	}
}

// TestNestedChildInputUsesRoot pins that input from a grandchild walks the
// parent chain to the top-level session.
func TestNestedChildInputUsesRoot(t *testing.T) {
	tr := NewTracker(opencode2Spec())
	const root = "ses_root"
	tr.Handle(agent.StreamEvent{Type: sessionCreated, SessionID: root, Title: "root task"})
	tr.Handle(agent.StreamEvent{Type: sessionCreated, SessionID: "ses_child", ParentID: root})
	tr.Handle(agent.StreamEvent{Type: sessionCreated, SessionID: "ses_grandchild", ParentID: "ses_child"})

	n := tr.Handle(agent.StreamEvent{Type: permissionAsked, SessionID: "ses_grandchild", EventID: "evt_grand"})
	if n == nil || n.SessionID != root || n.Body != "in session 'root task'" {
		t.Fatalf("grandchild input = %+v, want attribution to the root", n)
	}
}

// TestDoneWithoutSubtreeStates pins v1's frozen trigger semantics: with
// SubtreeDoneStates off, a top-level idle transition notifies done regardless
// of live descendants, with no reasons.
func TestDoneWithoutSubtreeStates(t *testing.T) {
	spec := opencodeSpec()
	spec.SessionEvents = []string{"session.created"}
	tr := NewTracker(spec)
	const root = "ses_root"
	tr.Handle(agent.StreamEvent{Type: "message.part.updated", SessionID: root})
	tr.Handle(agent.StreamEvent{Type: "session.created", SessionID: "ses_child", ParentID: root})
	tr.Handle(agent.StreamEvent{Type: "message.part.updated", SessionID: "ses_child"})

	n := tr.Handle(agent.StreamEvent{Type: "session.idle", SessionID: root})
	if n == nil || n.Trigger != TriggerDone || len(n.DoneReasons) != 0 ||
		n.Title != "agents-sandbox: agent done" {
		t.Fatalf("v1 done with a live child = %+v, want today's plain done", n)
	}
}

// TestResetTransientClearsChildStateAndShells pins what a fresh stream cannot
// replay: a child's live state and every running background shell are dropped,
// while the top-level session's state, the parent links, and the titles survive
// (they come from the seed or the stream, not from liveness).
func TestResetTransientClearsChildStateAndShells(t *testing.T) {
	tr := NewTracker(opencode2Spec())
	const root = "ses_root"
	tr.Handle(agent.StreamEvent{Type: sessionCreated, SessionID: root, Title: "root task"})
	tr.Handle(agent.StreamEvent{Type: sessionCreated, SessionID: "ses_child", ParentID: root})
	tr.Handle(agent.StreamEvent{Type: execStarted, SessionID: root})
	tr.Handle(agent.StreamEvent{Type: execStarted, SessionID: "ses_child"})
	tr.Handle(agent.StreamEvent{Type: shellStarted, SessionID: root, ShellID: "sh_1", ShellBackground: true})

	tr.ResetTransient()

	if tr.states["ses_child"] != stateIdle {
		t.Errorf("child state after reset = %v, want idle", tr.states["ses_child"])
	}
	if len(tr.shells[root]) != 0 {
		t.Errorf("shells after reset = %v, want none", tr.shells[root])
	}
	if tr.states[root] != stateBusy {
		t.Errorf("top-level state after reset = %v, want it to persist as busy", tr.states[root])
	}
	if tr.parentOf["ses_child"] != root || tr.titles[root] != "root task" {
		t.Errorf("parent links/titles lost: parentOf=%q titles=%q", tr.parentOf["ses_child"], tr.titles[root])
	}

	// The reset must also remove the child's liveness from the done
	// classification: the next top-level succeeded is a plain done.
	n := tr.Handle(agent.StreamEvent{Type: execSucceeded, SessionID: root})
	if n == nil || n.Trigger != TriggerDone || len(n.DoneReasons) != 0 {
		t.Fatalf("done after reset = %+v, want a plain done", n)
	}
}

// TestSeedRecordsTitlesAndParents pins that the seed teaches both the parent
// link (so a later-live seeded child classifies the done) and the title (so the
// body names the session).
func TestSeedRecordsTitlesAndParents(t *testing.T) {
	tr := NewTracker(opencode2Spec())
	tr.Seed([]agent.SessionRef{
		{ID: "ses_root", Title: "seeded task"},
		{ID: "ses_child", ParentID: "ses_root", Title: "kid"},
		{ID: "", ParentID: "ses_root"},
	})
	tr.Handle(agent.StreamEvent{Type: execStarted, SessionID: "ses_root"})
	tr.Handle(agent.StreamEvent{Type: execStarted, SessionID: "ses_child"})

	n := tr.Handle(agent.StreamEvent{Type: execSucceeded, SessionID: "ses_root"})
	if !slices.Equal(n.DoneReasons, []DoneReason{DoneSubagentsRunning}) ||
		n.Title != "agents-sandbox: agent done (1 subagent)" {
		t.Fatalf("seeded child not classified = %+v, want the subagent reason", n)
	}
	if n.Body != "in session 'seeded task'" {
		t.Fatalf("Body = %q, want the seeded title", n.Body)
	}
}
