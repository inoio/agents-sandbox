package journeys

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v4"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configdecode"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/firstrun"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
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

// The isolated XDG tree driver.IsolatedEnv roots under a journey's home base:
// the launcher's config lives under config/agents-sandbox and its project state
// under state/agents-sandbox. The names mirror driver.env.go's private
// constants, which the journeys package cannot import.
const (
	isolatedConfigDir     = "config"
	isolatedStateDir      = "state"
	launcherConfigDirName = "agents-sandbox"
)

// StepFreshHome prepares a journey's isolated HOME, so it starts with no host
// config and no managed layer: no first-run decision exists and the menu is
// offered.
func StepFreshHome() Step {
	return Step{Name: "fresh isolated home", Run: func(c *Context) error {
		return driver.PrepareHome(c.Home)
	}}
}

// StepSeedHostConfig writes the given home-relative files under the journey's
// isolated HOME, the way an agent's own host config would be present: the
// first-run decision sees host setup and offers "Learn from my host setup".
func StepSeedHostConfig(files map[string]string) Step {
	return Step{Name: "seed host agent config", Run: func(c *Context) error {
		return driver.SeedHomeFiles(c.Home, files)
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
		c.Session = driver.MustStartUnderPTY(c.TB, driver.ProjectDir(c.Root, c.Agent), env, argv...)
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

// StepSendPrompt types prompt into the harness's editor and presses Enter.
func StepSendPrompt(prompt string) Step {
	return Step{Name: "send prompt", Run: func(c *Context) error {
		ses, err := session(c)
		if err != nil {
			return err
		}
		return ses.Write([]byte(prompt + "\r"))
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

// StepAssertNoProjectScaffolding asserts the run created no `.agents-sandbox`
// directory in the project: a learning run writes the managed layer to the
// per-slug user scope, never the checkout (#252).
func StepAssertNoProjectScaffolding() Step {
	return Step{Name: "assert no project scaffolding", Run: func(c *Context) error {
		scaffold := filepath.Join(driver.ProjectDir(c.Root, c.Agent), configpaths.ProjectConfigDir)
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

// StepAssertNetworkNotWidened asserts the scope's launcher config names no
// network profile the run did not already have, so a learning session narrowed
// or kept the posture instead of widening it.
func StepAssertNetworkNotWidened(after network.Policy) Step {
	return Step{Name: "assert the network policy was not widened", Run: func(c *Context) error {
		dir, err := journeyScopeDir(c)
		if err != nil {
			return err
		}
		profiles, err := scopeNetworkProfiles(dir)
		if err != nil {
			return err
		}
		if widenedBeyond(profiles, after) {
			return fmt.Errorf("the learned scope named network profile(s) %v beyond %v", profiles, after.Profiles)
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
	dir := driver.ProjectDir(c.Root, c.Agent)
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

// scopeNetworkProfiles reads the network profile(s) the scope's launcher config
// names, or none when the scope wrote no config or no network block.
func scopeNetworkProfiles(dir string) ([]network.Profile, error) {
	path, _, found, err := configdecode.FindFile(dir)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	settings, err := viperconfig.ParseLayerSettings(path, data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	raw, _ := settings["network"].(map[string]any)
	if raw == nil {
		return nil, nil
	}
	return profilesFromSetting(raw["profile"])
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

// widenedBeyond reports whether profiles names a profile after does not already
// allow, so a learned scope that kept or narrowed the posture passes.
//
// The terminal none profile is a narrowing whatever the baseline: network.
// checkProfileCombination makes it exclusive, so replacing any profile with none
// can only shrink egress. That is exactly what the analyser writes when it
// narrows a broad profile (analyser.ConfigFacts.broadProfileFinding), so the
// attribution journey's narrowing must pass here even when the run started on
// public/private.
func widenedBeyond(profiles []network.Profile, after network.Policy) bool {
	allowed := make(map[network.Profile]bool, len(after.Profiles))
	for _, profile := range after.Profiles {
		allowed[profile] = true
	}
	for _, profile := range profiles {
		if profile == network.ProfileNone {
			continue
		}
		if !allowed[profile] {
			return true
		}
	}
	return false
}
