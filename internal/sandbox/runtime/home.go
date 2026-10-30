package runtime

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

// PrivateHome is the microsandbox home agents-sandbox owns: a private directory
// under the tool's data dir, never the user's ~/.microsandbox. It delegates to
// configpaths.MSBHome so the sandbox/msb adapter can resolve the same home
// without importing this package.
func PrivateHome() string {
	return configpaths.MSBHome()
}

// LegacyHome is the shared microsandbox home a user's own install may occupy.
// It is only read by the opt-in legacy volume copy.
func LegacyHome() (string, error) {
	home, err := configpaths.Get().UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".microsandbox"), nil
}

// setenv is a seam over os.Setenv so tests can exercise the failure path. A
// real Setenv of a valid constant key cannot fail.
var setenv = os.Setenv

// ApplyHome points the microsandbox SDK at the private home for this process by
// setting MSB_HOME. Runtime setup calls additionally name the home explicitly
// via RuntimeConfig.Home, so resolution is order-independent; MSB_HOME remains
// the single home for the FFI extraction cache and every other SDK operation
// (volumes, sandboxes, images), which read it from the environment.
func ApplyHome() (string, error) {
	home := PrivateHome()
	if err := setenv("MSB_HOME", home); err != nil {
		return "", err
	}
	return home, nil
}
