//go:build linux

package doctor

import (
	"errors"
	"os"
)

// checkPlatform verifies Linux KVM availability.
func checkPlatform() error {
	if _, err := os.Stat("/dev/kvm"); err != nil {
		return kvmUnavailableError()
	}
	return nil
}

// kvmUnavailableError describes how to provide /dev/kvm. This is the one host
// prerequisite a user usually fixes with a command rather than a config change,
// so the fix follows the diagnosis and a docs link covers what commands cannot.
func kvmUnavailableError() error {
	return errors.New("/dev/kvm not found: the sandbox VM needs KVM\n" +
		"  Add your user to the kvm group, then log out and back in:\n" +
		"    sudo usermod -aG kvm \"$USER\"\n" +
		"  If the kernel module is not loaded:  sudo modprobe kvm\n" +
		"  If it still fails, enable Intel VT-x / AMD-V in your firmware.\n" +
		"  Docs: " + troubleshootingURL("kvm-unavailable"))
}

// dockerRemedyHint is the Linux way to start the Docker daemon.
func dockerRemedyHint() string {
	return "  Start the Docker daemon, then verify:\n" +
		"    sudo systemctl start docker\n" +
		"    docker info\n" +
		"  Docs: " + troubleshootingURL("docker-not-running")
}
