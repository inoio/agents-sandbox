package journeys

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v4"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configdecode"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/firstrun"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/channel"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/viperconfig"
	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// ScopeWant is the semantic effect StepAssertScopeWritten checks in the per-slug
// user scope: the credential's env-var name, the hosts it is scoped to, the
// config keys the managed config must carry, and whether the managed config
// references the credential as the ${ENVVAR} placeholder. It is deliberately
// semantic, never a full-file golden.
type ScopeWant struct {
	EnvVar      string
	Hosts       []string
	ConfigKeys  []string
	Placeholder bool
}

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

// The isolated XDG tree driver.IsolatedEnv roots under a journey's home base:
// the launcher's config lives under config/agents-sandbox, its project state
// under state/agents-sandbox, and its learn artefacts under
// state/agents-sandbox/learn/<checkout-slug>. The names mirror driver.env.go
// and channel's private constants, which the journeys package cannot import.
const (
	isolatedConfigDir     = "config"
	isolatedStateDir      = "state"
	isolatedLearnDirName  = "learn"
	launcherConfigDirName = "agents-sandbox"
)

// StepOwnProject moves the journey onto a project of its own, named
// <agent>-<label>, and builds the agent's runner image there: the journey then
// gets its own project slug, VM, and home volume instead of the agent's shared
// project, so a VM another journey left running cannot leak into it.
func StepOwnProject(label string) Step {
	return Step{Name: "an own project " + label, Run: func(c *Context) error {
		project := driver.ProjectDir(c.Root, c.Agent+"-"+label)
		if err := driver.BuildProjectImage(c.Root, c.Binary, project, c.Agent); err != nil {
			return err
		}
		c.Project = project
		return nil
	}}
}

// StepFreshHome prepares a journey's isolated HOME, so it starts with no host
// config and no managed layer: no first-run decision exists and the menu is
// offered.
func StepFreshHome() Step {
	return Step{Name: "fresh isolated home", Run: func(c *Context) error {
		return driver.PrepareHome(c.Home)
	}}
}

// StepSeedHostConfig writes home-relative agent config under the journey's
// isolated HOME, the way an agent's own host config would be present: the
// first-run decision sees host setup and offers "Learn from my host setup".
//
// build derives the files from the journey context at run time, so a host config
// that depends on an earlier step can be rendered then: UC4's provider baseURL
// points at the stub's runtime address, which only StepSeedStub knows. A later
// journey needing the same shape (Task 17's attribution journey) reuses it.
func StepSeedHostConfig(build func(*Context) map[string]string) Step {
	return Step{Name: "seed host agent config", Run: func(c *Context) error {
		return driver.SeedHomeFiles(c.Home, build(c))
	}}
}

// StepSeedStub starts the journey's host-side stub provider and provisions the
// agent's VM config so its provider points at the stub, storing the provider on
// the context. It writes the file through the launcher's `home:` manifest
// (driver.SeedLauncherConfig), not the agent's launcher config dir and not the
// home the first-run host seed reads, so a fresh-home journey still gets the
// first-run menu rather than a managed layer or a host-setup seed. The listener
// is closed by StartStubProvider's tb.Cleanup.
func StepSeedStub() Step {
	return Step{Name: "seed the stub provider", Run: func(c *Context) error {
		provider, err := StartStubProvider(c.TB)
		if err != nil {
			return err
		}
		c.Stub = provider
		if err := driver.SeedLauncherConfig(c.Home, provider.ProviderConfig()); err != nil {
			return fmt.Errorf("provision stub provider config: %w", err)
		}
		return nil
	}}
}

