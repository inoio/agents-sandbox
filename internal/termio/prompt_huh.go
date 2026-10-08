package termio

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/huh/v2"
)

// ErrPromptAborted is returned when the user aborts an interactive prompt (for
// example with Esc or Ctrl-C).
var ErrPromptAborted = errors.New("prompt aborted")

func (p *printer) huhSelect(prompt string, choices []Choice, defaultKey string, accessible bool) (string, error) {
	selected := defaultKey
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title(prompt).
				Options(huhOptions(choices)...).
				Value(&selected),
		),
	)
	if err := p.runHuhForm(form, accessible); err != nil {
		return "", err
	}
	return selected, nil
}

func (p *printer) huhInput(prompt, defaultValue string, accessible bool) (string, error) {
	// The default is a fallback, not pre-filled text: an empty submission
	// yields the default. This matches the line backend, where typing replaces
	// the (display-only) default rather than appending to it.
	var value string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title(prompt).
				Placeholder(defaultValue).
				Value(&value),
		),
	)
	if err := p.runHuhForm(form, accessible); err != nil {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultValue, nil
	}
	return value, nil
}

func (p *printer) runHuhForm(form *huh.Form, accessible bool) error {
	// Prompts render on stderr so stdout stays machine-parseable. huh's
	// accessible mode defaults to stdout, so setting the writer is required.
	form.WithInput(p.stdin)
	form.WithOutput(p.stderr)
	if accessible {
		form.WithAccessible(true)
	}
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return ErrPromptAborted
		}
		return fmt.Errorf("prompt: %w", err)
	}
	return nil
}

// huhOptions maps termio choices to huh options. huh options have no
// description field, so the description is folded into the display label.
func huhOptions(choices []Choice) []huh.Option[string] {
	options := make([]huh.Option[string], 0, len(choices))
	for _, choice := range choices {
		label := choice.Label
		if choice.Description != "" {
			label = fmt.Sprintf("%s - %s", label, choice.Description)
		}
		options = append(options, huh.NewOption(label, choice.Key))
	}
	return options
}
