//go:build linux

package doctor

import (
	"strings"
	"testing"
)

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
