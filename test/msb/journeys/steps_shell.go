package journeys

import (
	"fmt"
	"time"

	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// keyRetypeInterval is how often typeUntilExit types its input again. The
// launcher's attach takes the terminal only once the VM is up, so input typed
// earlier can be lost.
const keyRetypeInterval = 2 * time.Second

// launchShellCommand waits for a new shell's prompt and submits command once.
// The shell exits with the command's status, which the caller checks after
// completing any interactive exchange.
func launchShellCommand(c *Context, command string) error {
	launchClient(c, []string{"shell"}, nil)
	if err := c.Session.WaitForMarker(shellPromptMarker, scaled(shellPromptTimeout)); err != nil {
		return err
	}
	return c.Session.Write([]byte(command + "; exit\r"))
}

// typeUntilExit types input into ses until the process exits or budget
// elapses. The input must end the process (a shell line ending in exit), so a
// repeat only runs once more when the first was lost.
func typeUntilExit(ses *driver.Session, input []byte, budget time.Duration) error {
	return pollUntil(budget, keyRetypeInterval, func() (bool, error) {
		if ses.HasExited() {
			return true, nil
		}
		if err := ses.Write(input); err != nil {
			return false, fmt.Errorf("type the shell input: %w", err)
		}
		return false, nil
	}, func() error {
		return fmt.Errorf("the shell did not exit within %s; output: %s", budget, ses.OutputTail())
	})
}

// runInShell types command into a new `agents-sandbox shell` client, ending
// the shell with the command's status, and returns that status.
func runInShell(c *Context, command string) (int, *driver.Session, error) {
	launchClient(c, []string{"shell"}, nil)
	ses := c.Session
	budget := scaled(loginExitTimeout)
	if err := typeUntilExit(ses, []byte(command+"; exit\r"), budget); err != nil {
		return 0, ses, err
	}
	code, err := ses.WaitExit(budget)
	return code, ses, err
}

// logHarnessDiagnostics prints the harness's own view of its sessions
// (Driving.Diagnostics) from the journey's VM, read through a shell client, so
// a failure the launcher's output does not explain carries the harness's view
// before the sweep removes the VM. The journey's active client is kept.
func logHarnessDiagnostics(c *Context) {
	command := c.Harness.Drive.Diagnostics
	if command == "" {
		return
	}
	active, clients := c.Session, c.Clients
	defer func() { c.Session, c.Clients = active, clients }()
	code, ses, err := runInShell(c, command)
	c.TB.Logf("%s diagnostics (exit %d, %v):\n%s", c.Agent, code, err, driver.StripANSI(ses.Output()))
}
