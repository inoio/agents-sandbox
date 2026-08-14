package doctor

import (
	"context"
	"os"
	"os/exec"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

// CheckDocker reports whether the docker binary is on PATH, logging a
// descriptive error when it is not.
func CheckDocker(ui termio.UI) bool {
	if _, err := exec.LookPath("docker"); err != nil {
		ui.Error("docker not found. Install Docker or Podman with docker-compatible CLI", err)
		return false
	}
	return true
}

func checkGit(ui termio.UI) bool {
	if _, err := exec.LookPath("git"); err != nil {
		ui.Error("git not found. Install git via your system package manager", err)
		return false
	}
	return true
}

func checkMsb(ctx context.Context, ui termio.UI) bool {
	if err := ensureMsbInstalled(ctx, ui); err != nil {
		ui.Error("the microsandbox runtime could not be auto-installed", err)
		return false
	}
	if _, err := exec.LookPath("msb"); err == nil {
		return true
	}
	home, binDir, binPath, ok := msbBinPath(ui)
	if !ok {
		return false
	}
	appendPathHint(home, os.Getenv("SHELL"), binDir, binPath, ui)
	return true
}

// checkAllReal contains the actual CheckAll logic. This allows the exported
// CheckAllFunc to be reassigned in tests without redefining the checks.
func checkAllReal(ctx context.Context, ui termio.UI) bool {
	return checkDoctor(ctx, ui)
}

// CheckAll runs all prerequisite checks and reports orphaned VMs.
func CheckAll(ctx context.Context, ui termio.UI) bool {
	return CheckAllFunc(ctx, ui)
}
