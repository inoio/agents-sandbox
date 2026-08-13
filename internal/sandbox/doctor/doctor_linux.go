//go:build linux

package doctor

import (
	"context"
	"os"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

// checkDoctor runs Linux-specific prerequisite checks.
// On Linux, this includes the KVM availability check.
func checkDoctor(ctx context.Context, ui termio.UI) bool {
	return checkKvm(ui) && docker.CheckDockerAPI(ctx, ui) && CheckDocker(ui) && checkGit(ui) && checkMsb(ctx, ui)
}

func checkKvm(ui termio.UI) bool {
	if _, err := os.Stat("/dev/kvm"); err != nil {
		ui.Error("/dev/kvm not found. Load kvm module and ensure user is in the kvm group", err)
		return false
	}
	return true
}
