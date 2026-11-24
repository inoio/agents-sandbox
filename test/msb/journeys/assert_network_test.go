package journeys

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// TestWidenedBeyondAcceptsNarrowingToNone pins the terminal profile's semantics:
// the analyser narrows a broad profile to none, and that narrowing must pass
// whatever the baseline, because none is exclusive and can only shrink egress.
func TestWidenedBeyondAcceptsNarrowingToNone(t *testing.T) {
	after := network.Policy{Profiles: []network.Profile{network.ProfilePublic}}
	if widenedBeyond(networkPosture{profiles: []network.Profile{network.ProfileNone}}, after, nil) {
		t.Error("widenedBeyond(none, public) = true, want false: narrowing to none must pass")
	}
}

// TestWidenedBeyondFlagsAProfileTheBaselineDidNotAllow pins the guard's purpose:
// a learned scope naming a profile or egress-allow host the run did not already
// have is flagged, while the baseline's own allow entries and the journey's
// allowed destinations pass.
func TestWidenedBeyondFlagsAProfileTheBaselineDidNotAllow(t *testing.T) {
	cases := []struct {
		name          string
		learned       networkPosture
		after         network.Policy
		allowedEgress []string
		want          bool
	}{
		{
			name:    "public over a none baseline",
			learned: networkPosture{profiles: []network.Profile{network.ProfilePublic}},
			after:   network.Policy{Profiles: []network.Profile{network.ProfileNone}},
			want:    true,
		},
		{
			name:    "private over a public baseline",
			learned: networkPosture{profiles: []network.Profile{network.ProfilePrivate}},
			after:   network.Policy{Profiles: []network.Profile{network.ProfilePublic}},
			want:    true,
		},
		{
			name:    "public over an unset baseline",
			learned: networkPosture{profiles: []network.Profile{network.ProfilePublic}},
			after:   network.Policy{},
			want:    true,
		},
		{
			name:    "a kept profile",
			learned: networkPosture{profiles: []network.Profile{network.ProfilePublic}},
			after:   network.Policy{Profiles: []network.Profile{network.ProfilePublic}},
			want:    false,
		},
		{
			name:    "a scope that wrote no profiles",
			learned: networkPosture{},
			after:   network.Policy{Profiles: []network.Profile{network.ProfileNone}},
			want:    false,
		},
		{
			name: "an egress-allow host over a none baseline",
			learned: networkPosture{
				profiles:    []network.Profile{network.ProfileNone},
				egressAllow: []string{"widened.example"},
			},
			after: network.Policy{Profiles: []network.Profile{network.ProfileNone}},
			want:  true,
		},
		{
			name: "the journey's own destination is allowed",
			learned: networkPosture{
				profiles:    []network.Profile{network.ProfileNone},
				egressAllow: []string{"stub.example"},
			},
			after:         network.Policy{Profiles: []network.Profile{network.ProfileNone}},
			allowedEgress: []string{"stub.example"},
			want:          false,
		},
		{
			name:    "a baseline allow entry is kept",
			learned: networkPosture{egressAllow: []string{"kept.example"}},
			after:   network.Policy{EgressAllow: []string{"kept.example"}},
			want:    false,
		},
		{
			name:    "an egress-allow host beyond the baseline's allow set",
			learned: networkPosture{egressAllow: []string{"kept.example", "extra.example"}},
			after:   network.Policy{EgressAllow: []string{"kept.example"}},
			want:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := widenedBeyond(tc.learned, tc.after, tc.allowedEgress); got != tc.want {
				t.Errorf(
					"widenedBeyond(%+v, %v, %v) = %t, want %t",
					tc.learned,
					tc.after,
					tc.allowedEgress,
					got,
					tc.want,
				)
			}
		})
	}
}

// TestRunNetworkPolicyDerivesTheRunsProfile pins the network-not-widened
// baseline: the policy is the profile the journey's spec passes to `--network`,
// so a learned scope is compared against the posture the run actually had rather
// than a literal built before the Context exists. opencode2's spec names no
// profile, so the baseline is the secure default none.
func TestRunNetworkPolicyDerivesTheRunsProfile(t *testing.T) {
	cases := []struct {
		name string
		spec driver.Spec
		want []network.Profile
	}{
		{name: "an unset spec defaults to none", spec: driver.Spec{}, want: []network.Profile{network.ProfileNone}},
		{
			name: "the spec's own profile wins",
			spec: driver.Spec{Network: driver.PublicNetwork},
			want: []network.Profile{network.ProfilePublic},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runNetworkPolicy(&Context{Spec: tc.spec})
			if !slices.Equal(got.Profiles, tc.want) {
				t.Errorf("runNetworkPolicy(%+v).Profiles = %v, want %v", tc.spec, got.Profiles, tc.want)
			}
		})
	}
}

