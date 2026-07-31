package main

import (
	"os"
)

func main() {
	args := os.Args[1:]
	ui := newUI(args)

	if err := Execute(args, ui); err != nil {
		ui.Error("Failed: ", err)
		os.Exit(1)
	}
}
