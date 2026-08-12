package volume

import "gitlab.inoio.de/inoio/opencode-msb/internal/termio"

// VolumeAction is the user-selected disposition for an existing home volume.
//
//nolint:revive // VolumeAction is the intended name per the remediation plan
type VolumeAction int

const (
	ActionKeep VolumeAction = iota
	ActionMigrate
	ActionReset
	ActionQuit
)

func (a VolumeAction) String() string {
	switch a {
	case ActionKeep:
		return "keep" //nolint:goconst // repeated in String() switch
	case ActionMigrate:
		return "migrate" //nolint:goconst // repeated in String() switch
	case ActionReset:
		return "reset" //nolint:goconst // repeated in String() switch
	case ActionQuit:
		return "quit" //nolint:goconst // repeated in String() switch
	default:
		return "unknown"
	}
}

// FromKey maps the numeric prompt key ("1".."4") to a VolumeAction.
func FromKey(key string) (VolumeAction, error) {
	switch key {
	case "1":
		return ActionKeep, nil
	case "2":
		return ActionMigrate, nil
	case "3":
		return ActionReset, nil
	case "4":
		return ActionQuit, nil
	default:
		return ActionKeep, &invalidKeyError{key}
	}
}

type invalidKeyError struct{ key string }

func (e *invalidKeyError) Error() string { return "invalid action key: " + e.key }

// actionLabel returns a human-friendly label for a home volume action.
func actionLabel(action VolumeAction) string {
	if action == ActionReset {
		return "reset"
	}
	if action == ActionMigrate {
		return "migrate"
	}
	return "keep"
}

// ResolveHomeAction compares the stored image digest with the current one.
// If they match, returns ActionKeep immediately.
// If they differ, presents a prompt: keep/migrate/reset/quit.
// In non-interactive mode or with --yes, defaults to ActionKeep.
func (vm *Manager) ResolveHomeAction(
	ui termio.UI,
	storedDigest, currentDigest string,
) VolumeAction {
	if storedDigest == currentDigest {
		return ActionKeep
	}

	if !ui.IsInteractive() {
		ui.Infof("non-interactive: using existing home volume")
		return ActionKeep
	}

	prompt := "Docker image changed for project. The image's home directory is different from your current one."
	choices := []termio.Choice{
		{Key: "1", Label: "keep", Description: "continue with existing home volume"},
		{Key: "2", Label: "migrate", Description: "create fresh volume, copy all files on top"},
		{
			Key:         "3",
			Label:       "reset",
			Description: "replace with fresh volume from image (lose local changes)",
		},
		{Key: "4", Label: "quit", Description: "exit without starting a session"},
	}
	selected, err := ui.Select(prompt, choices, "1")
	if err != nil {
		ui.Warnf("prompt failed, continuing with existing volume")
		return ActionKeep
	}
	action, _ := FromKey(selected)
	return action
}
