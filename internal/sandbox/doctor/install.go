package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

// ensureMsbInstalled runs the msb runtime setup, reporting a fatal message on
// failure so the caller can short-circuit.
func ensureMsbInstalled(ctx context.Context, ui termio.UI) error {
	if err := ensureInstalled(ctx); err != nil {
		ui.Error("msb runtime setup failed", err)
		return err
	}
	return nil
}

// msbBinPath resolves home, the microsandbox bin directory and the msb binary
// path, reporting a fatal message when the path cannot be resolved or the
// binary is missing. It has no side effects beyond reporting.
func msbBinPath(ui termio.UI) (string, string, string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		ui.Error("msb not on PATH and home directory cannot be resolved", err)
		return "", "", "", false
	}
	binDir := filepath.Join(home, ".microsandbox", "bin")
	binPath := filepath.Join(binDir, "msb")
	if _, err := os.Stat(binPath); err != nil {
		ui.Error(fmt.Sprintf("msb not on PATH and binary missing at %s", binDir), err)
		return "", "", "", false
	}
	return home, binDir, binPath, true
}

// appendPathHint adds the microsandbox bin directory to PATH for the session
// and tells the user how to make that permanent in other shells.
func appendPathHint(home, shell, binDir, binPath string, ui termio.UI) {
	if err := os.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
		ui.Warnf("could not add %s to PATH for this session (msb CLI may be unavailable): %v", binDir, err)
	}
	rc := shellRcFile(home, shell)
	ui.Warnf("msb CLI is not on your PATH. Added %s for this session.", binDir)
	ui.Infof("To make this permanent in other shells:")
	ui.Infof("  echo 'export PATH=\"$PATH:%s\"' >> %s", binDir, rc)
	ui.Infof("  or: ln -s %s ~/.local/bin/msb", binPath)
}
