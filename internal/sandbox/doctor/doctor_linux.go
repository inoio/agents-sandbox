//go:build linux

package doctor

import (
	"errors"
	"os"

	"github.com/inoio/agents-sandbox/internal/docslink"
)

// kvmDevice is the host device the sandbox VM needs.
const kvmDevice = "/dev/kvm"

// checkPlatform verifies Linux KVM availability.
func checkPlatform() error {
	return checkKVMDevice(kvmDevice)
}

// checkKVMDevice reports whether the KVM device is present. The path is a
// parameter so both outcomes are testable wherever the tests run, whether or
// not the host has /dev/kvm.
func checkKVMDevice(path string) error {
	if _, err := os.Stat(path); err != nil {
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
		"  Docs: " + docslink.SectionURL("troubleshooting", "kvm-unavailable"))
}

// dockerRemedyHint is the Linux way to start the Docker daemon.
func dockerRemedyHint() string {
	return "  Start the Docker daemon, then verify:\n" +
		"    sudo systemctl start docker\n" +
		"    docker info\n" +
		"  Docs: " + docslink.SectionURL("troubleshooting", "docker-not-running")
}