// StepLaunch starts the built launcher under the journey's isolated environment
// and stores the session on the context. args are the launcher's subcommand and
// flags (`run`, `--learn`, …); the step appends the journey's agent, its network
// profile, the smoke memory cap, and --no-rebuild so the run reuses the image
// the suite prebuilt. It deliberately does not pass --yes: a journey drives the
// interactive first-run and exit prompts.
func StepLaunch(args ...string) Step {
	return Step{Name: "launch " + strings.Join(args, " "), Run: func(c *Context) error {
		argv := make([]string, 0, len(args)+8)
		argv = append(argv, c.Binary)
		argv = append(argv, args...)
		argv = append(argv, "--agent", c.Agent)
		argv = append(argv, "--network", c.Spec.NetworkOrDefault())
		argv = append(argv, "--memory", driver.SmokeMemorySpec(), "--no-rebuild")
		argv = driver.WithVMCPUs(argv, driver.SmokeVMCPUs())
		//testenv:allow the journey launcher runs under the host environment it isolates from
		env := driver.IsolatedEnv(c.Root, c.Home, os.Environ())
		c.Session = driver.MustStartUnderPTY(c.TB, c.ProjectDir(), env, argv...)
		return nil
	}}
}

// StepChooseFirstRun answers the first-run menu with choice's key, which the
// launcher's termio Select reads as a line.
func StepChooseFirstRun(choice firstrun.Choice) Step {
	return Step{Name: "choose first-run " + string(choice), Run: func(c *Context) error {
		if _, err := session(c); err != nil {
			return err
		}
		return writeLine(c, string(choice))
	}}
}

// StepDeclineHostProvisioning answers the manual first-run choice's
// host-config provisioning question with no, so the run provisions only what the
// journey seeded.
func StepDeclineHostProvisioning() Step {
	return Step{Name: "decline host-config provisioning", Run: func(c *Context) error {
		if _, err := session(c); err != nil {
			return err
		}
		return writeLine(c, "n")
	}}
}

// StepAssertFirstRunOffered blocks until the first-run menu prints choice's key,
// which both waits for the menu and asserts the choice was offered. A menu that
// omits the choice (no host config, so no "Learn from my host setup") never
// prints its key and the wait times out with the choice named.
func StepAssertFirstRunOffered(choice firstrun.Choice) Step {
	return Step{Name: "assert first-run offers " + string(choice), Run: func(c *Context) error {
		ses, err := session(c)
		if err != nil {
			return err
		}
		if err := ses.WaitForMarker(
			string(choice),
			driver.ScaleTimeout(driver.TuiReadyTimeout, driver.SmokeTimeoutFactor()),
		); err != nil {
			return fmt.Errorf("first-run menu did not offer %q: %w", choice, err)
		}
		return nil
	}}
}

// StepWaitReady blocks until the harness reaches its TUI, using the harness
// table's readiness signal.
func StepWaitReady() Step {
	return Step{Name: "wait for the harness TUI", Run: func(c *Context) error {
		ses, err := session(c)
		if err != nil {
			return err
		}
		return driver.WaitReady(ses, c.Spec)
	}}
}

// StepSendPrompt types prompt into the harness's editor, retyping it until the
// harness echoes it (see typeUntilEchoed), and presses Enter.
func StepSendPrompt(prompt string) Step {
	return Step{Name: "send prompt", Run: func(c *Context) error {
		ses, err := session(c)
		if err != nil {
			return err
		}
		echoWait := driver.ScaleTimeout(promptEchoWait, driver.SmokeTimeoutFactor())
		if err := typeUntilEchoed(ses, prompt, promptTypeAttempts, echoWait); err != nil {
			return err
		}
		return ses.Write([]byte("\r"))
	}}
}

// stubTurnTimeout bounds how long a turn may take to reach the stub provider
// after the prompt was sent; it is a ceiling, not the expected wait.
const stubTurnTimeout = 60 * time.Second

// StepAssertStubServed waits until the stub provider served a chat completion,
// proving the harness's turn reached the host-side stub.
func StepAssertStubServed() Step {
	return stepAssertStubServed(driver.ScaleTimeout(stubTurnTimeout, driver.SmokeTimeoutFactor()))
}

