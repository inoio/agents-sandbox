package journeys

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v4"

	"github.com/inoio/agents-sandbox/internal/sandbox/channel"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// The launcher's learn exit prompt asks whether to save what the session
// learned; the journey answers it after the harness TUI is gone, so StepQuit
// waits for this line rather than for the process to exit.
const learnExitPromptMarker = "Save what agents-sandbox learned from this session?"

// exitSaveAllKey answers the learn exit prompt with "s" (Save all): the exit
// step takes a plain string, so the key is named here rather than imported from
// learn, whose exit keys are unexported.
const exitSaveAllKey = "s"

// exitNotNowKey answers the learn exit prompt with "n" (Not now): the
// preliminary `config learn` journey run reaches the TUI, quits, and writes
// nothing, leaving the home volume and VM-derived state it created for the
// replay. The key is named here for the same reason as exitSaveAllKey.
const exitNotNowKey = "n"

// The applier's per-finding prompt belongs to analyser's unexported
// applyFindingKey, so it is named here like the exit keys: applyFindingKey
// answers one finding's Select with "a" (Apply).
const applyFindingKey = "a"

// learnArtefactStartedUnix names the seeded artefact: channel.Save keys a
// session's artefact by its start time, and config learn loads it back through
// the recorded path. Any fixed instant works; it only has to be the same one the
// record and the save use.
const learnArtefactStartedUnix = 1763458200

// StepQuit taps the harness's quit key until the launcher's learn exit prompt
// appears, so the journey can answer it in StepChooseExit. It stops once the
// prompt is up rather than waiting for the process to exit, which the prompt
// holds open. The wait reuses the driver's tapping cadence and scales its budget
// with the smoke timeout factor, like driver.Quit.
func StepQuit() Step {
	return Step{Name: "the user quits the harness and the learn exit prompt appears", Run: func(c *Context) error {
		ses, err := session(c)
		if err != nil {
			return err
		}
		if len(c.Spec.QuitKey) == 0 {
			return errors.New("the harness has no quit key")
		}
		budget := scaled(driver.QuitTimeout)
		return driver.TapUntilMarker(ses, c.Spec.QuitKey, learnExitPromptMarker, budget)
	}}
}

// exitChoiceLabel names a learn exit prompt key as the prompt shows it.
func exitChoiceLabel(key string) string {
	switch key {
	case exitSaveAllKey:
		return "Save all"
	case exitNotNowKey:
		return "Not now"
	}
	return strconv.Quote(key)
}

// StepChooseExit answers the learn exit prompt with key (the launcher's
// r/s/n choice) and waits for the launcher to exit 0. Answering "Save all" runs
// the analysis/save path before the process exits, so the post-answer wait uses
// the larger ExitSaveTimeout rather than the quit budget.
func StepChooseExit(key string) Step {
	return Step{Name: "the user answers the exit prompt with " + exitChoiceLabel(key), Run: func(c *Context) error {
		ses, err := session(c)
		if err != nil {
			return err
		}
		if err = writeLine(c, key); err != nil {
			return err
		}
		code, err := ses.WaitExit(scaled(driver.ExitSaveTimeout))
		if err != nil {
			return fmt.Errorf("the launcher did not exit after the exit prompt: %w", err)
		}
		if code != 0 {
			return fmt.Errorf("the launcher exited %d, want 0", code)
		}
		return nil
	}}
}

// StepSeedLearnSession seeds the state a `config learn` replay reads: it copies
// the HomeVolume the preliminary run recorded into a fresh LearnSession, saves an
// observation naming the stub host as the session's artefact, and records both
// through the production helpers. The replay then runs as if the owning process
// were gone.
//
// The write goes through an isolated config-path resolver rooted at the
// journey's home (see withJourneyStatePaths), never the developer's real XDG
// state, and the recorded home volume is preserved so the replay does not
// discard the session as volume-recreated.
func StepSeedLearnSession() Step {
	return Step{Name: "a recorded learning session whose launcher is gone", Run: func(c *Context) error {
		if c.Stub == nil {
			return errors.New("no stub provider: StepSeedStub must run before StepSeedLearnSession")
		}
		identity, err := journeyIdentity(c)
		if err != nil {
			return err
		}
		key := state.Key{Slug: identity.CheckoutSlug, Agent: c.Agent}
		observation := channel.Observation{
			Destinations: []channel.Destination{{
				Host:     stubHost,
				Protocol: "http",
				Success:  true,
				Count:    1,
			}},
		}

		journeyStatePathsLock.Lock()
		defer journeyStatePathsLock.Unlock()
		restore := withJourneyStatePaths(c.Home)
		defer restore()

		homeState, err := state.ReadState(key)
		if err != nil {
			return fmt.Errorf("read the pre-run state for %s/%s: %w", key.Slug, key.Agent, err)
		}
		if homeState.HomeVolume == "" {
			return fmt.Errorf("the preliminary run recorded no home volume for %s/%s", key.Slug, key.Agent)
		}
		artefactPath, err := channel.Save(key.Slug, learnArtefactStartedUnix, observation)
		if err != nil {
			return fmt.Errorf("save the seed artefact: %w", err)
		}
		record := state.LearnSession{
			StartedAt:    time.Unix(learnArtefactStartedUnix, 0).UTC(),
			HomeVolume:   homeState.HomeVolume,
			ArtefactPath: artefactPath,
			// An empty baseline makes every provisioned config path "changed"
			// for the replay, so the stub provider's config reaches the
			// managed-config write-back (see configLearnScope). The journey
			// cannot synthesize the run's real baseline, whose hashes live only
			// in the volume.
			Baseline: nil,
		}
		if err := state.RecordLearnSession(key, record); err != nil {
			return fmt.Errorf("record the seed session: %w", err)
		}
		return nil
	}}
}