// TestStepAssertNetworkNotWidened pins the changed step's context-derived
// baseline: it reads the scope's launcher config and compares the network
// posture it names — the profiles and the egress-allow list — against the
// builder's policy plus the destinations the journey drove, so a scope that kept
// or narrowed the posture passes, one that wrote nothing passes, and one that
// widened fails. An egress-allow entry the journey did not drive (or the
// baseline did not already allow) is a widening, because under none the allow
// list is the egress.
func TestStepAssertNetworkNotWidened(t *testing.T) {
	const agentName = "opencode2"
	newContext := func(t *testing.T) *Context {
		t.Helper()
		root := t.TempDir()
		if err := os.MkdirAll(driver.ProjectDir(root, agentName), 0o750); err != nil {
			t.Fatalf("create project dir: %v", err)
		}
		return &Context{TB: t, Root: root, Agent: agentName, Spec: driver.Spec{}, Home: t.TempDir()}
	}
	writeScope := func(t *testing.T, c *Context, body string) {
		t.Helper()
		dir, err := journeyScopeDir(c)
		if err != nil {
			t.Fatalf("journeyScopeDir: %v", err)
		}
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("create scope dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o600); err != nil {
			t.Fatalf("write scope config: %v", err)
		}
	}

	t.Run("a kept posture passes", func(t *testing.T) {
		c := newContext(t)
		writeScope(t, c, "network:\n  profile: none\n")
		if err := StepAssertNetworkNotWidened(runNetworkPolicy).Run(c); err != nil {
			t.Errorf("a scope keeping none: %v", err)
		}
	})

	t.Run("a scope with no network block passes", func(t *testing.T) {
		if err := StepAssertNetworkNotWidened(runNetworkPolicy).Run(newContext(t)); err != nil {
			t.Errorf("a scope with no network block: %v", err)
		}
	})

	t.Run("a widened posture fails", func(t *testing.T) {
		c := newContext(t)
		writeScope(t, c, "network:\n  profile: public\n")
		if err := StepAssertNetworkNotWidened(runNetworkPolicy).Run(c); err == nil {
			t.Error("a scope naming public over a none baseline: got nil error, want failure")
		}
	})

	t.Run("the journey's egress-allow destination passes", func(t *testing.T) {
		c := newContext(t)
		writeScope(t, c, "network:\n  profile: none\n  egress-allow:\n    - stub.example\n")
		if err := StepAssertNetworkNotWidened(runNetworkPolicy, "stub.example").Run(c); err != nil {
			t.Errorf("a scope allowing the journey's destination: %v", err)
		}
	})

	t.Run("an egress-allow host beyond the allowed set fails", func(t *testing.T) {
		c := newContext(t)
		writeScope(t, c, "network:\n  profile: none\n  egress-allow:\n    - widened.example\n")
		if err := StepAssertNetworkNotWidened(runNetworkPolicy, "stub.example").Run(c); err == nil {
			t.Error("a scope allowing an undeclared host: got nil error, want failure")
		}
	})

	t.Run("an egress-allow host with no allowed set fails", func(t *testing.T) {
		c := newContext(t)
		writeScope(t, c, "network:\n  profile: none\n  egress-allow:\n    - widened.example\n")
		if err := StepAssertNetworkNotWidened(runNetworkPolicy).Run(c); err == nil {
			t.Error("a scope allowing a host no journey drove: got nil error, want failure")
		}
	})
}

// TestStringsFromSetting pins the config-list decoder used for
// network.egress-allow: the accepted shapes decode, a blank scalar is empty, and
// a non-string list entry — or an unexpected type — is rejected rather than
// silently dropped or coerced.
func TestStringsFromSetting(t *testing.T) {
	cases := []struct {
		name    string
		in      any
		want    []string
		wantErr bool
	}{
		{name: "nil", in: nil, want: nil},
		{name: "a scalar", in: "a.example", want: []string{"a.example"}},
		{name: "a blank scalar", in: "   ", want: nil},
		{name: "a string slice", in: []string{"a.example", "b.example"}, want: []string{"a.example", "b.example"}},
		{name: "an any slice", in: []any{"a.example", "b.example"}, want: []string{"a.example", "b.example"}},
		{name: "a non-string entry", in: []any{"a.example", 5}, wantErr: true},
		{name: "an unexpected type", in: 5, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := stringsFromSetting(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("stringsFromSetting(%v) = %v, want an error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("stringsFromSetting(%v): %v", tc.in, err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("stringsFromSetting(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
