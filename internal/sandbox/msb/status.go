package msb

import (
	"fmt"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// VMStatusKind is the semantic lifecycle class of a sandbox status, as
// consumed by the launcher's VM lifecycle decisions.
type VMStatusKind int

const (
	VMStatusUnknown VMStatusKind = iota
	VMStatusCreated
	VMStatusStarting
	VMStatusActive
	VMStatusStopped
)

// GetVMStatus maps a sandbox status to its lifecycle class. Starting is an
// active runtime transition; running, draining, and paused have an active
// runtime; created has no runtime but can be started; stopped and crashed are
// terminal. Unknown statuses return an error so callers can fail closed.
func GetVMStatus(status msbSdk.SandboxStatus) (VMStatusKind, error) {
	switch status {
	case msbSdk.SandboxStatusCreated:
		return VMStatusCreated, nil
	case msbSdk.SandboxStatusStarting:
		return VMStatusStarting, nil
	case msbSdk.SandboxStatusRunning, msbSdk.SandboxStatusDraining, msbSdk.SandboxStatusPaused:
		return VMStatusActive, nil
	case msbSdk.SandboxStatusStopped, msbSdk.SandboxStatusCrashed:
		return VMStatusStopped, nil
	default:
		// gomutants:disable-next-line RETURN_ZERO reason="VMStatusUnknown is VMStatusKind's zero value, so a RETURN_ZERO replacement is identical"
		return VMStatusUnknown, fmt.Errorf("unexpected sandbox status: %q", status)
	}
}

// IsSandboxActive reports whether a sandbox has a live runtime or is in an
// active runtime transition. Starting is included to protect a VM that another
// caller is booting, although it is not ready for commands yet.
func IsSandboxActive(status msbSdk.SandboxStatus) bool {
	// An unknown status reads as VMStatusUnknown, which is neither.
	kind, _ := GetVMStatus(status)
	return kind == VMStatusStarting || kind == VMStatusActive
}

// IsSandboxInactive reports whether a sandbox has no live runtime and is safe
// to treat as a stopped resource. Unknown statuses return false so callers
// that remove or reuse resources fail closed.
func IsSandboxInactive(status msbSdk.SandboxStatus) bool {
	kind, _ := GetVMStatus(status)
	return kind == VMStatusCreated || kind == VMStatusStopped
}
