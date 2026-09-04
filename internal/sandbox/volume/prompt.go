package volume

import "github.com/inoio/agents-sandbox/internal/termio"

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

// FromKey maps the letter prompt key ("k"/"m"/"r"/"q") to a VolumeAction.
func FromKey(key string) (VolumeAction, error) {
	switch key {
	case "k":
		return ActionKeep, nil
	case "m":
		return ActionMigrate, nil
	case "r":
		return ActionReset, nil
	case "q":
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
		ui.Infof("non-interactive: image changed; keeping existing home volume")
		return ActionKeep
	}

	prompt := "Docker image changed for project. The image's home directory is different from your current one."
	choices := []termio.Choice{
		{Key: "k", Label: "keep", Description: "continue with existing home volume"},
		{Key: "m", Label: "migrate", Description: "create fresh volume, copy all files on top"},
		{Key: "r", Label: "reset", Description: "replace with fresh volume from image (lose local changes)"},
		{Key: "q", Label: "quit", Description: "exit without starting a session"},
	}
	selected, err := ui.Select(prompt, choices, "k")
	if err != nil {
		ui.Warnf("prompt failed, continuing with existing volume")
		return ActionKeep
	}
	action, _ := FromKey(selected)
	return action
}
