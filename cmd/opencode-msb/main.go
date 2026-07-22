package main

import (
	"fmt"
	"os"

	"gitlab.inoio.de/inoio/opencode-msb/internal/opencodemsb"
)

func main() {
	if err := opencodemsb.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
