package sandbox

import (
	"context"
	"os"
	"os/exec"
	"runtime"

	"gitlab.inoio.de/inoio/opencode-msb/internal/log"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

func CheckDocker(logger *log.Logger) bool {
	if _, err := exec.LookPath("docker"); err != nil {
		logger.Error("docker not found. Install Docker or Podman with docker-compatible CLI.")
		return false
	}
	return true
}

func CheckKvm(logger *log.Logger) bool {
	if runtime.GOOS != "linux" {
		return true
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		logger.Error("/dev/kvm not found. Load kvm module and ensure user is in the kvm group.")
		return false
	}
	return true
}

func CheckGit(logger *log.Logger) bool {
	if _, err := exec.LookPath("git"); err != nil {
		logger.Error("git not found. Install git via your system package manager.")
		return false
	}
	return true
}

func CheckMsb(ctx context.Context, logger *log.Logger) bool {
	if err := msb.EnsureInstalled(ctx); err != nil {

		logger.Error("msb not found. Install microsandbox: https://github.com/microsandbox/microsandbox")
		return false
	}
	return true
}

func CheckAll(ctx context.Context, logger *log.Logger) bool {
	return CheckMsb(ctx, logger) && CheckDocker(logger) && CheckKvm(logger) && CheckGit(logger)
}
