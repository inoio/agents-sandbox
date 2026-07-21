package opencodemsb

import (
	"context"
	"os"
	"os/exec"
	"runtime"
)

func CheckDocker() bool {
	if _, err := exec.LookPath("docker"); err != nil {
		errorMsg("docker not found. Install Docker or Podman with docker-compatible CLI.")
		return false
	}
	return true
}

func CheckKvm() bool {
	if runtime.GOOS != "linux" {
		return true
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		errorMsg("/dev/kvm not found. Load kvm module and ensure user is in the kvm group.")
		return false
	}
	return true
}

func CheckGit() bool {
	if _, err := exec.LookPath("git"); err != nil {
		errorMsg("git not found. Install git via your system package manager.")
		return false
	}
	return true
}

func CheckAll(ctx context.Context) bool {
	return checkMsb(ctx) && CheckDocker() && CheckKvm() && CheckGit()
}
