package state

import "time"

// DiskPressureState is the per-project marker for one disk limit's detected
// hit, keyed by kind in HomeState.DiskPressure. It keeps the probed size and
// free space so the next launch can offer the raise without probing again.
// OfferedAt is set only when the user answers an offer for this hit.
type DiskPressureState struct {
	// SeenAt is when the hit was first observed.
	SeenAt time.Time `yaml:"seen_at,omitempty"`
	// SizeKiB and AvailKiB are the mount's size and free space at the hit.
	SizeKiB  uint64 `yaml:"size_kib,omitempty"`
	AvailKiB uint64 `yaml:"avail_kib,omitempty"`
	// OfferedAt is zero until the user answers an offer for this hit.
	OfferedAt time.Time `yaml:"offered_at,omitempty"`
}

// RecordDiskPressure records kind's hit unless kind already has a marker, and
// returns the marker now stored: the caller's hit, or the one another session
// recorded first. Without a state file it records nothing and returns the
// caller's hit, so it never creates a fingerprint-less partial state.
func RecordDiskPressure(k Key, kind string, ds DiskPressureState) (DiskPressureState, error) {
	stored := ds
	err := WithHomeState(k, func(hs *HomeState, exists bool) (bool, error) {
		if !exists {
			return false, nil
		}
		if existing, ok := hs.DiskPressure[kind]; ok {
			stored = existing
			// gomutants:disable-next-line RETURN_TRUE reason="rewriting the unchanged state is invisible"
			return false, nil
		}
		if hs.DiskPressure == nil {
			hs.DiskPressure = map[string]DiskPressureState{}
		}
		hs.DiskPressure[kind] = ds
		return true, nil
	})
	return stored, err
}

// MarkDiskPressureOffered records that the user answered the offer for kind's
// hit first seen at seenAt. It is a no-op when kind has no marker or its marker
// is a newer hit another session recorded while the prompt was open.
func MarkDiskPressureOffered(k Key, kind string, seenAt, at time.Time) error {
	return WithHomeState(k, func(hs *HomeState, _ bool) (bool, error) {
		marker, ok := hs.DiskPressure[kind]
		if !ok || !marker.SeenAt.Equal(seenAt) {
			return false, nil
		}
		marker.OfferedAt = at
		hs.DiskPressure[kind] = marker
		return true, nil
	})
}

// ClearDiskPressure drops kind's marker for the hit first seen at seenAt once
// its mount has recovered, so a later trip is offered again. A newer hit
// another session recorded meanwhile stays.
func ClearDiskPressure(k Key, kind string, seenAt time.Time) error {
	return WithHomeState(k, func(hs *HomeState, _ bool) (bool, error) {
		marker, ok := hs.DiskPressure[kind]
		if !ok || !marker.SeenAt.Equal(seenAt) {
			return false, nil
		}
		delete(hs.DiskPressure, kind)
		return true, nil
	})
}
