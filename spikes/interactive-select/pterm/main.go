package main

import (
	"fmt"
	"os"

	"github.com/pterm/pterm"
)

func main() {
	if os.Getenv("SPIKE_PTERM_STDERR") == "1" {
		pterm.SetDefaultOutput(os.Stderr)
	}

	options := []string{"Alpha", "Bravo", "Charlie", "Delta"}
	which := os.Getenv("SPIKE_WHICH")

	if which == "" || which == "single" {
		single, err := pterm.DefaultInteractiveSelect.
			WithOptions(options).
			WithDefaultText("pterm: pick one").
			Show()
		if err != nil {
			fmt.Fprintf(os.Stderr, "pterm single error: %v\n", err)
		}
		fmt.Printf("PTERM_SINGLE_RESULT=%q\n", single)
	}

	if which == "" || which == "multi" {
		multi, err := pterm.DefaultInteractiveMultiselect.
			WithOptions(options).
			WithDefaultText("pterm: pick many").
			Show()
		if err != nil {
			fmt.Fprintf(os.Stderr, "pterm multi error: %v\n", err)
		}
		fmt.Printf("PTERM_MULTI_RESULT=%q\n", multi)
	}
}
