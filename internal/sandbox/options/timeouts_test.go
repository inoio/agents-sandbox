package options

import (
	"testing"
	"time"
)

// TestDefaultVMIdleTimeoutPinsDocumentedValue pins the documented idle-timeout
// default (10s, see docs/configuration/launcher.md). The VM lifecycle applies
// DefaultVMIdleTimeout whenever RunOptions.IdleTimeout is unset, and the tests
// that exercise that fallback read the constant as their expected value, so
// they cannot catch a change to it. This assertion is what keeps the constant
// equal to the value the docs promise.
func TestDefaultVMIdleTimeoutPinsDocumentedValue(t *testing.T) {
	if DefaultVMIdleTimeout != 10*time.Second {
		t.Errorf("DefaultVMIdleTimeout = %v, want the documented 10s", DefaultVMIdleTimeout)
	}
}
