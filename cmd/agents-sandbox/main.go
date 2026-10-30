package main

import (
	"errors"
	"os"

	"golang.org/x/term"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func main() {
	installBareCheckoutLocator()
	args := os.Args[1:]
	ui := termio.New(os.Stdin, os.Stdout, os.Stderr,
		term.IsTerminal(int(os.Stderr.Fd())), termio.LevelInfo, false, false)

	if err := execute(args, ui); err != nil {
		if exitErr, ok := errors.AsType[*sandbox.ExitError](err); ok {
			os.Exit(exitErr.Code)
		}
		ui.Error("Error", err)
		os.Exit(1)
	}
}

// installBareCheckoutLocator lets path resolution find a bare checkout's
// project config. configpaths cannot import git itself, so the composition root
// injects it.
func installBareCheckoutLocator() {
	configpaths.UseBareCheckoutLocator(git.BareCheckoutRoots)
}
