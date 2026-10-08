package main

import (
	"bytes"
	"fmt"
	"os"

	"charm.land/huh/v2"
)

func main() {
	options := []string{"Alpha", "Bravo", "Charlie", "Delta"}
	which := os.Getenv("SPIKE_WHICH")
	scripted := os.Getenv("SPIKE_HUH_SCRIPTED") == "1"
	accessible := os.Getenv("SPIKE_HUH_ACCESSIBLE") == "1"

	if which == "" || which == "single" {
		var single string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("huh: pick one").
					Options(huh.NewOptions(options...)...).
					Value(&single),
			),
		)
		runForm(form, scripted, accessible, "\x1b[B\r")
		fmt.Printf("HUH_SINGLE_RESULT=%q\n", single)
	}

	if which == "" || which == "multi" {
		var multi []string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewMultiSelect[string]().
					Title("huh: pick many").
					Options(huh.NewOptions(options...)...).
					Value(&multi),
			),
		)
		runForm(form, scripted, accessible, "\x1b[B \x1b[B \r")
		fmt.Printf("HUH_MULTI_RESULT=%q\n", multi)
	}
}

func runForm(form *huh.Form, scripted, accessible bool, keys string) {
	if override := os.Getenv("SPIKE_HUH_KEYS"); override != "" {
		keys = override
	}
	if accessible {
		form.WithAccessible(true)
	}
	if scripted {
		var captured bytes.Buffer
		form.WithInput(bytes.NewReader([]byte(keys)))
		form.WithOutput(&captured)
		if err := form.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "huh scripted error: %v\n", err)
		}
		fmt.Fprintf(os.Stderr, "HUH_SCRIPTED_FRAMES=%q\n", captured.String())
		return
	}

	form.WithInput(os.Stdin)
	form.WithOutput(os.Stdout)
	if err := form.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "huh error: %v\n", err)
	}
}
