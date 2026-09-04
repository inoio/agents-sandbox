package main

import (
	"errors"
	"os"

	"golang.org/x/term"

	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func main() {
	args := os.Args[1:]
	ui := termio.New(os.Stdin, os.Stdout, os.Stderr,
		term.IsTerminal(int(os.Stderr.Fd())), termio.LevelInfo, false, false)

	if err := execute(args, ui); err != nil {
		var exitErr *sandbox.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		ui.Error("Error", err)
		os.Exit(1)
	}
}
