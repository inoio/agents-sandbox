// Command migrate exercises the main module's prompt backends end to end, so
// the line and huh implementations can be compared interactively (tmux) and
// non-interactively (piped stdin).
package main

import (
	"fmt"
	"os"

	"github.com/inoio/agents-sandbox/internal/termio"
)

func main() {
	backend, err := termio.ParsePromptBackend(os.Getenv("AGENTS_SANDBOX_PROMPT"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		backend = termio.PromptLine
	}

	ui := termio.New(os.Stdin, os.Stdout, os.Stderr, true, termio.LevelInfo, false, false,
		termio.WithPromptBackend(backend))

	choices := []termio.Choice{
		{Label: "Keep", Key: "k", Description: "Keep the worktree"},
		{Label: "Remove", Key: "r", Description: "Remove the worktree"},
	}

	selected, err := ui.Select("What should happen to the worktree?", choices, "k")
	fmt.Printf("SELECT_RESULT=%q err=%v\n", selected, err)

	value, err := ui.Input("Branch name:", "main")
	fmt.Printf("INPUT_RESULT=%q err=%v\n", value, err)
}
