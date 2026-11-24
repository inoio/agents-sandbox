package journeys

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// guestWorkspace is where the project is bound inside the VM.
const guestWorkspace = "/workspace"

// experimentalNoticeMarker is the part of the launcher's experimental-harness
// notice a journey looks for.
const experimentalNoticeMarker = "is experimental"

// reconfigPromptMarker is the part every launcher reconfig prompt prints
// ("Applying requires rebuilding the VM…" / "…restarting the server…").
const reconfigPromptMarker = "Applying requires"

// journeyEnvMarker is the variable StepConfigureLauncherEnv always writes, so
// the env file exists (and the launcher counts the home as configured) for a
// harness whose adapter needs no variables.
const journeyEnvMarker = "AGENTS_SANDBOX_JOURNEY"

// StepConfigureLauncherEnv configures the launcher with the adapter's stub
// settings: the env file with Stub.Env (always written, with a marker), and the
// agent-config snippets: the startup smoke's (driver.Spec.Seeds) with the
// adapter's Stub.Snippets on top. The home then counts as configured,
// so the launcher skips the first-run menu, and a harness configured through
// its environment or merged config reaches the stub.
func StepConfigureLauncherEnv() Step {
	return Step{Name: "the launcher is configured with the harness's stub settings", Run: func(c *Context) error {
		if c.Stub == nil {
			return errors.New("no stub provider on the context; seed it first")
		}
		vars := map[string]string{journeyEnvMarker: "1"}
		if c.Harness.Stub.Env != nil {
			maps.Copy(vars, c.Harness.Stub.Env(c.Stub.Addr()))
		}
		if err := driver.SeedLauncherEnv(c.Home, vars); err != nil {
			return err
		}
		snippets := driver.Seeds{AgentSnippets: c.Spec.Seeds.AgentSnippets}
		if c.Harness.Stub.Snippets != nil {
			snippets = snippets.Merge(driver.Seeds{AgentSnippets: c.Harness.Stub.Snippets(c.Stub.Addr())})
		}
		if len(snippets.AgentSnippets) == 0 {
			return nil
		}
		profile, ok := agent.Lookup(c.Agent)
		if !ok {
			return fmt.Errorf("unknown agent %q", c.Agent)
		}
		return driver.SeedAgentSnippets(c.Home, profile.ConfigDirName(), snippets.AgentSnippets)
	}}
}

// StepConfigureLauncher writes the launcher env file with only the journey
// marker, so the home counts as configured and the launcher skips the
// first-run menu, for a journey that needs no stub.
func StepConfigureLauncher() Step {
	return Step{Name: "the launcher is configured", Run: func(c *Context) error {
		return driver.SeedLauncherEnv(c.Home, map[string]string{journeyEnvMarker: "1"})
	}}
}

// StepScriptWriteFile makes the stub answer the next turn with the harness's
// tool call that writes content to name in the project, through the adapter's
// WriteFileCall.
func StepScriptWriteFile(name, content string) Step {
	return Step{Name: "the stub answers the next turn by writing " + strconv.Quote(name), Run: func(c *Context) error {
		if c.Stub == nil {
			return errors.New("no stub provider on the context; seed it first")
		}
		if c.Harness.Drive.WriteFileCall == nil {
			return fmt.Errorf("the %s adapter declares no WriteFileCall", c.Agent)
		}
		c.Stub.ScriptToolCall(c.Harness.Drive.WriteFileCall(path.Join(guestWorkspace, name), content))
		return nil
	}}
}

// StepAssertProjectFile waits until the harness wrote name in the project on
// the host with content (trailing whitespace ignored), owned by the host user.
func StepAssertProjectFile(name, content string) Step {
	return stepAssertProjectFile(name, content, scaled(stubTurnTimeout))
}

func stepAssertProjectFile(name, content string, budget time.Duration) Step {
	return AsCheck(Step{
		Name: strconv.Quote(name) + " is in the project on the host, owned by the host user",
		Run: func(c *Context) error {
			filePath := filepath.Join(c.ProjectDir(), name)
			return pollUntil(budget, stubPollInterval, func() (bool, error) {
				return projectFileProblem(filePath, content) == nil, nil
			}, func() error {
				return projectFileProblem(filePath, content)
			})
		},
	})
}

// projectFileProblem reports why filePath is not a file holding content
// (trailing whitespace ignored) owned by the current user, or nil.
func projectFileProblem(filePath, content string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("%s is not readable on the host: %w", filePath, err)
	}
	if got := strings.TrimRight(string(data), " \t\r\n"); got != content {
		return fmt.Errorf("%s holds %q, want %q", filePath, got, content)
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", filePath, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s has no owner information", filePath)
	}
	//testenv:allow the host user owning the file is the assertion
	if uid := os.Getuid(); int(stat.Uid) != uid {
		return fmt.Errorf("%s is owned by uid %d, want the host user %d", filePath, stat.Uid, uid)
	}
	return nil
}

// StepAssertExperimentalNotice checks the launcher printed the experimental
// notice exactly when the harness's profile is experimental.
func StepAssertExperimentalNotice() Step {
	return AsCheck(Step{Name: "the experimental notice matches the harness's profile", Run: func(c *Context) error {
		ses, err := session(c)
		if err != nil {
			return err
		}
		return experimentalNoticeProblem(c.Agent, driver.StripANSI(ses.Output()))
	}})
}

