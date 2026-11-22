package journeys

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

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

	t.Run("the manual choice records the manual decision", func(t *testing.T) {
		c := newContext(t)
		writeRootState(t, c, "schema: 1\nfirst_runs:\n  \""+keyFor(t, c)+"\":\n    choice: manual\n")
		if err := StepAssertFirstRunRecorded(firstrun.ChoiceManual).Run(c); err != nil {
			t.Errorf("manual choice: %v", err)
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

// fakeStubProvider is a StubProvider for a step test: StepSeedLearnSession only
// needs one present, and it reads the advertised host from the package's
// stubHost rather than the provider.
type fakeStubProvider struct{}

func (fakeStubProvider) Addr() string                      { return "http://stub.invalid" }
func (fakeStubProvider) ProviderConfig() map[string]string { return nil }
func (fakeStubProvider) Stop()                             {}

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
	observation, err := channel.Load(record.ArtefactPath)
	if err != nil {
		t.Fatalf("load the seeded artefact: %v", err)
	}
	if len(observation.Destinations) != 1 || observation.Destinations[0].Host != stubHost {
		t.Errorf("seeded observation = %+v, want one destination at %q", observation, stubHost)
	}
}

// TestStepSeedLearnSessionRequiresThePreRunVolume pins the ordering: a step run
// before the preliminary launcher run has no home volume to copy and fails
// rather than seeding a record the replay would discard.
func TestStepSeedLearnSessionRequiresThePreRunVolume(t *testing.T) {
	const agentName = "opencode2"
	root := t.TempDir()
	if err := os.MkdirAll(driver.ProjectDir(root, agentName), 0o750); err != nil {
		t.Fatalf("create project dir: %v", err)
	}
	ctx := &Context{TB: t, Root: root, Agent: agentName, Home: t.TempDir(), Stub: fakeStubProvider{}}
	if err := StepSeedLearnSession().Run(ctx); err == nil {
		t.Error("no recorded home volume: got nil error, want failure")
	}
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
// baseline: it reads the scope's launcher config and compares the profiles it
// names against the builder's policy, so a scope that kept or narrowed the
// posture passes, one that wrote nothing passes, and one that widened fails.
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
}