func stepAssertStubServed(budget time.Duration) Step {
	return Step{Name: "the stub provider served the turn", Run: func(c *Context) error {
		if c.Stub == nil {
			return errors.New("no stub provider on the context; seed it first")
		}
		deadline := time.Now().Add(budget)
		for !servedTurn(c.Stub.Requests()) {
			if !time.Now().Before(deadline) {
				return fmt.Errorf(
					"the stub provider served no turn within %s (requests: %v)",
					budget,
					c.Stub.Requests(),
				)
			}
			time.Sleep(echoPollInterval)
		}
		return nil
	}}
}

// StepAssertReplyShown waits until the harness shows the stub's reply, so the
// turn completed rather than only reaching the stub.
func StepAssertReplyShown() Step {
	return Step{Name: "the harness shows the stub's reply", Run: func(c *Context) error {
		ses, err := session(c)
		if err != nil {
			return err
		}
		return ses.WaitForMarker(StubReply, driver.ScaleTimeout(stubTurnTimeout, driver.SmokeTimeoutFactor()))
	}}
}

// StepQuitToExit leaves the harness with its quit key and waits for the
// launcher to exit 0: the quit for a run with no learn exit prompt.
func StepQuitToExit() Step {
	return Step{Name: "quit the harness and the launcher exits", Run: func(c *Context) error {
		ses, err := session(c)
		if err != nil {
			return err
		}
		code, err := driver.Quit(ses, c.Spec)
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("the launcher exited %d, want 0", code)
		}
		return nil
	}}
}

// StepQuit taps the harness's quit key until the launcher's learn exit prompt
// appears, so the journey can answer it in StepChooseExit. It stops once the
// prompt is up rather than waiting for the process to exit, which the prompt
// holds open. The wait reuses the driver's tapping cadence and scales its budget
// with the smoke timeout factor, like driver.Quit.
func StepQuit() Step {
	return Step{Name: "quit the harness", Run: func(c *Context) error {
		ses, err := session(c)
		if err != nil {
			return err
		}
		if len(c.Spec.QuitKey) == 0 {
			return errors.New("the harness has no quit key")
		}
		budget := driver.ScaleTimeout(driver.QuitTimeout, driver.SmokeTimeoutFactor())
		return driver.TapUntilMarker(ses, c.Spec.QuitKey, learnExitPromptMarker, budget)
	}}
}

// StepChooseExit answers the learn exit prompt with key (the launcher's
// r/s/n choice) and waits for the launcher to exit 0. Answering "Save all" runs
// the analysis/save path before the process exits, so the post-answer wait uses
// the larger ExitSaveTimeout rather than the quit budget.
func StepChooseExit(key string) Step {
	return Step{Name: "choose exit " + key, Run: func(c *Context) error {
		ses, err := session(c)
		if err != nil {
			return err
		}
		if err = writeLine(c, key); err != nil {
			return err
		}
		code, err := ses.WaitExit(driver.ScaleTimeout(driver.ExitSaveTimeout, driver.SmokeTimeoutFactor()))
		if err != nil {
			return fmt.Errorf("the launcher did not exit after the exit prompt: %w", err)
		}
		if code != 0 {
			return fmt.Errorf("the launcher exited %d, want 0", code)
		}
		return nil
	}}
}

// StepLaunchCommand starts the built launcher under the journey's isolated
// environment with the project directory as its CWD and stores the session, like
// StepLaunch but appending no journey flags. It drives a subcommand that takes
// none of the run flags StepLaunch adds (--agent, --network, --memory,
// --no-rebuild, the CPU cap): `config learn` declares no flags, so a StepLaunch
// call would fail on an unknown flag.
//
// The command resolves its agent from the resolver the root installs (falling
// back to agent.DefaultAgentName), not from a flag, so the journey's agent must
// be that default or the record it replays is keyed under a different agent; the
// step fails rather than drive a command the journey cannot assert.
func StepLaunchCommand(args ...string) Step {
	return Step{Name: "launch " + strings.Join(args, " "), Run: func(c *Context) error {
		if c.Agent != agent.DefaultAgentName {
			return fmt.Errorf(
				"StepLaunchCommand drives a flagless command resolving agent %q, but the journey drives %q",
				agent.DefaultAgentName,
				c.Agent,
			)
		}
		argv := commandArgv(c.Binary, args)
		//testenv:allow the journey launcher runs under the host environment it isolates from
		env := driver.IsolatedEnv(c.Root, c.Home, os.Environ())
		c.Session = driver.MustStartUnderPTY(c.TB, c.ProjectDir(), env, argv...)
		return nil
	}}
}

