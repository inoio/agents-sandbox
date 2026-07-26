package sandbox

import (
	"context"
	"os"
	"os/exec"
	"runtime"

	"gitlab.inoio.de/inoio/opencode-msb/internal/output"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

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
	if err := msb.EnsureInstalled(ctx); err != nil {
		logger.Errorf("msb not found. Install microsandbox (https://github.com/microsandbox/microsandbox): %v", err)
		return false
	}
	return true
}

func CheckAll(ctx context.Context, logger *output.Printer) bool {
	return CheckMsb(ctx, logger) && CheckDocker(logger) && CheckKvm(logger) && CheckGit(logger)
}
