package journeys

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/inoio/agents-sandbox/test/msb/driver"
)

const renewingLoginStore = `{"tokens":{"refresh_token":"journeys-renewing-login"}}`

// StepAssertRenewingLoginPreserved replaces the static store with a renewing
// login, then provisions again through a second shell client.
func StepAssertRenewingLoginPreserved() Step {
	return AsCheck(Step{Name: "a later renewing login survives provisioning", Run: func(c *Context) error {
		store := exposedLoginKeys[c.Agent]
		if !store.provisioned {
			return nil
		}
		command := "printf '%s\\n' '" + renewingLoginStore + "' > ~/" + store.path
		code, session, err := runInShell(c, command)
		if err != nil || code != 0 {
			return fmt.Errorf("seed the renewing login (exit %d): %w\n%s", code, err, session.OutputTail())
		}
		code, session, err = runInShell(c, "cat ~/"+store.path)
		if err != nil || code != 0 {
			return fmt.Errorf("read the renewing login (exit %d): %w", code, err)
		}
		if !strings.Contains(driver.StripANSI(session.Output()), renewingLoginStore) {
			return fmt.Errorf("provisioning replaced the renewing login:\n%s", session.OutputTail())
		}
		return nil
	}})
}

// StepResetProvisionedLoginHome resets the journey's isolated home volume so
// the next shell must recover its static login store from the host scope.
func StepResetProvisionedLoginHome() Step {
	return Step{Name: "the user resets the provisioned login's home volume", Run: func(c *Context) error {
		if !exposedLoginKeys[c.Agent].provisioned {
			return nil
		}
		if err := StepStopVM().Run(c); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), scaled(vmStopTimeout))
		defer cancel()
		command := exec.CommandContext(ctx, c.Binary, "volume", "reset", "--rm", "--agent", c.Agent)
		command.Dir = c.ProjectDir()
		//testenv:allow the journey launcher runs under the host environment it isolates from
		command.Env = driver.IsolatedEnv(c.Root, c.Home, os.Environ())
		if output, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("reset the journey home: %w\n%s", err, output)
		}
		return nil
	}}
}