// StepAnswerApplyPrompts answers each of the replay's per-finding apply prompts
// with Apply ("a"), then waits for the process to exit 0. `config learn` runs
// the analysis with AcceptAll off (learn.Analyse), so the applier puts one
// Select per finding — network, secret, and the managed config — and a single
// written line only answers the first. The step taps the apply key on the quit
// cadence until the process leaves, so the finding count is not a constant the
// journey pins, and the post-answer wait reuses StepChooseExit's scaled budget.
func StepAnswerApplyPrompts() Step {
	return Step{Name: "the user applies every proposal", Run: func(c *Context) error {
		ses, err := session(c)
		if err != nil {
			return err
		}
		budget := scaled(driver.ExitSaveTimeout)
		return answerApplyPrompts(ses, budget)
	}}
}

// answerApplyPrompts writes one Apply answer per prompt until the process exits
// and reports its exit code. It stops on exit rather than on a fixed finding
// count, so an extra buffered answer is harmless and a prompt it has not yet seen
// is still answered. An answer written after the process has exited is not an
// error; one written while it is alive and fails is.
func answerApplyPrompts(ses *driver.Session, budget time.Duration) error {
	answer := []byte(applyFindingKey + "\n")
	answerUntilExit := func() (bool, error) {
		if ses.HasExited() {
			return true, nil
		}
		// gomutants:disable-next-line EXPRESSION_REMOVE,REMOVE_LOGICAL_NOT,BRANCH_IF reason="a write to a live child pty does not fail, and one racing the exit cannot be staged; both cases need a failing write"
		if err := ses.Write(answer); err != nil && !ses.HasExited() {
			return false, fmt.Errorf("write an apply answer: %w", err)
		}
		// The next poll sees the exit.
		return false, nil
	}
	if err := pollUntil(budget, driver.QuitPressInterval, answerUntilExit, func() error {
		return fmt.Errorf("the replay did not exit within %s while answering the apply prompts", budget)
	}); err != nil {
		return err
	}
	code, err := ses.WaitExit(time.Second)
	if err != nil {
		return fmt.Errorf("the replay did not exit after the apply prompts: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("the replay exited %d, want 0", code)
	}
	return nil
}

// StepAssertLearnArtefactGone asserts the isolated learn-artefact directory
// holds no artefact, so a consumed replay deleted the capture it loaded. An
// absent directory passes: a replay that never saved one leaves none, and the
// deletion is a no-op on a missing path.
func StepAssertLearnArtefactGone() Step {
	return AsCheck(Step{Name: "the learn artefact is deleted", Run: func(c *Context) error {
		identity, err := journeyIdentity(c)
		if err != nil {
			return err
		}
		dir := filepath.Join(
			c.Home, isolatedStateDir, launcherConfigDirName, isolatedLearnDirName, identity.CheckoutSlug,
		)
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", dir, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				return fmt.Errorf("the replay left the learn artefact %s behind", filepath.Join(dir, entry.Name()))
			}
		}
		return nil
	}})
}

// StepAssertSessionCleared asserts the project's state file records no learning
// session, so an owning-process-free replay cleared its record after analysis.
func StepAssertSessionCleared() Step {
	return AsCheck(Step{Name: "the learn session record is cleared", Run: func(c *Context) error {
		path, err := journeyStateFile(c)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		var document struct {
			Learn *struct{} `yaml:"learn"`
		}
		if err := yaml.Unmarshal(data, &document); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if document.Learn != nil {
			return fmt.Errorf("the learn session record is still present in %s", path)
		}
		return nil
	}})
}

// StepAssertStubObserved checks that a saved learn artefact under the
// journey's isolated state names the stub's advertised host, so the turn went
// through the learn observation proxy. It reads the artefact while the exit
// prompt holds the launcher open (the after-run deletes it once answered), and
// names every host it saw, so a route the proxy sees under another name is
// diagnosable.
func StepAssertStubObserved() Step {
	return AsCheck(Step{Name: "the learn proxy observed the stub's host", Run: func(c *Context) error {
		if c.Stub == nil {
			return errors.New("no stub provider on the context; seed it first")
		}
		addr, err := url.Parse(c.Stub.Addr())
		if err != nil || addr.Hostname() == "" {
			return fmt.Errorf("stub address %q is not a URL with a host", c.Stub.Addr())
		}
		return observedHostProblem(
			filepath.Join(c.Home, isolatedStateDir, launcherConfigDirName, "learn"),
			addr.Hostname(),
		)
	}})
}

