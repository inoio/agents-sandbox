package reprovision

import (
	"fmt"

	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

const (
	keepKey    = "k"
	quitKey    = "q"
	restartKey = "r"
)

// PromptA handles the VM recreation prompt when there are active sessions.
func PromptA(ui termio.UI, changes []Change, n int) (string, error) {
	prompt := fmt.Sprintf(
		"%s\nApplying requires rebuilding the VM, which would disconnect %d active session(s).",
		configChangeList(changes),
		n,
	)
	return ui.Select(prompt, []termio.Choice{
		//nolint:exhaustruct // brief-specified wording
		{Key: keepKey, Label: "Keep current VM (defer this change)"},
		//nolint:exhaustruct // brief-specified wording
		{Key: quitKey, Label: "Quit (allow finalization of other sessions)"},
	}, keepKey)
}

// PromptB handles the daemon restart prompt when there are active clients.
func PromptB(ui termio.UI, changes []Change, n int) (string, error) {
	prompt := fmt.Sprintf(
		"%s\nApplying requires restarting the server; %d active client(s) should reconnect automatically.",
		configChangeList(changes),
		n,
	)
	return ui.Select(prompt, []termio.Choice{
		//nolint:exhaustruct // brief-specified wording
		{Key: keepKey, Label: "Keep current server (defer this change)"},
		//nolint:exhaustruct // brief-specified wording
		{Key: quitKey, Label: "Quit (leave server running; apply later)"},
		//nolint:exhaustruct // brief-specified wording
		{Key: restartKey, Label: "Restart server (apply changes)"},
	}, keepKey)
}
