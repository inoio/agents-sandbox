package main

import (
	"errors"
	"os"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
)

func main() {
	args := os.Args[1:]
	ui := newUI(args)
	ui.Verbose("Initialized terminal output")

	if err := Execute(args, ui); err != nil {
		var exitErr *sandbox.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		ui.Error("Failed: ", err)
		os.Exit(1)
	}
}
