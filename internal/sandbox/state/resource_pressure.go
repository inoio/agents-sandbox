package state

import (
	"reflect"
	"time"
)

// ResourcePressureState is the per-project marker for a detected VM
// resource-limit hit. OOMSeenAt fingerprints the newest observed hit (the OOM
// victim's log timestamp, or the time a headroom trip was first seen);
// OfferedAt is set only when the user actually answers a raise offer, so a hit
// recorded by a non-interactive run survives to the next interactive launch.
type ResourcePressureState struct {
	Kind     string `yaml:"kind,omitempty"`
	Evidence string `yaml:"evidence,omitempty"`
	Victim   string `yaml:"victim,omitempty"`
	// OOMSeenAt is the timestamp of the newest observed hit.
	OOMSeenAt time.Time `yaml:"oom_seen_at,omitempty"`
	// OfferedAt is zero until the user answers an offer for this hit.
	OfferedAt time.Time `yaml:"offered_at,omitempty"`
}

// RecordResourcePressure records a detected hit. It is a no-op when no state
// file exists, so it never creates a fingerprint-less partial state.
func RecordResourcePressure(k Key, rp ResourcePressureState) error {
	return WithHomeState(k, func(hs *HomeState, exists bool) (bool, error) {
		if !exists {
			return false, nil
		}
		// gomutants:disable-next-line BRANCH_IF reason="rewriting an equal marker document is unobservable; the guard only skips a redundant write"
		if reflect.DeepEqual(hs.ResourcePressure, &rp) {
			// gomutants:disable-next-line RETURN_TRUE reason="returning write=true for an equal marker rewrites the identical document; unobservable"
			return false, nil
		}
		copied := rp
		hs.ResourcePressure = &copied
		return true, nil
	})
}

// MarkResourcePressureOffered records that the user answered a raise offer for the
// hit whose OOMSeenAt is seenAt, preserving the hit's kind and seen timestamp.
// It is a no-op when no hit is recorded, when the stored hit is a newer one
// (another session recorded it while the prompt was open), or when the hit was
// already answered at at.
func MarkResourcePressureOffered(k Key, seenAt, at time.Time) error {
	// A missing state file is a zero HomeState, so hs.ResourcePressure == nil
	// already covers it; a separate !exists operand would be redundant.
	return WithHomeState(k, func(hs *HomeState, _ bool) (bool, error) {
		if hs.ResourcePressure == nil ||
			!hs.ResourcePressure.OOMSeenAt.Equal(seenAt) ||
			// gomutants:disable-next-line EXPRESSION_REMOVE reason="when OfferedAt already equals at, setting it again writes the identical document; unobservable"
			hs.ResourcePressure.OfferedAt.Equal(at) {
			return false, nil
		}
		hs.ResourcePressure.OfferedAt = at
		return true, nil
	})
}
