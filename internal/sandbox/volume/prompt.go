package volume

import "gitlab.inoio.de/inoio/opencode-msb/internal/termio"

// Action constants for the home volume resolution prompt.
const (
	actionKeep    = "1"
	actionMigrate = "2"
	actionReset   = "3"
	actionQuit    = "4"

	actionMigrateLabel = "migrate"
	actionResetLabel   = "reset"
)

// actionLabel returns a human-friendly label for a home volume action.
func actionLabel(action string) string {
	if action == actionReset {
		return actionResetLabel
	}
	if action == actionMigrate {
		return actionMigrateLabel
	}
	return "keep"
}

// ResolveHomeAction compares the stored image digest with the current one.
// If they match, returns actionKeep immediately.
// If they differ, presents a prompt: keep/migrate/reset/quit.
// In non-interactive mode or with --yes, defaults to actionKeep.
func (vm *Manager) ResolveHomeAction(
	ui termio.UI,
	storedDigest, currentDigest string,
) string {
	if storedDigest == currentDigest {
		return actionKeep
	}

	if !ui.IsInteractive() {
		ui.Infof("non-interactive: using existing home volume")
		return actionKeep
	}

	prompt := "Docker image changed for project. The image's home directory is different from your current one."
	choices := []termio.Choice{
		{Key: actionKeep, Label: "keep", Description: "continue with existing home volume"},
		{Key: actionMigrate, Label: actionMigrateLabel, Description: "create fresh volume, copy all files on top"},
		{
			Key:         actionReset,
			Label:       actionResetLabel,
			Description: "replace with fresh volume from image (lose local changes)",
		},
		{Key: actionQuit, Label: "quit", Description: "exit without starting a session"},
	}
	selected, err := ui.Select(prompt, choices, actionKeep)
	if err != nil {
		ui.Warnf("prompt failed, continuing with existing volume")
		return actionKeep
	}
	return selected
}