// observedHostProblem reports why no learn artefact under learnDir names host.
func observedHostProblem(learnDir, host string) error {
	artefacts, err := filepath.Glob(filepath.Join(learnDir, "*", "*.json"))
	if err != nil {
		return fmt.Errorf("glob learn artefacts under %s: %w", learnDir, err)
	}
	if len(artefacts) == 0 {
		return fmt.Errorf("the run saved no learn artefact under %s", learnDir)
	}
	var observed []string
	for _, path := range artefacts {
		observation, err := channel.Load(path)
		if err != nil {
			return fmt.Errorf("load observation %s: %w", path, err)
		}
		for _, destination := range observation.Destinations {
			if destination.Host == host {
				return nil
			}
			observed = append(observed, destination.Host)
		}
	}
	return fmt.Errorf("the saved observation names no destination for %q (saw %v)", host, observed)
}

// StepSeedExposedLoginKey stores a plain-text key in the harness's login store,
// over the stub seeds: it adds the key field to the seeded file (claude-code's
// ~/.claude.json carries its onboarding state) or starts the file with it.
func StepSeedExposedLoginKey() Step {
	return Step{Name: "the harness's login store holds a plain-text API key", Run: func(c *Context) error {
		store, ok := exposedLoginKeys[c.Agent]
		if !ok {
			return fmt.Errorf("no exposed login key store is recorded for %s", c.Agent)
		}
		seeds := stubSeeds(c)
		document := map[string]any{}
		if seeded, ok := seeds.HomeManifest[store.path]; ok {
			if err := json.Unmarshal([]byte(seeded), &document); err != nil {
				return fmt.Errorf("parse the seeded %s: %w", store.path, err)
			}
		}
		document[store.field] = exposedLoginKeyValue
		seeds = seeds.Merge(driver.Seeds{HomeManifest: map[string]string{store.path: mustJSON(document)}})
		return seeds.Apply(c.Home, "")
	}}
}

// StepAssertExposedLoginKeyMoved checks that the saved proposal moved the key
// into the per-slug scope: an env secret for the provider's host and, where
// the harness reads the key from its store, a keep-login home: entry that
// provisions the store with the secret's placeholder.
func StepAssertExposedLoginKeyMoved() Step {
	return AsCheck(Step{Name: "the per-slug scope holds the key as a secret", Run: func(c *Context) error {
		dir, err := journeyScopeDir(c)
		if err != nil {
			return err
		}
		store := exposedLoginKeys[c.Agent]
		if err := assertSecretScope(dir, scopeExpectation{
			envVar: store.envVar, hosts: []string{store.host}, configKeys: nil, placeholder: false,
		}); err != nil {
			return err
		}
		if !store.provisioned {
			return nil
		}
		return provisionedStoreDeclared(dir, store)
	}})
}

// provisionedStoreDeclared checks dir's config for the store's keep-login home:
// entry and its host copy for the secret's placeholder, without the key.
func provisionedStoreDeclared(dir string, store exposedLoginKey) error {
	config, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		return fmt.Errorf("read the per-slug config: %w", err)
	}
	if !strings.Contains(string(config), store.path) || !strings.Contains(string(config), "keep-login: true") {
		return fmt.Errorf("the per-slug config does not provision %s with keep-login:\n%s", store.path, config)
	}
	// The pattern is a fixed, valid glob, so Glob cannot fail on it.
	copies, _ := filepath.Glob(filepath.Join(dir, "home", "*"))
	if len(copies) != 1 {
		return fmt.Errorf("want one provisioned store under %s/home, got %v", dir, copies)
	}
	return holdsPlaceholderNotKey(copies[0], store)
}

// holdsPlaceholderNotKey checks that the file at path holds the secret's
// placeholder and not the plain-text key.
func holdsPlaceholderNotKey(path string, store exposedLoginKey) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return placeholderNotKey(string(content), store)
}

// placeholderNotKey checks text for the secret's placeholder and against the key.
func placeholderNotKey(text string, store exposedLoginKey) error {
	if strings.Contains(text, exposedLoginKeyValue) {
		return fmt.Errorf("the store still holds the plain-text key: %s", text)
	}
	if !strings.Contains(text, "${"+store.envVar+"}") {
		return fmt.Errorf("the store does not hold the ${%s} placeholder: %s", store.envVar, text)
	}
	return nil
}

// StepAssertProvisionedLoginStore checks, in the VM, that the launcher now
// provisions the store with the placeholder in place of the plain-text key: a
// shell client provisions the home on connecting. A harness that reads the key
// from its environment has no store to check.
func StepAssertProvisionedLoginStore() Step {
	return AsCheck(Step{Name: "the VM's login store holds the placeholder, not the key", Run: func(c *Context) error {
		store := exposedLoginKeys[c.Agent]
		if !store.provisioned {
			return nil
		}
		code, ses, err := runInShell(c, "cat ~/"+store.path)
		if err != nil || code != 0 {
			return fmt.Errorf("read ~/%s in the VM (exit %d): %w", store.path, code, err)
		}
		return placeholderNotKey(driver.StripANSI(ses.Output()), store)
	}})
}
