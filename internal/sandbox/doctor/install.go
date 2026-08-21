package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
)

var ensureInstalledFunc = func(ctx context.Context) error { //nolint:gochecknoglobals // test seam, swapped in tests
	return msb.Get().EnsureInstalled(ctx)
}

// ensureMsbInstalled runs the msb runtime setup.
func ensureMsbInstalled(ctx context.Context) error {
	return ensureInstalledFunc(ctx)
}

// msbBinPath resolves home, the microsandbox bin directory and the msb binary
// path, reporting an error when the path cannot be resolved or the binary is
// missing.
func msbBinPath() (string, string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", "", fmt.Errorf("msb not on PATH and home directory cannot be resolved: %w", err)
	}
	binDir := filepath.Join(home, ".microsandbox", "bin")
	binPath := filepath.Join(binDir, "msb")
	if _, err := os.Stat(binPath); err != nil {
		return "", "", "", fmt.Errorf("msb not on PATH and binary missing at %s: %w", binDir, err)
	}
	return home, binDir, binPath, nil
}

// pathHints adds the microsandbox bin directory to PATH for the session and
// returns user-facing messages for making that permanent in other shells.
func pathHints(home, shell, binDir, binPath string) []string {
	rc := shellRcFile(home, shell)
	return []string{
		fmt.Sprintf("msb CLI installed (in %s), but not in PATH.", binDir),
		fmt.Sprintf(
			"If you want to add it to PATH:\n  echo 'export PATH=\"$PATH:%s\"' >> %s\n  or: ln -s %s ~/.local/bin/msb",
			binDir,
			rc,
			binPath,
		),
	}
}