// experimentalNoticeProblem reports a notice that output shows for a stable
// agent or lacks for an experimental one, or nil.
func experimentalNoticeProblem(agentName, output string) error {
	profile, ok := agent.Lookup(agentName)
	if !ok {
		return fmt.Errorf("unknown agent %q", agentName)
	}
	shown := strings.Contains(output, agentName+" "+experimentalNoticeMarker)
	switch {
	case profile.Experimental() && !shown:
		return fmt.Errorf("%s is experimental but the launcher printed no notice", agentName)
	case !profile.Experimental() && shown:
		return fmt.Errorf("%s is not experimental but the launcher printed the notice", agentName)
	}
	return nil
}

// StepAttachClient starts another client of the same project while the active
// one keeps running, and makes it the active client.
func StepAttachClient() Step {
	return Step{Name: "another client runs `agents-sandbox run` on the same project", Run: func(c *Context) error {
		if len(c.Clients) == 0 {
			return errors.New("no first client to attach next to")
		}
		launchClient(c, []string{runCommand}, nil)
		return nil
	}}
}

// StepUseClient makes the journey's number-th client (1-based) the active one.
func StepUseClient(number int) Step {
	return Step{Name: "the user switches to client " + strconv.Itoa(number), Run: func(c *Context) error {
		if number < 1 || number > len(c.Clients) {
			return fmt.Errorf("client %d does not exist; the journey started %d", number, len(c.Clients))
		}
		c.Session = c.Clients[number-1]
		return nil
	}}
}

// StepAssertNoReconfigPrompt checks the active client was not asked to rebuild
// the VM or restart the server. A reconfig prompt blocks before the TUI, so in a
// journey the readiness step before this check fails first; its failure report
// carries the harness output with the prompt.
func StepAssertNoReconfigPrompt() Step {
	return AsCheck(Step{Name: "no reconfig prompt appears", Run: func(c *Context) error {
		ses, err := session(c)
		if err != nil {
			return err
		}
		return reconfigPromptProblem(driver.MarkerText(string(ses.Output())))
	}})
}

// reconfigPromptProblem reports a reconfig prompt in output, or nil.
func reconfigPromptProblem(output string) error {
	if strings.Contains(driver.MarkerText(output), driver.MarkerText(reconfigPromptMarker)) {
		return errors.New("the launcher asked to apply a reconfig")
	}
	return nil
}

// vmStartedMarker is what the launcher prints when it boots a stopped project
// VM again.
const vmStartedMarker = "started existing project VM"

// vmStopTimeout bounds `agents-sandbox stop`; it is a ceiling, not the
// expected wait.
const vmStopTimeout = 2 * time.Minute

// StepStopVM stops the journey's project VM with `agents-sandbox stop`, so the
// next run boots it again and only state the home volume kept survives.
func StepStopVM() Step {
	return Step{Name: "the user stops the project VM", Run: func(c *Context) error {
		ctx, cancel := context.WithTimeout(
			context.Background(),
			scaled(vmStopTimeout),
		)
		defer cancel()
		stop := exec.CommandContext(ctx, c.Binary, "stop", "--agent", c.Agent)
		stop.Dir = c.ProjectDir()
		//testenv:allow the journey launcher runs under the host environment it isolates from
		stop.Env = driver.IsolatedEnv(c.Root, c.Home, os.Environ())
		if out, err := stop.CombinedOutput(); err != nil {
			return fmt.Errorf("agents-sandbox stop: %w\n%s", err, out)
		}
		return nil
	}}
}

// StepAssertVMRestarted checks the active client's launcher booted the stopped
// project VM again rather than attaching to a running one.
func StepAssertVMRestarted() Step {
	return AsCheck(Step{Name: "the launcher boots the stopped VM again", Run: func(c *Context) error {
		ses, err := session(c)
		if err != nil {
			return err
		}
		return vmRestartProblem(driver.StripANSI(ses.Output()))
	}})
}

// vmRestartProblem reports output that does not show the VM booting again.
func vmRestartProblem(output string) error {
	if !strings.Contains(output, vmStartedMarker) {
		return errors.New("the launcher did not boot the stopped VM; it attached to a running one or created a new one")
	}
	return nil
}

// StepRelaunchWithResume starts the harness again with its resume arguments
// (the adapter's ResumeArgs) as the active client.
func StepRelaunchWithResume() Step {
	return Step{Name: "the user runs the harness again, resuming the last session", Run: func(c *Context) error {
		if len(c.Harness.Drive.ResumeArgs) == 0 {
			return fmt.Errorf("the %s adapter declares no ResumeArgs", c.Agent)
		}
		launchClient(c, []string{runCommand}, c.Harness.Drive.ResumeArgs)
		return nil
	}}
}

// StepAssertSessionRestored checks the active client shows prompt, a message of
// the resumed session. It matches the client's whole output, which holds only
// because the relaunched client's pty is fresh and a harness's start screen
// does not list earlier prompts.
func StepAssertSessionRestored(prompt string) Step {
	return AsCheck(Step{Name: "the resumed session shows " + strconv.Quote(prompt), Run: func(c *Context) error {
		ses, err := session(c)
		if err != nil {
			return err
		}
		return ses.WaitForMarker(prompt, scaled(stubTurnTimeout))
	}})
}
