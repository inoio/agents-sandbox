package journeys

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/firstrun"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/test/msb/driver"
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

// TestStepAssertFirstRunRecorded pins the UC1 first-run-decision assertion: it
// passes for the "<repo slug>/<agent>" key carrying the recorded learning
// choice, and fails for a missing file, a missing key, or a mismatched choice.
// It reads the isolated root state directly, so the test only needs a project
// dir for the identity and a hand-written state file.
func TestStepAssertFirstRunRecorded(t *testing.T) {
	const agentName = "opencode2"

	newContext := func(t *testing.T) *Context {
		t.Helper()
		root := t.TempDir()
		if err := os.MkdirAll(driver.ProjectDir(root, agentName), 0o750); err != nil {
			t.Fatalf("create project dir: %v", err)
		}
		return &Context{TB: t, Root: root, Agent: agentName, Home: t.TempDir()}
	}
	keyFor := func(t *testing.T, c *Context) string {
		t.Helper()
		identity, err := journeyIdentity(c)
		if err != nil {
			t.Fatalf("journeyIdentity: %v", err)
		}
		return identity.RepoSlug + "/" + c.Agent
	}
	writeRootState := func(t *testing.T, c *Context, body string) {
		t.Helper()
		path := firstRunStateFile(c)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("create state dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	t.Run("a matching record passes", func(t *testing.T) {
		c := newContext(t)
		writeRootState(t, c, "schema: 1\nfirst_runs:\n  \""+keyFor(t, c)+"\":\n    choice: learned\n")
		if err := StepAssertFirstRunRecorded(firstrun.ChoiceLearnByUsing).Run(c); err != nil {
			t.Errorf("matching record: %v", err)
		}
	})

	t.Run("the host-seed choice records the same decision", func(t *testing.T) {
		c := newContext(t)
		writeRootState(t, c, "schema: 1\nfirst_runs:\n  \""+keyFor(t, c)+"\":\n    choice: learned\n")
		if err := StepAssertFirstRunRecorded(firstrun.ChoiceLearnFromHost).Run(c); err != nil {
			t.Errorf("host-seed choice: %v", err)
		}
	})

	t.Run("a missing root state fails", func(t *testing.T) {
		if err := StepAssertFirstRunRecorded(firstrun.ChoiceLearnByUsing).Run(newContext(t)); err == nil {
			t.Error("missing root state file: got nil error, want failure")
		}
	})

	t.Run("a missing key fails", func(t *testing.T) {
		c := newContext(t)
		writeRootState(t, c, "schema: 1\nfirst_runs: {}\n")
		if err := StepAssertFirstRunRecorded(firstrun.ChoiceLearnByUsing).Run(c); err == nil {
			t.Error("missing key: got nil error, want failure")
		}
	})

	t.Run("a mismatched choice fails", func(t *testing.T) {
		c := newContext(t)
		writeRootState(t, c, "schema: 1\nfirst_runs:\n  \""+keyFor(t, c)+"\":\n    choice: manual\n")
		if err := StepAssertFirstRunRecorded(firstrun.ChoiceLearnByUsing).Run(c); err == nil {
			t.Error("mismatched choice: got nil error, want failure")
		}
	})
}
