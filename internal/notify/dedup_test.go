package notify

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

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

func TestDedupSameClientSecondNotifyDropped(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	sink := &sharedSink{}
	c := NewDedup("dedupheld-a", StateClaimer{}, sink)

	n := Notification{SessionID: "ses_A", Trigger: TriggerDone}
	c.Notify(n) // claims and delivers
	c.Notify(n) // held locally -> dropped
	if sink.count() != 1 {
		t.Fatalf("expected 1 notification after a repeat, got %d", sink.count())
	}
}

// TestDedupReleasesInputClaims pins bounded storage: each request gets its own
// token (so one client's dropped stream cannot silence another client's later
// request), and releasing the session's claims removes their files rather than
// leaving one per request behind.
func TestDedupReleasesInputClaims(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	slug := "dedupcleanup-a"
	sink := &sharedSink{}
	c := NewDedup(slug, StateClaimer{}, sink)

	c.Notify(Notification{SessionID: "ses_A", Trigger: TriggerInput, EventID: "evt_1"})
	c.Notify(Notification{SessionID: "ses_A", Trigger: TriggerInput, EventID: "evt_2"})
	if sink.count() != 2 {
		t.Fatalf("expected 2 distinct input notifications, got %d", sink.count())
	}
	if n := countClaimFiles(t, claimsDir(t, slug)); n != 2 {
		t.Fatalf("expected 2 claim files while held, got %d", n)
	}

	c.SessionBusy("ses_A")
	if n := countClaimFiles(t, claimsDir(t, slug)); n != 0 {
		t.Fatalf("expected the released claim files removed, got %d", n)
	}
}

// TestDedupLaterInputNotBlockedByAnotherClient pins liveness: a client holding
// one request's token must not block a second request when its own stream has
// dropped (so it never sees the second request). Each request has its own
// token, so the other client can still deliver it.
func TestDedupLaterInputNotBlockedByAnotherClient(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	slug := "dedupliveness-a"
	sink := &sharedSink{}
	c1 := NewDedup(slug, StateClaimer{}, sink)
	c2 := NewDedup(slug, StateClaimer{}, sink)

	c1.Notify(Notification{SessionID: "ses_A", Trigger: TriggerInput, EventID: "evt_1"})
	// c1's stream drops here and it never sees evt_2.
	c2.Notify(Notification{SessionID: "ses_A", Trigger: TriggerInput, EventID: "evt_2"})
	if sink.count() != 2 {
		t.Fatalf("expected both requests delivered, got %d", sink.count())
	}
}

// TestDedupSameInputEventDeliveredOnce pins dedup's own idempotence: the same
// input event delivered twice (the tracker normally suppresses it first) is
// forwarded once.
func TestDedupSameInputEventDeliveredOnce(t *testing.T) {
	sink := &sharedSink{}
	c := NewDedup("dedupinputrepeat-a", alwaysClaimer{}, sink)

	n := Notification{SessionID: "ses_A", Trigger: TriggerInput, EventID: "evt_1"}
	c.Notify(n)
	c.Notify(n)
	if sink.count() != 1 {
		t.Fatalf("expected the same input event delivered once, got %d", sink.count())
	}
}

// TestDedupCrossClientInputsDeliveredOnce guards cross-client behavior: each
// distinct request is claimed by one client and dropped by the other.
func TestDedupCrossClientInputsDeliveredOnce(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	slug := "dedupcross-input-a"
	sink := &sharedSink{}
	c1 := NewDedup(slug, StateClaimer{}, sink)
	c2 := NewDedup(slug, StateClaimer{}, sink)

	n1 := Notification{SessionID: "ses_A", Trigger: TriggerInput, EventID: "evt_1"}
	n2 := Notification{SessionID: "ses_A", Trigger: TriggerInput, EventID: "evt_2"}
	c1.Notify(n1)
	c2.Notify(n1)
	c1.Notify(n2)
	c2.Notify(n2)
	if sink.count() != 2 {
		t.Fatalf("expected each distinct request delivered once across clients, got %d", sink.count())
	}
}

func TestDedupFailsOpenOnClaimError(t *testing.T) {
	sink := &sharedSink{}
	c := NewDedup("deduperr-a", errorClaimer{}, sink)

	c.Notify(Notification{SessionID: "ses_A", Trigger: TriggerDone})
	if sink.count() != 1 {
		t.Fatalf("expected fail-open delivery on claim error, got %d", sink.count())
	}
}

// claimsDir returns the slug's claim-file directory under the mocked state root.
func claimsDir(t *testing.T, slug string) string {
	t.Helper()
	return filepath.Join(configpaths.Get().UserStateDir(), slug, "claims")
}

func countClaimFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	count := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".claim" {
			count++
		}
	}
	return count
}

// TestClaimKey pins the dedup token: an input request carries its envelope id,
// so two requests in one step get distinct tokens and a re-delivery the same;
// other triggers, and an input without an id, key by session and trigger only.
func TestClaimKey(t *testing.T) {
	cases := []struct {
		n    Notification
		want string
	}{
		{Notification{SessionID: "s", Trigger: TriggerInput, EventID: "e1"}, fmt.Sprintf("s-%d-e1", int(TriggerInput))},
		{Notification{SessionID: "s", Trigger: TriggerInput}, fmt.Sprintf("s-%d", int(TriggerInput))},
		{Notification{SessionID: "s", Trigger: TriggerDone, EventID: "e1"}, fmt.Sprintf("s-%d", int(TriggerDone))},
	}
	for _, tc := range cases {
		if got := claimKey(tc.n); got != tc.want {
			t.Errorf("claimKey(%+v) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
