//go:build darwin

package sandbox

import (
	"context"
	"runtime"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"
)

// checkDoctor runs macOS-specific prerequisite checks.
// On macOS, this includes an ARM64 architecture check.
func checkDoctor(ctx context.Context, ui stdio.UI) bool {
	return CheckDarwin(ui) && docker.CheckDockerAPI(ctx, ui) && CheckDocker(ui) && CheckGit(ui) && CheckMsb(ctx, ui)
}

// CheckDarwin validates that opencode-msb is running on Apple Silicon.
// The macOS binary is compiled for arm64 only; x86_64 binaries under Rosetta 2
// are unsupported.
func CheckDarwin(ui stdio.UI) bool {
	if runtime.GOARCH != "arm64" {
		ui.Errorf(
			"macOS support requires Apple Silicon (arm64). You are running the x86_64 binary under Rosetta 2. Download the darwin-arm64 binary instead.",
		)
		return false
	}
	return true
}
