//go:build linux

package sandbox

import (
	"context"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"
)

// checkDoctor runs Linux-specific prerequisite checks.
// On Linux, this includes the KVM availability check.
func checkDoctor(ctx context.Context, ui stdio.UI) bool {
	return CheckKvm(ui) && docker.CheckDockerAPI(ctx, ui) && CheckDocker(ui) && CheckGit(ui) && CheckMsb(ctx, ui)
}
