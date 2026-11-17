package msb_test

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// TestProxySpecEmpty pins that every proxy field can make the spec non-empty on
// its own: sandboxOptions only emits the proxy option when the spec is
// non-empty, so a field masked by Empty() would silently drop the proxy.
func TestProxySpecEmpty(t *testing.T) {
	if !(msb.ProxySpec{}).Empty() {
		t.Error("zero ProxySpec should be empty")
	}
	nonEmpty := []struct {
		name  string
		proxy msb.ProxySpec
	}{
		{"protocol", msb.ProxySpec{Protocol: msb.ProxySOCKS5}},
		{"address", msb.ProxySpec{Address: "10.0.0.1:1080"}},
		{"username", msb.ProxySpec{Username: "u"}},
		{"password-env", msb.ProxySpec{PasswordEnv: "P"}},
		{
			"all fields",
			msb.ProxySpec{Protocol: msb.ProxySOCKS5, Address: "10.0.0.1:1080", Username: "u", PasswordEnv: "P"},
		},
	}
	for _, tc := range nonEmpty {
		t.Run(tc.name, func(t *testing.T) {
			if tc.proxy.Empty() {
				t.Errorf("ProxySpec with only %s set should not be empty", tc.name)
			}
		})
	}
}
