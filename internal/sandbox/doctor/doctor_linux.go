//go:build linux

package doctor

import (
	"errors"
	"os"
)

// checkPlatform verifies Linux KVM availability.
func checkPlatform() error {
	if _, err := os.Stat("/dev/kvm"); err != nil {
		return errors.New("/dev/kvm not found. Load kvm module and ensure user is in the kvm group")
	}
	return nil
}
