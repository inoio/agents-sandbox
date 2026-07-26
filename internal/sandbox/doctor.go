package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"gitlab.inoio.de/inoio/opencode-msb/internal/output"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// ensureInstalled is indirected so tests can stub the SDK runtime download.
// It defaults to the real EnsureInstalled; tests reassign it and restore via
// t.Cleanup.
var ensureInstalled = func(ctx context.Context) error { //nolint:gochecknoglobals // test seam, swapped in tests
	return msb.EnsureInstalled(ctx)
}

// shellRcFile returns the rc file a PATH export should be appended to, chosen by
// the basename of the login shell (e.g. "/bin/zsh" -> "~/.zshrc"). Unknown or
// empty shells fall back to ~/.bashrc, the most common default on Linux.
func shellRcFile(home, shell string) string {
	switch filepath.Base(shell) {
	case "zsh":
		return filepath.Join(home, ".zshrc")
	case "fish":
		return filepath.Join(home, ".config", "fish", "config.fish")
	default:
		return filepath.Join(home, ".bashrc")
	}
}

func CheckDocker(logger *output.Printer) bool {
	if _, err := exec.LookPath("docker"); err != nil {
		logger.Errorf("docker not found. Install Docker or Podman with docker-compatible CLI: %v", err)
		return false
	}
	return true
}

func CheckKvm(logger *output.Printer) bool {
	if runtime.GOOS != "linux" {
		return true
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		logger.Errorf("/dev/kvm not found. Load kvm module and ensure user is in the kvm group: %v", err)
		return false
	}
	return true
}

func CheckGit(logger *output.Printer) bool {
	if _, err := exec.LookPath("git"); err != nil {
		logger.Errorf("git not found. Install git via your system package manager: %v", err)
		return false
	}
	return true
}

func CheckMsb(ctx context.Context, logger *output.Printer) bool {
	if err := ensureInstalled(ctx); err != nil {
		logger.Errorf("msb runtime setup failed: %v", err)
		return false
	}
	if _, err := exec.LookPath("msb"); err == nil {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		logger.Errorf("msb not on PATH and home directory cannot be resolved: %v", err)
		return false
	}
	binDir := filepath.Join(home, ".microsandbox", "bin")
	binPath := filepath.Join(binDir, "msb")
	if _, err := os.Stat(binPath); err != nil {
		logger.Errorf("msb not on PATH and binary missing at %s: %v", binDir, err)
		return false
	}
	if err := os.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
		logger.Warnf("could not add %s to PATH for this session (msb CLI may be unavailable): %v", binDir, err)
	}
	rc := shellRcFile(home, os.Getenv("SHELL"))
	logger.Warnf("msb CLI is not on your PATH. Added %s for this session.", binDir)
	logger.Infof("To make this permanent in other shells:")
	logger.Infof("  echo 'export PATH=\"$PATH:%s\"' >> %s", binDir, rc)
	logger.Infof("  or: ln -s %s ~/.local/bin/msb", binPath)
	return true
}

func CheckAll(ctx context.Context, logger *output.Printer) bool {
	return CheckMsb(ctx, logger) && CheckDocker(logger) && CheckKvm(logger) && CheckGit(logger)
}
