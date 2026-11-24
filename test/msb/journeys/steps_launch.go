package journeys

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// StepOwnProject moves the journey onto a project of its own, named
// <agent>-<label>, and builds the agent's runner image there: the journey then
// gets its own project slug, VM, and home volume instead of the agent's shared
// project, so a VM another journey left running cannot leak into it.
func StepOwnProject(label string) Step {
	return Step{Name: "the journey runs on a project of its own (" + label + ")", Run: func(c *Context) error {
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
	return Step{Name: "a fresh isolated home", Run: func(c *Context) error {
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
func StepSeedHostConfig(build func(*Context) (map[string]string, error)) Step {
	return Step{Name: "the host holds the harness's own config", Run: func(c *Context) error {
		files, err := build(c)
		if err != nil {
			return err
		}
		return driver.SeedHomeFiles(c.Home, files)
	}}
}

// StepSeedStub starts the journey's host-side stub provider, storing it on the
// context, and seeds the VM home: the startup smoke's `home:` manifest for the
// harness (driver.Spec.Seeds.HomeManifest) with the adapter's stub files added.
// The smoke's host files are not seeded: the first-run menu reads host files as
// host setup, and a journey's host setup is its own (StepSeedHostConfig). The
// smoke's agent snippets wait for StepConfigureLauncherEnv: a snippet is a
// managed layer, and a fresh-home journey must still get the first-run menu.
// The listener is closed by StartStubProvider's tb.Cleanup.
func StepSeedStub() Step {
	return Step{Name: "the stub provider is provisioned into the VM", Run: func(c *Context) error {
		provider, err := StartStubProvider(c.TB)
		if err != nil {
			return err
		}
		c.Stub = provider
		seeds := driver.Seeds{HomeManifest: c.Spec.Seeds.HomeManifest}
		if c.Harness.Stub.HomeFiles != nil {
			seeds = seeds.Merge(driver.Seeds{HomeManifest: c.Harness.Stub.HomeFiles(provider.Addr())})
		}
		if err := seeds.Apply(c.Home, ""); err != nil {
			return fmt.Errorf("seed the home: %w", err)
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
	return Step{Name: "the user runs `agents-sandbox " + strings.Join(args, " ") + "`", Run: func(c *Context) error {
		launchClient(c, args, nil)
		return nil
	}}
}

// launchClient starts the launcher with launchArgv's arguments, makes the
// session the active client, and adds it to the journey's clients.
func launchClient(c *Context, launcherArgs, agentArgs []string) {
	argv := launchArgv(c, launcherArgs, agentArgs)
	//testenv:allow the journey launcher runs under the host environment it isolates from
	env := driver.IsolatedEnv(c.Root, c.Home, os.Environ())
	c.Session = driver.MustStartUnderPTY(c.TB, c.ProjectDir(), env, argv...)
	c.Clients = append(c.Clients, c.Session)
}

// runCommand is the launcher command that starts the agent.
const runCommand = "run"

// launchArgv is the launcher invocation: launcherArgs, the journey's agent,
// network, memory, and CPU flags, and after `--` the agent arguments. For a
// `run` these are the adapter's LaunchArgs followed by agentArgs; another
// command does not start the agent, so it gets only agentArgs.
func launchArgv(c *Context, launcherArgs, agentArgs []string) []string {
	argv := make([]string, 0, len(launcherArgs)+len(agentArgs)+10)
	argv = append(argv, c.Binary)
	argv = append(argv, launcherArgs...)
	argv = append(argv, "--agent", c.Agent)
	argv = append(argv, "--network", c.Spec.NetworkOrDefault())
	argv = append(argv, "--memory", driver.SmokeMemorySpec(), "--no-rebuild")
	argv = driver.WithVMCPUs(argv, driver.SmokeVMCPUs())
	if len(launcherArgs) > 0 && launcherArgs[0] == runCommand {
		agentArgs = append(append([]string(nil), c.Harness.Drive.LaunchArgs...), agentArgs...)
	}
	if len(agentArgs) > 0 {
		argv = append(append(argv, "--"), agentArgs...)
	}
	return argv
}

// StepWaitReady blocks until the harness reaches its TUI, using the harness
// table's readiness signal.
func StepWaitReady() Step {
	return Step{Name: "the harness TUI comes up", Run: func(c *Context) error {
		ses, err := session(c)
		if err != nil {
			return err
		}
		return driver.WaitReady(ses, c.Spec)
	}}
}

// setupScreenSettle is how long StepExitHarnessSetup lets the output settle
// after the readiness marker showed first, in case the setup screen is painted
// just after the editor behind it.
const setupScreenSettle = time.Second

// StepExitHarnessSetup leaves the harness's own first-start setup screen
// (Driving.Setup), as a user on a fresh install would. It waits for that screen
// or the harness's readiness marker; when the readiness marker shows first, it
// lets the output settle and leaves the setup only if the screen came up after
// all, so it does nothing for a harness that skips its setup (the seeded host
// config turns it off) or declares none. After the exit keys it waits for the
// readiness marker in the output written since, so a status line painted
// behind the setup screen does not count.
func StepExitHarnessSetup() Step {
	return Step{Name: "the user leaves the harness's own setup, if it shows one", Run: func(c *Context) error {
		setup := c.Harness.Drive.Setup
		if setup == nil {
			return nil
		}
		ses, err := session(c)
		if err != nil {
			return err
		}
		timeout := driver.ScaleTimeout(c.Spec.ReadyTimeout(), driver.SmokeTimeoutFactor())
		first, err := waitForFirstMarker(ses, []string{setup.Marker, c.Spec.ReadyMarker}, 0, timeout)
		if err != nil {
			return err
		}
		if first != 0 {
			//testenv:allow letting a setup screen painted after the editor show up
			time.Sleep(setupScreenSettle)
			if firstShownMarker(string(ses.Output()), []string{setup.Marker}) < 0 {
				return nil
			}
		}
		sent := len(ses.Output())
		if err = ses.Write(setup.ExitKeys); err != nil {
			return err
		}
		_, err = waitForFirstMarker(ses, []string{c.Spec.ReadyMarker}, sent, timeout)
		return err
	}}
}

// waitForFirstMarker waits until the session's output from offset on shows one
// of markers and returns the index of the first one listed that it shows.
func waitForFirstMarker(ses *driver.Session, markers []string, offset int, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for {
		if index := firstShownMarker(string(ses.Output()[offset:]), markers); index >= 0 {
			return index, nil
		}
		if ses.HasExited() {
			return -1, fmt.Errorf("the session exited before any of %q appeared (%s); output: %s",
				markers, ses.ExitStatus(), ses.OutputTail())
		}
		if !time.Now().Before(deadline) {
			return -1, fmt.Errorf("none of %q appeared within %s; output: %s", markers, timeout, ses.OutputTail())
		}
		//testenv:allow polling the harness output for its first screen
		time.Sleep(stubPollInterval)
	}
}

// firstShownMarker returns the index of the first of markers that output shows
// (compared as driver.MarkerText), or -1. An empty marker never matches.
func firstShownMarker(output string, markers []string) int {
	text := driver.MarkerText(output)
	for index, marker := range markers {
		if needle := driver.MarkerText(marker); needle != "" && strings.Contains(text, needle) {
			return index
		}
	}
	return -1
}

// StepSendPrompt types prompt into the harness's editor, retyping it until the
// harness echoes it (see typeUntilEchoed), and presses Enter.
func StepSendPrompt(prompt string) Step {
	return Step{Name: "the user sends the prompt " + strconv.Quote(prompt), Run: func(c *Context) error {
		ses, err := session(c)
		if err != nil {
			return err
		}
		c.markPrompt(ses)
		echoWait := driver.ScaleTimeout(promptEchoWait, driver.SmokeTimeoutFactor())
		if err := typeUntilEchoed(ses, prompt, promptTypeAttempts, echoWait); err != nil {
			return err
		}
		return ses.Write([]byte("\r"))
	}}
}

// stubPollInterval is how often StepAssertStubServed checks the stub's log.
const stubPollInterval = 100 * time.Millisecond

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
			//testenv:allow polling a live harness on a wall-clock interval
			time.Sleep(stubPollInterval)
		}
		c.TB.Logf("the stub served %v", c.Stub.Requests())
		return nil
	}}
}

// StepAssertReplyShown waits until the stub sent a new reply since the active
// client's last prompt and the client shows it after that prompt, so the turn
// completed rather than only reaching the stub, and a repainted earlier reply
// cannot pass for it.
func StepAssertReplyShown() Step {
	return AsCheck(Step{Name: "the harness shows the stub's reply", Run: func(c *Context) error {
		ses, err := session(c)
		if err != nil {
			return err
		}
		if c.Stub == nil {
			return errors.New("no stub provider on the context; seed it first")
		}
		budget := driver.ScaleTimeout(stubTurnTimeout, driver.SmokeTimeoutFactor())
		reply, err := newReplySince(c.Stub, c.lastPrompt(ses).replies, budget)
		if err != nil {
			return err
		}
		if !waitForEcho(ses, c.lastPrompt(ses).offset, driver.MarkerText(reply), budget) {
			return fmt.Errorf("the harness did not show the stub's reply %q within %s", reply, budget)
		}
		return nil
	}})
}

// newReplySince waits until stub has sent more than seen replies and returns
// the newest one.
func newReplySince(stub StubProvider, seen int, budget time.Duration) (string, error) {
	deadline := time.Now().Add(budget)
	for {
		if replies := stub.Replies(); len(replies) > seen {
			return replies[len(replies)-1], nil
		}
		if !time.Now().Before(deadline) {
			return "", fmt.Errorf(
				"the stub sent no reply for the prompt within %s (requests: %v)",
				budget,
				stub.Requests(),
			)
		}
		//testenv:allow polling a live harness on a wall-clock interval
		time.Sleep(stubPollInterval)
	}
}

// StepQuitToExit leaves the harness with its quit key and waits for the
// launcher to exit 0: the quit for a run with no learn exit prompt.
func StepQuitToExit() Step {
	return Step{Name: "the user quits the harness and the launcher exits 0", Run: func(c *Context) error {
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

// StepLaunchCommand starts the built launcher under the journey's isolated
// environment with the project directory as its CWD and stores the session, like
// StepLaunch but appending no journey flags. It drives a subcommand that takes
// none of the run flags StepLaunch adds (--agent, --network, --memory,
// --no-rebuild, the CPU cap): `config learn` declares no flags, so a StepLaunch
// call would fail on an unknown flag. Such a command resolves its agent from the
// launcher config, so the step names the journey's agent there through
// AGENTS_SANDBOX_AGENT (commandEnv).
func StepLaunchCommand(args ...string) Step {
	return Step{Name: "the user runs `agents-sandbox " + strings.Join(args, " ") + "`", Run: func(c *Context) error {
		//testenv:allow the journey launcher runs under the host environment it isolates from
		env := commandEnv(c, os.Environ())
		c.Session = driver.MustStartUnderPTY(c.TB, c.ProjectDir(), env, commandArgv(c.Binary, args)...)
		return nil
	}}
}

// agentEnv is the launcher's environment variable for the `agent` config key.
const agentEnv = "AGENTS_SANDBOX_AGENT"

// commandEnv is the isolated environment for a flagless command, naming the
// journey's agent through the launcher's agent variable.
func commandEnv(c *Context, inherited []string) []string {
	return append(driver.IsolatedEnv(c.Root, c.Home, inherited), agentEnv+"="+c.Agent)
}

// commandArgv is the argv StepLaunchCommand builds: the built binary followed by
// the command and its arguments, with no journey flags appended. It is pure so
// the step's arg handling is testable without starting a process.
func commandArgv(binary string, args []string) []string {
	var argv []string
	argv = append(argv, binary)
	return append(argv, args...)
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
