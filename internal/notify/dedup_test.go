package notify

import (
	"sync"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

// sharedSink records notifications from multiple Dedup instances backed by the
// same underlying sink.
type sharedSink struct {
	mu  sync.Mutex
	got []Notification
}

func (s *sharedSink) Notify(n Notification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, n)
}

func (s *sharedSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.got)
}

func TestDedupCrossClientSingleNotification(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	slug := "dedupcross-a"
	sink := &sharedSink{}

	c1 := NewDedup(slug, StateClaimer{}, sink)
	c2 := NewDedup(slug, StateClaimer{}, sink)

	n := Notification{SessionID: "ses_A", Trigger: TriggerDone, Title: "opencode: done", Body: "x"}
	c1.Notify(n) // first claim -> delivered
	c2.Notify(n) // already claimed -> dropped
	if sink.count() != 1 {
		t.Fatalf("expected 1 notification across two clients, got %d", sink.count())
	}

	// Session returns to work, releasing the claim.
	c1.SessionBusy("ses_A")
	c2.Notify(n) // now a fresh transition can claim again
	if sink.count() != 2 {
		t.Fatalf("expected 2nd notification after SessionBusy, got %d", sink.count())
	}
}

func TestDedupDifferentSessionsIndependent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	slug := "dedupses-a"
	sink := &sharedSink{}
	c := NewDedup(slug, StateClaimer{}, sink)

	c.Notify(Notification{SessionID: "ses_A", Trigger: TriggerDone})
	c.Notify(Notification{SessionID: "ses_B", Trigger: TriggerDone})
	if sink.count() != 2 {
		t.Fatalf("expected 2 notifications for 2 sessions, got %d", sink.count())
	}
}

func TestDedupDifferentTriggersIndependent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	slug := "deduptrig-a"
	sink := &sharedSink{}
	c := NewDedup(slug, StateClaimer{}, sink)

	c.Notify(Notification{SessionID: "ses_A", Trigger: TriggerDone})
	c.Notify(Notification{SessionID: "ses_A", Trigger: TriggerInput})
	if sink.count() != 2 {
		t.Fatalf("expected 2 notifications for 2 triggers, got %d", sink.count())
	}
}

func TestDedupBypassesWhenNoSessionID(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	slug := "dedupnosid-a"
	sink := &sharedSink{}
	c := NewDedup(slug, StateClaimer{}, sink)

	c.Notify(Notification{Trigger: TriggerDone})
	c.Notify(Notification{Trigger: TriggerDone})
	if sink.count() != 2 {
		t.Fatalf("expected 2 notifications without session ID, got %d", sink.count())
	}
}

// sseBlockFor builds an SSE block carrying a sessionID, so dedup is exercised.
//
//nolint:unparam // sessionID is a parameter for readability though tests pass one value
func sseBlockFor(typ, sessionID string) []byte {
	return []byte(
		"data: {\"directory\":\"/workspace\",\"project\":\"p\",\"payload\":{\"id\":\"evt\",\"type\":\"" + typ + "\",\"properties\":{\"sessionID\":\"" + sessionID + "\"}}}\n\n",
	)
}
