// Package huhui is a migration spike: it implements termio's interactive
// prompts (Select/Input) with charmbracelet/huh while delegating every other
// method to an existing termio.UI.
//
// The point is to prove what a migration would touch: the termio.UI interface
// and all its callers stay untouched, the non-interactive path stays
// byte-for-byte identical (it delegates to the wrapped UI), and only the
// interactive Select/Input rendering changes.
package huhui

import (
	"errors"
	"fmt"
	"io"

	"charm.land/huh/v2"

	"github.com/inoio/agents-sandbox/internal/termio"
)

// Mode selects how the huh form renders in interactive mode.
type Mode int

const (
	// ModeForm renders the normal huh arrow-key menu. It needs a real
	// terminal.
	ModeForm Mode = iota
	// ModeAccessible renders huh's line-based, screen-reader-friendly
	// prompts (numbered options). It works with a plain io.Reader and is the
	// deterministic, testable path.
	ModeAccessible
)

// ErrAborted is returned when the user aborts a huh prompt (Esc/Ctrl-C).
var ErrAborted = errors.New("prompt aborted")

// UI decorates a termio.UI, overriding only Select and Input.
type UI struct {
	termio.UI
	stdin io.Reader
	mode  Mode
}

// New wraps base. stdin is the raw input stream (termio's UI interface does not
// expose it, which is the one interface gap this spike highlights).
func New(base termio.UI, stdin io.Reader, mode Mode) *UI {
	return &UI{UI: base, stdin: stdin, mode: mode}
}

var _ termio.UI = (*UI)(nil)

// Select asks the user to choose one of choices and returns the Choice.Key.
//
// Non-interactive behavior is delegated to the wrapped UI, preserving the
// current "use the default" semantics exactly.
func (u *UI) Select(prompt string, choices []termio.Choice, defaultKey string) (string, error) {
	if !u.UI.IsInteractive() {
		return u.UI.Select(prompt, choices, defaultKey)
	}

	selected := defaultKey
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title(prompt).
				Options(chooseOptions(choices)...).
				Value(&selected),
		),
	)
	return selected, u.run(form)
}

// Input reads a line, defaulting to defaultValue on empty input. Non-interactive
// behavior is delegated to the wrapped UI.
func (u *UI) Input(prompt, defaultValue string) (string, error) {
	if !u.UI.IsInteractive() {
		return u.UI.Input(prompt, defaultValue)
	}

	value := defaultValue
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title(prompt).
				Value(&value),
		),
	)
	return value, u.run(form)
}

func (u *UI) run(form *huh.Form) error {
	// Prompts must render on stderr so stdout stays machine-parseable.
	// Accessible mode defaults to stdout, so this must be explicit.
	form.WithInput(u.stdin)
	form.WithOutput(u.UI.StdErr())
	if u.mode == ModeAccessible {
		form.WithAccessible(true)
	}
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return ErrAborted
		}
		return err
	}
	return nil
}

// chooseOptions maps termio choices to huh options. huh options have no
// description field, so the description is folded into the display label.
func chooseOptions(choices []termio.Choice) []huh.Option[string] {
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
