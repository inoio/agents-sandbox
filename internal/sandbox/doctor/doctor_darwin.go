//go:build darwin

package doctor

import (
	"errors"
	"runtime"
)

// checkPlatform validates that opencode-sandbox is running on Apple Silicon.
func checkPlatform() error {
	if runtime.GOARCH != "arm64" {
		return errors.New("only arm64 macOS (Apple Silicon) is supported")
	}
	return nil
}
