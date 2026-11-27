package journeys

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/channel"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/test/msb/driver"
)

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

func TestExitChoiceLabelNamesThePromptsChoices(t *testing.T) {
	for key, want := range map[string]string{exitSaveAllKey: "Save all", exitNotNowKey: "Not now", "r": `"r"`} {
		if got := exitChoiceLabel(key); got != want {
			t.Errorf("exitChoiceLabel(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestObservedHostProblemFindsTheStubsHost(t *testing.T) {
	home := t.TempDir()
	learnDir := filepath.Join(home, isolatedStateDir, launcherConfigDirName, "learn")
	if err := observedHostProblem(
		learnDir,
		"stub.test",
	); err == nil ||
		!strings.Contains(err.Error(), "no learn artefact") {
		t.Errorf("no artefact: err = %v, want the missing artefact named", err)
	}

	restore := withJourneyStatePaths(home)
	defer restore()
	observation := channel.Observation{Destinations: []channel.Destination{{Host: "other.test"}}}
	if _, err := channel.Save("slug", 1, observation); err != nil {
		t.Fatalf("save observation: %v", err)
	}
	if err := observedHostProblem(learnDir, "stub.test"); err == nil || !strings.Contains(err.Error(), "other.test") {
		t.Errorf("another host only: err = %v, want the hosts it saw named", err)
	}
	observation.Destinations = append(observation.Destinations, channel.Destination{Host: "stub.test"})
	if _, err := channel.Save("slug", 2, observation); err != nil {
		t.Fatalf("save observation: %v", err)
	}
	if err := observedHostProblem(learnDir, "stub.test"); err != nil {
		t.Errorf("the stub's host observed: err = %v", err)
	}
}

func TestPlaceholderNotKey(t *testing.T) {
	store := exposedLoginKeys[codexAgentName]
	if err := placeholderNotKey(`{"OPENAI_API_KEY":"${OPENAI_API_KEY}"}`, store); err != nil {
		t.Errorf("the placeholdered store: %v", err)
	}
	for name, text := range map[string]string{
		"the key":                 `{"OPENAI_API_KEY":"` + exposedLoginKeyValue + `"}`,
		"the key and placeholder": `{"a":"${OPENAI_API_KEY}","b":"` + exposedLoginKeyValue + `"}`,
		"no placeholder":          `{"OPENAI_API_KEY":"$MSB_OPENAI_API_KEY"}`,
	} {
		if placeholderNotKey(text, store) == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestProvisionedStoreDeclared(t *testing.T) {
	const declaredConfig = "home:\n  .codex/auth.json:\n    source: home/codex-auth.json\n    keep-login: true\n"
	const placeholderStore = `{"OPENAI_API_KEY":"${OPENAI_API_KEY}"}`
	for _, entry := range []struct {
		name, config, content string
		copies                int
		wantError             string
	}{
		{"valid declaration", declaredConfig, placeholderStore, 1, ""},
		{"missing config", "", placeholderStore, 1, "read the per-slug config"},
		{"wrong target", strings.Replace(declaredConfig, ".codex/auth.json:", ".other/auth.json:", 1), placeholderStore, 1, "does not provision"},
		{"missing keep-login", "home:\n  .codex/auth.json: home/codex-auth.json\n", placeholderStore, 1, "with keep-login"},
		{"no source", declaredConfig, placeholderStore, 0, "want one provisioned store"},
		{"multiple sources", declaredConfig, placeholderStore, 2, "want one provisioned store"},
		{"plain-text key", declaredConfig, `{"OPENAI_API_KEY":"` + exposedLoginKeyValue + `"}`, 1, "still holds the plain-text key"},
		{"no placeholder", declaredConfig, `{}`, 1, "does not hold the"},
	} {
		t.Run(entry.name, func(t *testing.T) {
			dir := t.TempDir()
			if entry.config != "" {
				writeTestFile(t, filepath.Join(dir, "config.yaml"), entry.config)
			}
			for index := range entry.copies {
				name := "codex-auth.json"
				if index > 0 {
					name = fmt.Sprintf("extra-auth-%d.json", index)
				}
				writeTestFile(t, filepath.Join(dir, "home", name), entry.content)
			}
			err := provisionedStoreDeclared(dir, exposedLoginKeys[codexAgentName])
			if entry.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), entry.wantError) {
				t.Fatalf("got %v, want %q", err, entry.wantError)
			}
			if entry.config == "" && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("missing config lost its filesystem cause: %v", err)
			}
		})
	}
}

func TestHoldsPlaceholderNotKeyReadsTheStore(t *testing.T) {
	for _, entry := range []struct {
		name, content, wantError string
	}{
		{"placeholder", `{"OPENAI_API_KEY":"${OPENAI_API_KEY}"}`, ""},
		{"plain-text key", `{"OPENAI_API_KEY":"` + exposedLoginKeyValue + `"}`, "still holds the plain-text key"},
		{"no placeholder", `{}`, "does not hold the"},
	} {
		t.Run(entry.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth.json")
			writeTestFile(t, path, entry.content)
			err := holdsPlaceholderNotKey(path, exposedLoginKeys[codexAgentName])
			if entry.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), entry.wantError) {
				t.Fatalf("got %v, want %q", err, entry.wantError)
			}
		})
	}
	t.Run("missing store", func(t *testing.T) {
		err := holdsPlaceholderNotKey(filepath.Join(t.TempDir(), "missing.json"), exposedLoginKeys[codexAgentName])
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing store: got %v, want the filesystem cause", err)
		}
	})
}

// writeTestFile writes content to path, creating its directory.
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
