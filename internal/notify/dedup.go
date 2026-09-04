package notify

import (
	"fmt"
	"sync"

	sandboxstate "github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// claimer acquires named cross-process claims. It is an interface so tests can
// substitute a fake; production uses StateClaimer.
type claimer interface {
	Acquire(slug, key string) (release func(), claimed bool, err error)
}

// StateClaimer delegates claim acquisition to the shared sandbox state dir.
type StateClaimer struct{}

// Acquire claims a named token under the slug's state dir, returning a release
// func the holder calls when the claim should be freed.
func (StateClaimer) Acquire(slug, key string) (func(), bool, error) {
	return sandboxstate.AcquireClaim(slug, key)
}

// Dedup wraps a Backend so a given (session, trigger) transition notifies at
// most once across every running client of the same project. The first client
// to claim the token forwards the notification to inner; the rest drop it.
// Claims are released via SessionBusy when the session returns to work, so a
// later transition for it can notify again.
type Dedup struct {
	slug    string
	claimer claimer
	inner   Backend
	mu      sync.Mutex
	claims  map[string]map[Trigger]func()
}

// NewDedup builds a Backend that deduplicates notifications for slug across
// clients, forwarding claimed notifications to inner.
func NewDedup(slug string, c claimer, inner Backend) *Dedup {
	return &Dedup{
		slug:    slug,
		claimer: c,
		inner:   inner,
		mu:      sync.Mutex{},
		claims:  map[string]map[Trigger]func(){},
	}
}

// claimKey derives the stable claim token for a session and trigger.
func claimKey(sessionID string, t Trigger) string {
	return fmt.Sprintf("%s-%d", sessionID, int(t))
}

// Notify claims the token for n and forwards it to inner only when this client
// wins the claim. Notifications without a session ID bypass dedup.
func (d *Dedup) Notify(n Notification) {
	if n.SessionID == "" {
		d.inner.Notify(n)
		return
	}
	if d.held(n.SessionID, n.Trigger) {
		return
	}
	key := claimKey(n.SessionID, n.Trigger)
	release, claimed, err := d.claimer.Acquire(d.slug, key)
	if err != nil {
		// Fail open: deliver rather than risk dropping a real notification.
		d.inner.Notify(n)
		return
	}
	if !claimed {
		return
	}
	d.mu.Lock()
	if d.claims[n.SessionID] == nil {
		d.claims[n.SessionID] = map[Trigger]func(){}
	}
	d.claims[n.SessionID][n.Trigger] = release
	d.mu.Unlock()
	d.inner.Notify(n)
}

// held reports whether this client already owns the claim for the session and
// trigger (a per-process shortcut over the authoritative flock).
func (d *Dedup) held(sessionID string, t Trigger) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	perSession := d.claims[sessionID]
	if perSession == nil {
		return false
	}
	_, ok := perSession[t]
	return ok
}

// SessionBusy releases every claim this client holds for the session, so a
// later transition for it can notify again. It is called by Watch when the
// session returns to work.
func (d *Dedup) SessionBusy(sessionID string) {
	d.mu.Lock()
	releases := d.claims[sessionID]
	delete(d.claims, sessionID)
	d.mu.Unlock()
	for _, release := range releases {
		release()
	}
}
