package volume

import (
	"fmt"

	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/viperconfig"
)

// VolumeAction is the user-selected disposition for an existing home volume.
type VolumeAction int

const (
	ActionKeep VolumeAction = iota
	ActionMigrate
	ActionReset
	ActionQuit
)

const (
	labelKeep    = "keep"
	labelMigrate = "migrate"
	labelReset   = "reset"
	labelQuit    = "quit"
)

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
		return labelReset
	}
	if action == ActionMigrate {
		return labelMigrate
	}
	return labelKeep
}

// AnswerAsk is the viperconfig.KeyHomeVolumeOnImageChange value that shows the
// dialog.
const AnswerAsk = "ask"

// ImageChangePolicy is how ResolveHomeAction answers an image change: the
// configured answer (keep, migrate, reset, or ask; empty means keep), and, for
// ask, how to remember the answer the user picks as the configured one.
type ImageChangePolicy struct {
	Answer string
	// Remember writes answer as the configured one and returns the config
	// directory it was written to. Nil offers no remembering.
	Remember func(answer string) (string, error)
}

// ResolveHomeAction decides what happens to the home volume when the stored
// image digest differs from the current one. A configured answer applies
// without a dialog and is named in one info line; ask shows the dialog (keeping
// the volume when the session is not interactive) and then offers to remember
// the answer. An unchanged image keeps the volume silently.
func (vm *Manager) ResolveHomeAction(
	ui termio.UI,
	storedDigest, currentDigest string,
	policy ImageChangePolicy,
) VolumeAction {
	if storedDigest == currentDigest {
		return ActionKeep
	}
	if policy.Answer != AnswerAsk {
		action := configuredAction(policy.Answer)
		ui.Infof("the runner image changed; %s the home volume (%s: %s)",
			actionVerb(action), viperconfig.KeyHomeVolumeOnImageChange, actionLabel(action))
		return action
	}
	if !ui.IsInteractive() {
		ui.Infof("non-interactive: image changed; keeping existing home volume")
		return ActionKeep
	}
	action, answered := askHomeAction(ui)
	if answered && action != ActionQuit && policy.Remember != nil {
		rememberHomeAction(ui, action, policy.Remember)
	}
	return action
}

// askHomeAction shows the home-volume dialog and reports whether it was
// answered; a failed dialog keeps the volume.
func askHomeAction(ui termio.UI) (VolumeAction, bool) {
	prompt := "The project's runner image changed. What should happen to the home volume?"
	choices := []termio.Choice{
		{Key: "k", Label: labelKeep, Description: "continue with existing home volume"},
		{Key: "m", Label: labelMigrate, Description: "create fresh volume, copy all files on top"},
		{Key: "r", Label: labelReset, Description: "replace with fresh volume from image (lose local changes)"},
		{Key: "q", Label: labelQuit, Description: "exit without starting a session"},
	}
	selected, err := ui.Select(prompt, choices, "k")
	if err != nil {
		ui.Warnf("prompt failed, continuing with existing volume")
		// gomutants:disable-next-line RETURN_ZERO reason="ActionKeep is the zero VolumeAction"
		return ActionKeep, false
	}
	action, _ := FromKey(selected)
	return action, true
}

// rememberHomeAction offers to make action the configured answer and writes it
// when the user agrees.
func rememberHomeAction(ui termio.UI, action VolumeAction, remember func(string) (string, error)) {
	label := actionLabel(action)
	agreed, err := ui.Confirm(fmt.Sprintf(
		"Use %s on every image change from now on, without asking? This sets %s: %s.",
		label,
		viperconfig.KeyHomeVolumeOnImageChange,
		label,
	), false)
	if err != nil || !agreed {
		return
	}
	dir, err := remember(label)
	if err != nil {
		ui.Warnf("could not save %s: %v", viperconfig.KeyHomeVolumeOnImageChange, err)
		return
	}
	ui.Infof("saved %s: %s in %s", viperconfig.KeyHomeVolumeOnImageChange, label, dir)
}

// configuredAction maps a configured answer to its action; anything else,
// including the empty default, keeps the volume.
func configuredAction(answer string) VolumeAction {
	switch answer {
	case labelMigrate:
		return ActionMigrate
	case labelReset:
		return ActionReset
	default:
		// gomutants:disable-next-line RETURN_ZERO reason="ActionKeep is the zero VolumeAction"
		return ActionKeep
	}
}

// actionVerb is the info line's verb for action.
func actionVerb(action VolumeAction) string {
	switch action {
	case ActionMigrate:
		return "migrating"
	case ActionReset:
		return "resetting"
	case ActionKeep, ActionQuit:
	}
	return "keeping"
}
