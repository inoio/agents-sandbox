//go:build darwin

package doctor

import (
	"context"
	"runtime"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

// checkDoctor runs macOS-specific prerequisite checks.
// On macOS, this includes an ARM64 architecture check.
func checkDoctor(ctx context.Context, ui termio.UI) bool {
	return CheckDarwin(ui) && docker.CheckDockerAPI(ctx, ui) && CheckDocker(ui) && checkGit(ui) && checkMsb(ctx, ui)
}

// CheckDarwin validates that opencode-sandbox is running on Apple Silicon.
// The macOS binary is compiled for arm64 only; x86_64 binaries under Rosetta 2
// are unsupported.
func CheckDarwin(ui termio.UI) bool {
	if runtime.GOARCH != "arm64" {
		ui.Errorf(
			"macOS support requires Apple Silicon (arm64). You are running the x86_64 binary under Rosetta 2. Download the darwin-arm64 binary instead.",
		)
		return false
	}
	return true
}

// checkKvm is a no-op on macOS where KVM is unavailable.
func checkKvm(ui termio.UI) bool { return true }