// commandArgv is the argv StepLaunchCommand builds: the built binary followed by
// the command and its arguments, with no journey flags appended. It is pure so
// the step's arg handling is testable without starting a process.
func commandArgv(binary string, args []string) []string {
	var argv []string
	argv = append(argv, binary)
	return append(argv, args...)
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
	return Step{Name: "seed the recorded learn session", Run: func(c *Context) error {
		if c.Stub == nil {
			return errors.New("no stub provider: StepSeedStub must run before StepSeedLearnSession")
		}
		identity, err := journeyIdentity(c)
		if err != nil {
			return err
		}
		key := state.Key{Slug: identity.CheckoutSlug, Agent: c.Agent}
		//exhaustruct:ignore // an observation names only the destination the secret finding rests on
		observation := channel.Observation{
			Destinations: []channel.Destination{{
				Host:     stubHost,
				Protocol: "http",
				Success:  true,
				Count:    1,
			}},
		}

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
	return Step{Name: "answer the apply prompts", Run: func(c *Context) error {
		ses, err := session(c)
		if err != nil {
			return err
		}
		budget := driver.ScaleTimeout(driver.ExitSaveTimeout, driver.SmokeTimeoutFactor())
		return answerApplyPrompts(ses, budget)
	}}
}

// answerApplyPrompts writes one Apply answer per prompt until the process exits
// and reports its exit code. It stops on exit rather than on a fixed finding
// count, so an extra buffered answer is harmless and a prompt it has not yet seen
// is still answered. An answer written after the process has exited is not an
// error; one written while it is alive and fails is.
func answerApplyPrompts(ses *driver.Session, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	answer := []byte(applyFindingKey + "\n")
	for !ses.HasExited() {
		if !time.Now().Before(deadline) {
			return fmt.Errorf("the replay did not exit within %s while answering the apply prompts", budget)
		}
		if err := ses.Write(answer); err != nil {
			if ses.HasExited() {
				break
			}
			return fmt.Errorf("write an apply answer: %w", err)
		}
		time.Sleep(driver.QuitPressInterval) //testenv:allow taps a child pty's prompts, which have no in-process signal
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
	return Step{Name: "assert the learn artefact was deleted", Run: func(c *Context) error {
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
	}}
}

// journeyStatePaths is the isolated config-path resolver the seeding steps use:
// the real resolver with only the user-state dir redirected under the journey's
// home. state.RecordLearnSession and channel.Save resolve through
// configpaths.Get(), which a test build installs as a fail-fast double and which
// would otherwise write the developer's real XDG state; the redirect keeps the
// production state and artefact formats while confining every write to the
// journey's isolated tree.
type journeyStatePaths struct {
	configpaths.ConfigPaths

	stateDir string
}

// UserStateDir returns the journey's isolated state root.
func (p journeyStatePaths) UserStateDir() string { return p.stateDir }

// withJourneyStatePaths installs the isolated resolver rooted at home and
// returns the restore. The journeys run one at a time, so the process-global
// swap cannot race another journey.
func withJourneyStatePaths(home string) func() {
	previous := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths { //nolint:reassign // the seeding steps write through production helpers that resolve through this factory
		return journeyStatePaths{
			ConfigPaths: configpaths.NewRealConfigPaths(),
			stateDir:    filepath.Join(home, isolatedStateDir, launcherConfigDirName),
		}
	}
	return func() { configpaths.Get = previous } //nolint:reassign // restore the factory the seeding step replaced
}

// StepAssertScopeWritten asserts want's semantic effects in the per-slug user
// scope the run wrote: the credential's env-var name and hosts in
// env.secret.yaml, and the expected config keys with the ${ENVVAR} placeholder
// in the managed agent config.
func StepAssertScopeWritten(want ScopeWant) Step {
	return Step{Name: "assert the per-slug scope was written", Run: func(c *Context) error {
		dir, err := journeyScopeDir(c)
		if err != nil {
			return err
		}
		if err := assertSecretScope(dir, want); err != nil {
			return err
		}
		return assertManagedConfig(dir, c.Agent, want)
	}}
}

// StepAssertScopeExcludes asserts none of names is present in the per-slug
// scope's managed agent config dir. A host value store — a login credential
// store such as opencode's auth.json — must never be copied into the scope by
// the host seed, and its absence is not covered by the key/placeholder checks.
func StepAssertScopeExcludes(names ...string) Step {
	return Step{Name: "assert the scope excludes " + strings.Join(names, ", "), Run: func(c *Context) error {
		dir, err := journeyScopeDir(c)
		if err != nil {
			return err
		}
		profile, ok := agent.Lookup(c.Agent)
		if !ok {
			return fmt.Errorf("unknown agent %q", c.Agent)
		}
		configDirNames, err := agent.ConfigDirNames(profile)
		if err != nil {
			return fmt.Errorf("agent %s config-dir names: %w", c.Agent, err)
		}
		scope := configpaths.ConfigScope{Kind: configpaths.ScopePerSlugUser, Dir: dir}
		configDir := scope.AgentConfigDir(configDirNames[0])
		for _, name := range names {
			path := filepath.Join(configDir, name)
			_, err := os.Stat(path)
			if err == nil {
				return fmt.Errorf("the per-slug scope copied %s, which the host seed must drop", path)
			}
			if !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("stat %s: %w", path, err)
			}
		}
		return nil
	}}
}

// StepAssertNoProjectScaffolding asserts the run created no `.agents-sandbox`
// directory in the project: a learning run writes the managed layer to the
// per-slug user scope, never the checkout (#252).
func StepAssertNoProjectScaffolding() Step {
	return Step{Name: "assert no project scaffolding", Run: func(c *Context) error {
		scaffold := filepath.Join(c.ProjectDir(), configpaths.ProjectConfigDir)
		_, err := os.Stat(scaffold)
		if err == nil {
			return fmt.Errorf("the run created project scaffolding at %s", scaffold)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", scaffold, err)
		}
		return nil
	}}
}

// StepAssertFirstRunRecorded asserts the run recorded choice's first-run
// decision once in the isolated home's launcher-global RootState, under the
// "<repo slug>/<agent>" key the launcher uses. The menu's learning choices
// record state.FirstRunLearned, and the manual choice records
// state.FirstRunManual. It reads the root state file directly, not
// through state.ReadFirstRun: that resolves the process's real configpaths,
// while the run wrote under the journey's isolated XDG_STATE_HOME.
func StepAssertFirstRunRecorded(choice firstrun.Choice) Step {
	return Step{Name: "assert the first-run decision was recorded", Run: func(c *Context) error {
		want, ok := recordedFirstRunChoice(choice)
		if !ok {
			return fmt.Errorf("first-run choice %q records no RootState decision", choice)
		}
		identity, err := journeyIdentity(c)
		if err != nil {
			return err
		}
		path := firstRunStateFile(c)
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("the run recorded no first-run decision: %s does not exist", path)
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		var document struct {
			FirstRuns map[string]struct {
				Choice string `yaml:"choice"`
			} `yaml:"first_runs"`
		}
		if err := yaml.Unmarshal(data, &document); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		key := identity.RepoSlug + "/" + c.Agent
		record, ok := document.FirstRuns[key]
		if !ok {
			return fmt.Errorf(
				"the run recorded no first-run decision under %q in %s: %v\n%s",
				key,
				path,
				document.FirstRuns,
				data,
			)
		}
		if record.Choice != string(want) {
			return fmt.Errorf("first-run decision %q = %q, want %q", key, record.Choice, want)
		}
		return nil
	}}
}

// recordedFirstRunChoice maps a first-run menu choice to the choice the launcher
// records in RootState: both learning paths record FirstRunLearned, and the
// manual path records FirstRunManual (see firstrun.Outcome.Apply). It reports
// false for a choice no first-run path records.
func recordedFirstRunChoice(choice firstrun.Choice) (state.FirstRunChoice, bool) {
	switch choice {
	case firstrun.ChoiceLearnByUsing, firstrun.ChoiceLearnFromHost:
		return state.FirstRunLearned, true
	case firstrun.ChoiceManual:
		return state.FirstRunManual, true
	default:
		return "", false
	}
}

// firstRunStateFile is the isolated home's launcher-global state file, which
// holds the first-run records. It is not keyed by checkout: the records live at
// the state root (see state.RootState).
func firstRunStateFile(c *Context) string {
	return filepath.Join(c.Home, isolatedStateDir, launcherConfigDirName, "state.yaml")
}

// runNetworkPolicy is the policy a StepLaunch run under this journey already had:
// the profile its spec passes to `--network`, parsed. The journey literal is built
// before a Context exists, so StepAssertNetworkNotWidened takes this as a builder
// and derives the baseline when the step runs. The spec's profile is a validated
// name, so the parse cannot fail; an empty policy (rather than a panic) makes a
// hypothetical failure strict rather than silently lenient.
func runNetworkPolicy(c *Context) network.Policy {
	profiles, err := network.ParseProfiles([]string{c.Spec.NetworkOrDefault()})
	if err != nil {
		return network.Policy{}
	}
	//exhaustruct:ignore // only the profile is the baseline; every other policy field is zero
	return network.Policy{Profiles: profiles}
}

// StepAssertNetworkNotWidened asserts the scope's launcher config names no
// network profile or egress-allow host the run did not already have, so a
// learning session narrowed or kept the posture instead of widening it. baseline
// derives the run's pre-overlay policy from the context at run time, because the
// journey literal predates the Context; runNetworkPolicy is the builder for a run
// StepLaunch started. allowedEgress names the destinations the journey drove: the
// analyser's lock unions the current allow set with the hosts the session used
// (analyser.SessionFacts.networkFinding), so the journey's own destination must
// not read as a widening.
func StepAssertNetworkNotWidened(baseline func(*Context) network.Policy, allowedEgress ...string) Step {
	return Step{Name: "assert the network policy was not widened", Run: func(c *Context) error {
		dir, err := journeyScopeDir(c)
		if err != nil {
			return err
		}
		learned, err := scopeNetworkPosture(dir)
		if err != nil {
			return err
		}
		after := baseline(c)
		if widenedBeyond(learned, after, allowedEgress) {
			return fmt.Errorf(
				"the learned scope named a network posture beyond profiles %v / egress-allow %v: profiles %v, egress-allow %v",
				after.Profiles,
				after.EgressAllow,
				learned.profiles,
				learned.egressAllow,
			)
		}
		return nil
	}}
}

// StepAssertSessionCleared asserts the project's state file records no learning
// session, so an owning-process-free replay cleared its record after analysis.
func StepAssertSessionCleared() Step {
	return Step{Name: "assert the learn session record was cleared", Run: func(c *Context) error {
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
	}}
}

// session returns the context's launched session, or an error when no
// StepLaunch ran first. A session-dependent step before the launch is a
// malformed journey, and the runner names the failing step, so returning an
// error keeps it a reported failure rather than a nil dereference.
func session(c *Context) (*driver.Session, error) {
	if c.Session == nil {
		return nil, errors.New("no session: StepLaunch must run before this step")
	}
	return c.Session, nil
}

// writeLine sends line followed by a newline to the session, the form the
// launcher's termio prompts read.
func writeLine(c *Context, line string) error {
	return c.Session.Write([]byte(line + "\n"))
}

// journeyIdentity resolves the checkout identity of the journey's project
// directory, the same way the launcher does, so the slug the scope and state
// paths are keyed by matches the run.
func journeyIdentity(c *Context) (git.Identity, error) {
	dir := c.ProjectDir()
	identity, err := git.ResolveIdentity(dir)
	if err != nil {
		return git.Identity{}, fmt.Errorf("resolve checkout identity for %s: %w", dir, err)
	}
	return identity, nil
}

// journeyScopeDir is the per-slug user config scope the run writes into, keyed
// by the repo slug under the isolated XDG config dir.
func journeyScopeDir(c *Context) (string, error) {
	identity, err := journeyIdentity(c)
	if err != nil {
		return "", err
	}
	return filepath.Join(c.Home, isolatedConfigDir, launcherConfigDirName, identity.RepoSlug), nil
}

// journeyStateFile is the project's state file under the isolated XDG state dir,
// keyed by the checkout slug and agent.
func journeyStateFile(c *Context) (string, error) {
	identity, err := journeyIdentity(c)
	if err != nil {
		return "", err
	}
	return filepath.Join(
		c.Home, isolatedStateDir, launcherConfigDirName, identity.CheckoutSlug, c.Agent, "state.yaml",
	), nil
}

// assertSecretScope checks env.secret.yaml in dir for the wanted env-var name
// and host scoping.
func assertSecretScope(dir string, want ScopeWant) error {
	if want.EnvVar == "" && len(want.Hosts) == 0 {
		return nil
	}
	scope := configpaths.ConfigScope{Kind: configpaths.ScopePerSlugUser, Dir: dir}
	path := scope.EnvSecretYAMLFile()
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	text := string(data)
	if want.EnvVar != "" && !strings.Contains(text, want.EnvVar) {
		return fmt.Errorf("env.secret.yaml does not declare %s:\n%s", want.EnvVar, text)
	}
	for _, host := range want.Hosts {
		if !strings.Contains(text, host) {
			return fmt.Errorf("env.secret.yaml does not scope %s to host %q:\n%s", want.EnvVar, host, text)
		}
	}
	return nil
}

// assertManagedConfig checks the managed agent config in dir for the wanted
// config keys and the ${ENVVAR} placeholder.
func assertManagedConfig(dir, agentName string, want ScopeWant) error {
	if len(want.ConfigKeys) == 0 && !want.Placeholder {
		return nil
	}
	profile, ok := agent.Lookup(agentName)
	if !ok {
		return fmt.Errorf("unknown agent %q", agentName)
	}
	names, err := agent.ConfigDirNames(profile)
	if err != nil {
		return fmt.Errorf("agent %s config-dir names: %w", agentName, err)
	}
	scope := configpaths.ConfigScope{Kind: configpaths.ScopePerSlugUser, Dir: dir}
	configDir := scope.AgentConfigDir(names[0])
	text, err := readRegularFiles(configDir)
	if err != nil {
		return err
	}
	for _, key := range want.ConfigKeys {
		if !strings.Contains(text, key) {
			return fmt.Errorf("the managed agent config in %s does not carry key %q:\n%s", configDir, key, text)
		}
	}
	if want.Placeholder {
		if want.EnvVar == "" {
			return errors.New("a placeholder assertion needs the credential's env-var name")
		}
		placeholder := "${" + want.EnvVar + "}"
		if !strings.Contains(text, placeholder) {
			return fmt.Errorf("the managed agent config in %s does not carry %s:\n%s", configDir, placeholder, text)
		}
	}
	return nil
}

// readRegularFiles concatenates every regular file directly under dir. A
// missing directory reports the empty string, so absence of a managed config is
// reported by the key/placeholder assertions rather than as an I/O error.
func readRegularFiles(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", dir, err)
	}
	var builder strings.Builder
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return "", fmt.Errorf("read %s: %w", entry.Name(), err)
		}
		builder.Write(data)
	}
	return builder.String(), nil
}

