package main

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/term"

	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func main() {
	args := os.Args[1:]
	ui := termio.New(os.Stdin, os.Stdout, os.Stderr,
		term.IsTerminal(int(os.Stderr.Fd())), termio.LevelInfo, false, false,
		termio.WithPromptBackend(promptBackend()))

	if err := execute(args, ui); err != nil {
		if exitErr, ok := errors.AsType[*sandbox.ExitError](err); ok {
			os.Exit(exitErr.Code)
		}
		ui.Error("Error", err)
		os.Exit(1)
	}
}

// promptBackend selects the interactive prompt backend from the
// OPENCODE_SANDBOX_PROMPT environment variable (line, huh or huh-accessible),
// falling back to line on an invalid value.
func promptBackend() termio.PromptBackend {
	backend, err := termio.ParsePromptBackend(os.Getenv("OPENCODE_SANDBOX_PROMPT"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return termio.PromptLine
	}
	return backend
}
