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

// Dedup wraps a Backend so a given transition notifies at most once across
// every running client of the same project. The first client to claim the token
// forwards the notification to inner; the rest drop it. Claims are released via
// SessionBusy when the session returns to work, so a later transition can
// notify again, and release removes the claim file so tokens do not accumulate.
//
// Input notifications also carry the source event's envelope id, which is part
// of the token. A turn can raise two requests back to back in one step, so each
// request needs its own token; sharing one token would make a later request
// depend on the first holder's stream staying connected.
type Dedup struct {
	slug    string
	claimer claimer
	inner   Backend
	mu      sync.Mutex
	// claims records, per session, the release func for each token this client
	// holds, keyed by the token itself.
	claims map[string]map[string]func()
}

// NewDedup builds a Backend that deduplicates notifications for slug across
// clients, forwarding claimed notifications to inner.
func NewDedup(slug string, c claimer, inner Backend) *Dedup {
	return &Dedup{
		slug:    slug,
		claimer: c,
		inner:   inner,
		mu:      sync.Mutex{},
		claims:  map[string]map[string]func(){},
	}
}

// claimKey derives the stable claim token for a notification. An input
// notification includes its envelope id, so distinct requests get distinct
// tokens (a turn can ask for two approvals in one step) while a re-delivery of
// the same event maps back to the same token.
func claimKey(n Notification) string {
	if n.Trigger == TriggerInput && n.EventID != "" {
		return fmt.Sprintf("%s-%d-%s", n.SessionID, int(n.Trigger), n.EventID)
	}
	return fmt.Sprintf("%s-%d", n.SessionID, int(n.Trigger))
}

// Notify claims the token for n and forwards it to inner only when this client
// wins the claim. Notifications without a session ID bypass dedup.
func (d *Dedup) Notify(n Notification) {
	if n.SessionID == "" {
		d.inner.Notify(n)
		return
	}
	key := claimKey(n)
	if d.held(n.SessionID, key) {
		return
	}
	release, claimed, err := d.claimer.Acquire(d.slug, key)
	if err != nil {
		// Fail open: deliver rather than risk dropping a real notification.
		d.inner.Notify(n)
		return
	}
	if !claimed {
		return
	}
	d.record(n.SessionID, key, release)
	d.inner.Notify(n)
}

// record stores the release func for a session's token.
func (d *Dedup) record(sessionID, key string, release func()) {
	d.mu.Lock()
	if d.claims[sessionID] == nil {
		d.claims[sessionID] = map[string]func(){}
	}
	d.claims[sessionID][key] = release
	d.mu.Unlock()
}

// held reports whether this client already owns the token for the session (a
// per-process shortcut over the authoritative flock).
func (d *Dedup) held(sessionID, key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.claims[sessionID][key]
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