// networkPosture is the network policy a scope names: the profile(s) and the
// egress-allow list, the two keys the learn-mode lock writes.
type networkPosture struct {
	profiles    []network.Profile
	egressAllow []string
}

// scopeNetworkPosture reads the network posture the scope's launcher config
// names, or the zero posture when the scope wrote no config or no network block.
func scopeNetworkPosture(dir string) (networkPosture, error) {
	path, _, found, err := configdecode.FindFile(dir)
	if err != nil {
		return networkPosture{}, err
	}
	if !found {
		return networkPosture{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return networkPosture{}, fmt.Errorf("read %s: %w", path, err)
	}
	settings, err := viperconfig.ParseLayerSettings(path, data)
	if err != nil {
		return networkPosture{}, fmt.Errorf("parse %s: %w", path, err)
	}
	raw, _ := settings["network"].(map[string]any)
	if raw == nil {
		return networkPosture{}, nil
	}
	profiles, err := profilesFromSetting(raw["profile"])
	if err != nil {
		return networkPosture{}, err
	}
	egressAllow, err := stringsFromSetting(raw["egress-allow"])
	if err != nil {
		return networkPosture{}, err
	}
	return networkPosture{profiles: profiles, egressAllow: egressAllow}, nil
}

// profilesFromSetting decodes a network.profile setting, which the schema
// accepts as a single name, a comma-separated string, or a list.
func profilesFromSetting(value any) ([]network.Profile, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case string:
		return network.ParseProfiles([]string{typed})
	case []string:
		return network.ParseProfiles(typed)
	case []any:
		values := make([]string, 0, len(typed))
		for _, entry := range typed {
			name, ok := entry.(string)
			if !ok {
				return nil, fmt.Errorf("network.profile entry %v is not a string", entry)
			}
			values = append(values, name)
		}
		return network.ParseProfiles(values)
	default:
		return nil, fmt.Errorf("network.profile has unexpected type %T", value)
	}
}

