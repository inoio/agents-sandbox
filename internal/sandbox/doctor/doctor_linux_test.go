//go:build linux

package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckKVMDeviceReportsMissingDevice and its present-device counterpart pin
// both outcomes of the KVM check. checkPlatform itself can only observe the real
// /dev/kvm, so which branch the test host exercises is not knowable; injecting
// the path is what makes the other one reachable.
func TestCheckKVMDeviceReportsMissingDevice(t *testing.T) {
	err := checkKVMDevice(filepath.Join(t.TempDir(), "kvm"))
	if err == nil {
		t.Fatal("checkKVMDevice() = nil, want an error when the device is absent")
	}
	if !strings.Contains(err.Error(), "troubleshooting.html#kvm-unavailable") {
		t.Errorf("checkKVMDevice() = %q, want it to link the troubleshooting section", err)
	}
}

func TestCheckKVMDeviceAcceptsPresentDevice(t *testing.T) {
	device := filepath.Join(t.TempDir(), "kvm")
	if err := os.WriteFile(device, nil, 0o600); err != nil {
		t.Fatalf("creating a stand-in device: %v", err)
	}
	if err := checkKVMDevice(device); err != nil {
		t.Errorf("checkKVMDevice(%q) = %v, want nil for a present device", device, err)
	}
}

func TestKVMUnavailableErrorDescribesRemedy(t *testing.T) {
	message := kvmUnavailableError().Error()
	for _, want := range []string{
		"/dev/kvm not found",
		"sudo usermod -aG kvm",
		"sudo modprobe kvm",
		"troubleshooting.html#kvm-unavailable",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("kvmUnavailableError() = %q, want it to contain %q", message, want)
		}
	}
}

func TestDockerRemedyHintNamesSystemctl(t *testing.T) {
	hint := dockerRemedyHint()
	for _, want := range []string{
		"sudo systemctl start docker",
		"docker info",
		"troubleshooting.html#docker-not-running",
	} {
		if !strings.Contains(hint, want) {
			t.Errorf("dockerRemedyHint() = %q, want it to contain %q", hint, want)
		}
	}
}
