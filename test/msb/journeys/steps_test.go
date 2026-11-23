package journeys

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/firstrun"
	"github.com/inoio/agents-sandbox/internal/sandbox/channel"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
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

	t.Run("the manual choice records the manual decision", func(t *testing.T) {
		c := newContext(t)
		writeRootState(t, c, "schema: 1\nfirst_runs:\n  \""+keyFor(t, c)+"\":\n    choice: manual\n")
		if err := StepAssertFirstRunRecorded(firstrun.ChoiceManual).Run(c); err != nil {
			t.Errorf("manual choice: %v", err)
		}
	})

	t.Run("a missing root state fails", func(t *testing.T) {
		err := StepAssertFirstRunRecorded(firstrun.ChoiceLearnByUsing).Run(newContext(t))
		if err == nil {
			t.Fatal("missing root state file: got nil error, want failure")
		}
		if !strings.Contains(err.Error(), "recorded no first-run decision") {
			t.Errorf("error = %v, want the missing-state failure", err)
		}
	})

	t.Run("a missing key fails", func(t *testing.T) {
		c := newContext(t)
		writeRootState(t, c, "schema: 1\nfirst_runs: {}\n")
		err := StepAssertFirstRunRecorded(firstrun.ChoiceLearnByUsing).Run(c)
		if err == nil {
			t.Fatal("missing key: got nil error, want failure")
		}
		if !strings.Contains(err.Error(), "recorded no first-run decision under") {
			t.Errorf("error = %v, want the missing-key failure", err)
		}
	})

	t.Run("a malformed state file fails at the parse", func(t *testing.T) {
		c := newContext(t)
		writeRootState(t, c, "first_runs: [unclosed\n")
		err := StepAssertFirstRunRecorded(firstrun.ChoiceLearnByUsing).Run(c)
		if err == nil {
			t.Fatal("malformed state: got nil error, want failure")
		}
		if !strings.Contains(err.Error(), "parse") {
			t.Errorf("error = %v, want the parse failure", err)
		}
	})

	t.Run("a choice no path records fails", func(t *testing.T) {
		c := newContext(t)
		writeRootState(t, c, "schema: 1\nfirst_runs: {}\n")
		err := StepAssertFirstRunRecorded(firstrun.Choice("bogus")).Run(c)
		if err == nil {
			t.Fatal("an unmapped choice: got nil error, want failure")
		}
		if !strings.Contains(err.Error(), "records no RootState decision") {
			t.Errorf("error = %v, want the unmapped-choice failure", err)
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

// TestStepAssertScopeExcludesHostLoginStore pins the host-seed login-store guard: a file the
// seed must drop must not appear in the scope's managed agent config dir, so a
// copied login store fails the step rather than passing the key/placeholder
// checks.
func TestStepAssertScopeExcludesHostLoginStore(t *testing.T) {
	const agentName = "opencode2"
	root := t.TempDir()
	if err := os.MkdirAll(driver.ProjectDir(root, agentName), 0o750); err != nil {
		t.Fatalf("create project dir: %v", err)
	}
	ctx := &Context{TB: t, Root: root, Agent: agentName, Harness: opencode2Harness, Home: t.TempDir()}

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
	if err := StepAssertScopeExcludesHostLoginStore().Run(ctx); err == nil {
		t.Error("a copied login store: got nil error, want failure")
	} else if !strings.Contains(err.Error(), "must drop") {
		t.Errorf("error = %v, want the copied-login-store failure", err)
	}
	if err := os.Remove(loginStore); err != nil {
		t.Fatalf("remove %s: %v", loginStore, err)
	}
	if err := StepAssertScopeExcludesHostLoginStore().Run(ctx); err != nil {
		t.Errorf("an absent login store: %v", err)
	}

	unknownRoot := t.TempDir()
	if err := os.MkdirAll(driver.ProjectDir(unknownRoot, "nonexistent"), 0o750); err != nil {
		t.Fatalf("create the unknown agent's project dir: %v", err)
	}
	unknown := &Context{TB: t, Root: unknownRoot, Agent: "nonexistent", Harness: opencode2Harness, Home: t.TempDir()}
	if err := StepAssertScopeExcludesHostLoginStore().Run(unknown); err == nil {
		t.Error("an unknown agent: got nil error, want failure")
	} else if !strings.Contains(err.Error(), "unknown agent") {
		t.Errorf("error = %v, want the unknown-agent failure", err)
	}

	if err := StepAssertScopeExcludesHostLoginStore().Run(&Context{TB: t, Agent: agentName}); err == nil {
		t.Error("an adapter without a login store: got nil error, want failure")
	}
}

// fakeStubProvider is a StubProvider for a step test: StepSeedLearnSession only
// needs one present, and it reads the advertised host from the package's
// stubHost rather than the provider.
type fakeStubProvider struct{}

func (fakeStubProvider) Addr() string            { return "http://stub.invalid" }
func (fakeStubProvider) Requests() []StubRequest { return nil }
func (fakeStubProvider) ScriptToolCall(ToolCall) {}
func (fakeStubProvider) Replies() []string       { return nil }
func (fakeStubProvider) Stop()                   {}

// TestCommandArgvAppendsNoJourneyFlags pins StepLaunchCommand's arg handling:
// the built binary and the command's own arguments, with none of the run flags
// StepLaunch adds (config learn accepts none, so an appended --agent would fail
// on an unknown flag).
func TestCommandArgvAppendsNoJourneyFlags(t *testing.T) {
	got := commandArgv("/tmp/agents-sandbox", []string{"config", "learn"})
	want := []string{"/tmp/agents-sandbox", "config", "learn"}
	if !slices.Equal(got, want) {
		t.Errorf("commandArgv = %v, want %v", got, want)
	}
}

// TestStepLaunchCommandRefusesANonDefaultAgent pins the coupling the step
// records: the flagless command resolves agent.DefaultAgentName, so a journey
// driving any other agent must fail rather than replay a record keyed under a
// different agent.
func TestStepLaunchCommandRefusesANonDefaultAgent(t *testing.T) {
	ctx := &Context{TB: t, Agent: "pi", Root: t.TempDir(), Home: t.TempDir()}
	if err := StepLaunchCommand("config", "learn").Run(ctx); err == nil {
		t.Error("a non-default agent: got nil error, want the coupling failure")
	}
	if ctx.Session != nil {
		t.Error("the step started a session despite refusing the agent")
	}
}

// TestStepSeedLearnSessionSeedsIsolatedStateAndPreservesHomeVolume pins the
// seeding step: it records the replay session with the home volume the
// preliminary run left in the isolated state, saves an artefact naming the stub
// host, and confines every write under the journey's home rather than the
// developer's real XDG state.
func TestStepSeedLearnSessionSeedsIsolatedStateAndPreservesHomeVolume(t *testing.T) {
	const agentName = "opencode2"
	root := t.TempDir()
	if err := os.MkdirAll(driver.ProjectDir(root, agentName), 0o750); err != nil {
		t.Fatalf("create project dir: %v", err)
	}
	home := t.TempDir()
	ctx := &Context{TB: t, Root: root, Agent: agentName, Home: home, Stub: fakeStubProvider{}}

	identity, err := journeyIdentity(ctx)
	if err != nil {
		t.Fatalf("journeyIdentity: %v", err)
	}
	key := state.Key{Slug: identity.CheckoutSlug, Agent: agentName}

	restore := withJourneyStatePaths(home)
	defer restore()
	if err := state.WithHomeState(key, func(cur *state.HomeState, _ bool) (bool, error) {
		cur.HomeVolume = "vol-real"
		return true, nil
	}); err != nil {
		t.Fatalf("seed the pre-run home volume: %v", err)
	}

	if err := StepSeedLearnSession().Run(ctx); err != nil {
		t.Fatalf("StepSeedLearnSession: %v", err)
	}

	record, ok, err := state.ReadLearnSession(key)
	if err != nil {
		t.Fatalf("ReadLearnSession: %v", err)
	}
	if !ok {
		t.Fatal("ReadLearnSession ok = false, want the seeded session")
	}
	if record.HomeVolume != "vol-real" {
		t.Errorf("recorded HomeVolume = %q, want the pre-run's vol-real", record.HomeVolume)
	}
	if record.ArtefactPath == "" {
		t.Fatal("recorded ArtefactPath is empty, want the saved artefact")
	}
	wantDir := filepath.Join(home, isolatedStateDir, launcherConfigDirName, isolatedLearnDirName, key.Slug)
	if dir := filepath.Dir(record.ArtefactPath); dir != wantDir {
		t.Errorf("artefact dir = %q, want %q", dir, wantDir)
	}
	// Pinned literally, not via learnArtefactStartedUnix: a symbolic expectation
	// moves with a mutated constant, so it could never fail.
	if file := filepath.Base(record.ArtefactPath); file != "1763458200.json" {
		t.Errorf("artefact file = %q, want %q", file, "1763458200.json")
	}
	observation, err := channel.Load(record.ArtefactPath)
	if err != nil {
		t.Fatalf("load the seeded artefact: %v", err)
	}
	if len(observation.Destinations) != 1 || observation.Destinations[0].Host != stubHost {
		t.Fatalf("seeded observation = %+v, want one destination at %q", observation, stubHost)
	}
	if observation.Destinations[0].Count != 1 {
		t.Errorf("seeded destination count = %d, want 1", observation.Destinations[0].Count)
	}
}

// TestStepSeedLearnSessionRequiresThePreRunVolume pins the guard's contract: the
// step copies the HomeVolume the preliminary run recorded, so a state file with
// no home volume — what a run that never reached a VM leaves — fails at the
// missing-volume guard rather than seeding a record the replay would discard. A
// missing state file fails earlier, in state.ReadState.
func TestStepSeedLearnSessionRequiresThePreRunVolume(t *testing.T) {
	const agentName = "opencode2"
	newContext := func(t *testing.T) *Context {
		t.Helper()
		root := t.TempDir()
		if err := os.MkdirAll(driver.ProjectDir(root, agentName), 0o750); err != nil {
			t.Fatalf("create project dir: %v", err)
		}
		return &Context{TB: t, Root: root, Agent: agentName, Home: t.TempDir(), Stub: fakeStubProvider{}}
	}

	t.Run("a state file with no home volume fails at the guard", func(t *testing.T) {
		ctx := newContext(t)
		identity, err := journeyIdentity(ctx)
		if err != nil {
			t.Fatalf("journeyIdentity: %v", err)
		}
		key := state.Key{Slug: identity.CheckoutSlug, Agent: agentName}

		restore := withJourneyStatePaths(ctx.Home)
		defer restore()
		// Write a state file that exists but records no home volume.
		if err := state.WithHomeState(key, func(_ *state.HomeState, _ bool) (bool, error) {
			return true, nil
		}); err != nil {
			t.Fatalf("seed an empty home state: %v", err)
		}

		err = StepSeedLearnSession().Run(ctx)
		if err == nil {
			t.Fatal("a state with no home volume: got nil error, want the missing-volume guard")
		}
		if !strings.Contains(err.Error(), "no home volume") {
			t.Errorf("error = %v, want the missing-home-volume guard", err)
		}
	})

	t.Run("a missing state file fails at the read", func(t *testing.T) {
		err := StepSeedLearnSession().Run(newContext(t))
		if err == nil {
			t.Fatal("no state file: got nil error, want a read failure")
		}
		if !errors.Is(err, state.ErrStateNotFound) {
			t.Errorf("error = %v, want a state.ReadState ErrStateNotFound failure", err)
		}
	})

	t.Run("a missing stub provider fails", func(t *testing.T) {
		ctx := newContext(t)
		ctx.Stub = nil
		err := StepSeedLearnSession().Run(ctx)
		if err == nil {
			t.Fatal("no stub provider: got nil error, want the ordering guard")
		}
		if !strings.Contains(err.Error(), "no stub provider") {
			t.Errorf("error = %v, want the missing-stub guard", err)
		}
	})
}

// TestStepAssertLearnArtefactGone pins the deletion assertion: a missing learn
// directory passes, a lingering artefact fails, and an emptied directory passes
// again.
func TestStepAssertLearnArtefactGone(t *testing.T) {
	const agentName = "opencode2"
	root := t.TempDir()
	if err := os.MkdirAll(driver.ProjectDir(root, agentName), 0o750); err != nil {
		t.Fatalf("create project dir: %v", err)
	}
	ctx := &Context{TB: t, Root: root, Agent: agentName, Home: t.TempDir()}
	identity, err := journeyIdentity(ctx)
	if err != nil {
		t.Fatalf("journeyIdentity: %v", err)
	}

	if err := StepAssertLearnArtefactGone().Run(ctx); err != nil {
		t.Errorf("a missing learn directory: %v", err)
	}

	dir := filepath.Join(
		ctx.Home, isolatedStateDir, launcherConfigDirName, isolatedLearnDirName, identity.CheckoutSlug,
	)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create learn dir: %v", err)
	}
	artefact := filepath.Join(dir, "1763458200.json")
	if err := os.WriteFile(artefact, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write %s: %v", artefact, err)
	}
	if err := StepAssertLearnArtefactGone().Run(ctx); err == nil {
		t.Error("a lingering artefact: got nil error, want failure")
	}
	if err := os.Remove(artefact); err != nil {
		t.Fatalf("remove %s: %v", artefact, err)
	}
	if err := StepAssertLearnArtefactGone().Run(ctx); err != nil {
		t.Errorf("an emptied learn directory: %v", err)
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

// TestWithJourneyStatePathsRestoresTheFactory pins the redirect's teardown: after
// the returned restore runs, the config-path factory must be the one the step
// replaced — the fail-fast test default, which panics on any call — not the
// journey resolver. A removed restore leaves the journey resolver installed and
// the next test writing the journey's isolated state.
func TestWithJourneyStatePathsRestoresTheFactory(t *testing.T) {
	home := t.TempDir()
	restore := withJourneyStatePaths(home)
	want := filepath.Join(home, isolatedStateDir, launcherConfigDirName)
	if got := configpaths.Get().UserStateDir(); got != want {
		t.Errorf("redirected UserStateDir() = %q, want %q", got, want)
	}

	restore()
	defer func() {
		if recover() == nil {
			t.Error("the config-path factory was not restored: the fail-fast default must panic after restore")
		}
	}()
	_ = configpaths.Get().UserStateDir()
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

// recordingStubProvider is a StubProvider whose served requests a test sets.
type recordingStubProvider struct {
	fakeStubProvider

	requests []StubRequest
}

func (r recordingStubProvider) Requests() []StubRequest { return r.requests }

func TestStepAssertStubServedPassesOnceAChatCompletionArrived(t *testing.T) {
	stub := recordingStubProvider{requests: []StubRequest{{Method: http.MethodPost, Path: "/v1/chat/completions"}}}
	ctx := &Context{TB: t, Stub: stub}

	if err := stepAssertStubServed(time.Millisecond).Run(ctx); err != nil {
		t.Fatalf("stub served a turn, step failed: %v", err)
	}
}

func TestStepAssertStubServedFailsNamingWhatTheStubSaw(t *testing.T) {
	stub := recordingStubProvider{requests: []StubRequest{{Method: http.MethodGet, Path: "/v1/models"}}}
	ctx := &Context{TB: t, Stub: stub}

	err := stepAssertStubServed(time.Millisecond).Run(ctx)

	if err == nil || !strings.Contains(err.Error(), "/v1/models") {
		t.Fatalf("error = %v, want a failure naming the requests the stub saw", err)
	}
}

func TestStepAssertStubServedNeedsAStub(t *testing.T) {
	if err := stepAssertStubServed(time.Millisecond).Run(&Context{TB: t}); err == nil {
		t.Fatal("step passed with no stub on the context")
	}
}

// repliesStub is a StubProvider whose sent replies a test sets.
type repliesStub struct {
	fakeStubProvider

	replies []string
}

func (r repliesStub) Replies() []string { return r.replies }

func TestNewReplySinceReturnsOnlyAReplySentAfterThePrompt(t *testing.T) {
	stub := repliesStub{replies: []string{"stub-reply-1", "stub-reply-2"}}

	if got, err := newReplySince(stub, 1, time.Millisecond); err != nil || got != "stub-reply-2" {
		t.Errorf("newReplySince(seen 1) = %q, %v; want stub-reply-2", got, err)
	}
	if _, err := newReplySince(stub, 2, time.Millisecond); err == nil {
		t.Error("newReplySince(seen 2) found a reply although none followed the prompt")
	}
}