// stringsFromSetting decodes a config-file string-list setting the schema
// accepts as a single string or a list of strings, such as network.egress-allow.
func stringsFromSetting(value any) ([]string, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil, nil
		}
		return []string{strings.TrimSpace(typed)}, nil
	case []string:
		return typed, nil
	case []any:
		values := make([]string, 0, len(typed))
		for _, entry := range typed {
			name, ok := entry.(string)
			if !ok {
				return nil, fmt.Errorf("network.egress-allow entry %v is not a string", entry)
			}
			values = append(values, name)
		}
		return values, nil
	default:
		return nil, fmt.Errorf("network.egress-allow has unexpected type %T", value)
	}
}

// widenedBeyond reports whether learned names a profile or egress-allow entry
// the baseline did not already allow (beyond allowedEgress, the destinations the
// journey drove), so a learned scope that kept or narrowed the posture passes.
//
// The terminal none profile is a narrowing whatever the baseline: network.
// checkProfileCombination makes it exclusive, so replacing any profile with none
// can only shrink egress. That is exactly what the analyser writes when it
// narrows a broad profile (analyser.ConfigFacts.broadProfileFinding), so the
// attribution journey's narrowing must pass here even when the run started on
// public/private.
//
// The egress-allow list is the lock's other half: under a profile that admitted
// broad egress the analyser writes none plus the destinations the session used
// (analyser.SessionFacts.networkFinding unions the current allow set with the
// observed-used hosts), so the guard admits entries the baseline allowed or the
// journey drove, and flags any other host.
func widenedBeyond(learned networkPosture, baseline network.Policy, allowedEgress []string) bool {
	allowedProfiles := make(map[network.Profile]bool, len(baseline.Profiles))
	for _, profile := range baseline.Profiles {
		allowedProfiles[profile] = true
	}
	for _, profile := range learned.profiles {
		if profile == network.ProfileNone {
			continue
		}
		if !allowedProfiles[profile] {
			return true
		}
	}

	allowedHosts := make(map[string]bool)
	for _, host := range baseline.EgressAllow {
		allowedHosts[host] = true
	}
	for _, host := range allowedEgress {
		allowedHosts[host] = true
	}
	for _, host := range learned.egressAllow {
		if !allowedHosts[host] {
			return true
		}
	}
	return false
}
