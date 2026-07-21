//go:build !cgo

package opencodemsb

import "fmt"

func runCommand(opts RunOptions) error {
	return fmt.Errorf("run command requires CGO to be enabled")
}
