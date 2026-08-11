//go:build linux

package doctor

import (
	"context"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

// checkDoctor runs Linux-specific prerequisite checks.
// On Linux, this includes the KVM availability check.
func checkDoctor(ctx context.Context, ui termio.UI) bool {
	return checkKvm(ui) && docker.CheckDockerAPI(ctx, ui) && CheckDocker(ui) && checkGit(ui) && checkMsb(ctx, ui)
}
