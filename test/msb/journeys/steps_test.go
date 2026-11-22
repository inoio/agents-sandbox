package journeys

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/network"
)

// TestWidenedBeyondAcceptsNarrowingToNone pins the terminal profile's semantics:
// the analyser narrows a broad profile to none, and that narrowing must pass
// whatever the baseline, because none is exclusive and can only shrink egress.
func TestWidenedBeyondAcceptsNarrowingToNone(t *testing.T) {
	after := network.Policy{Profiles: []network.Profile{network.ProfilePublic}}
	if widenedBeyond([]network.Profile{network.ProfileNone}, after) {
		t.Error("widenedBeyond(none, public) = true, want false: narrowing to none must pass")
	}
}

// TestWidenedBeyondFlagsAProfileTheBaselineDidNotAllow pins the guard's purpose:
// a learned scope naming a profile the run did not already have is flagged.
func TestWidenedBeyondFlagsAProfileTheBaselineDidNotAllow(t *testing.T) {
	cases := []struct {
		name     string
		profiles []network.Profile
		after    network.Policy
		want     bool
	}{
		{
			name:     "public over a none baseline",
			profiles: []network.Profile{network.ProfilePublic},
			after:    network.Policy{Profiles: []network.Profile{network.ProfileNone}},
			want:     true,
		},
		{
			name:     "private over a public baseline",
			profiles: []network.Profile{network.ProfilePrivate},
			after:    network.Policy{Profiles: []network.Profile{network.ProfilePublic}},
			want:     true,
		},
		{
			name:     "public over an unset baseline",
			profiles: []network.Profile{network.ProfilePublic},
			after:    network.Policy{},
			want:     true,
		},
		{
			name:     "a kept profile",
			profiles: []network.Profile{network.ProfilePublic},
			after:    network.Policy{Profiles: []network.Profile{network.ProfilePublic}},
			want:     false,
		},
		{
			name:     "a scope that wrote no profiles",
			profiles: nil,
			after:    network.Policy{Profiles: []network.Profile{network.ProfileNone}},
			want:     false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := widenedBeyond(tc.profiles, tc.after); got != tc.want {
				t.Errorf("widenedBeyond(%v, %v) = %t, want %t", tc.profiles, tc.after.Profiles, got, tc.want)
			}
		})
	}
}
