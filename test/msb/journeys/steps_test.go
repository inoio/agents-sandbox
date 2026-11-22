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

// TestStepSeedHostConfigDerivesFilesFromContext pins the changed step contract:
// the host files are built from the journey context at run time, so a host config
// can point at a facility an earlier step provisioned (UC4's provider baseURL is
// the stub's runtime address). It also pins where the files land: under the
// isolated HOME the first-run host probe reads.
func TestStepSeedHostConfigDerivesFilesFromContext(t *testing.T) {
	home := t.TempDir()
	ctx := &Context{TB: t, Agent: "opencode2", Home: home}
	const rel = ".config/opencode/opencode.jsonc"
	const content = `{"provider":{"stub":{}}}`

	built := false
	step := StepSeedHostConfig(func(c *Context) map[string]string {
		built = true
		if c != ctx {
			t.Errorf("the builder got context %p, want %p", c, ctx)
		}
		return map[string]string{rel: content}
	})
	if err := step.Run(ctx); err != nil {
		t.Fatalf("StepSeedHostConfig: %v", err)
	}
	if !built {
		t.Fatal("the builder was not called")
	}
	// The isolated HOME is <homeBase>/home (driver.homeDirName, unexported).
	written, err := os.ReadFile(filepath.Join(home, "home", rel))
	if err != nil {
		t.Fatalf("read the seeded host config: %v", err)
	}
	if string(written) != content {
		t.Errorf("seeded content = %q, want %q", written, content)
	}
}

// TestStepAssertScopeExcludes pins the host-seed login-store guard: a file the
// seed must drop must not appear in the scope's managed agent config dir, so a
// copied login store fails the step rather than passing the key/placeholder
// checks.
func TestStepAssertScopeExcludes(t *testing.T) {
	const agentName = "opencode2"
	root := t.TempDir()
	if err := os.MkdirAll(driver.ProjectDir(root, agentName), 0o750); err != nil {
		t.Fatalf("create project dir: %v", err)
	}
	ctx := &Context{TB: t, Root: root, Agent: agentName, Home: t.TempDir()}

	scopeDir, err := journeyScopeDir(ctx)
	if err != nil {
		t.Fatalf("journeyScopeDir: %v", err)
	}
	configDir := filepath.Join(scopeDir, agentName)
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		t.Fatalf("create scope config dir: %v", err)
	}
	loginStore := filepath.Join(configDir, "auth.json")
	if err := os.WriteFile(loginStore, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write %s: %v", loginStore, err)
	}
	if err := StepAssertScopeExcludes("auth.json").Run(ctx); err == nil {
		t.Error("a copied login store: got nil error, want failure")
	}
	if err := os.Remove(loginStore); err != nil {
		t.Fatalf("remove %s: %v", loginStore, err)
	}
	if err := StepAssertScopeExcludes("auth.json").Run(ctx); err != nil {
		t.Errorf("an absent login store: %v", err)
	}
}
