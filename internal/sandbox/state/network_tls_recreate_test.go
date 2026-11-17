package state

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/network"
)

// TestNetworkTLSRecreate pins that each TLS-interception setting reaches the
// fingerprint microsandbox bakes into the VM at creation: changing any of them
// must recreate the project VM, while an unchanged policy must not.
func TestNetworkTLSRecreate(t *testing.T) {
	base := network.Policy{Profiles: []network.Profile{network.ProfileNone}}
	withBypass := base
	withBypass.TLSBypass = []string{"*.internal.example"}
	if !NetworkChanged(BuildNetworkState(base), withBypass) {
		t.Error("adding tls-bypass must recreate the VM")
	}
	on := base
	on.InterceptTLS = network.InterceptOn
	if !NetworkChanged(BuildNetworkState(base), on) {
		t.Error("changing intercept-tls must recreate the VM")
	}
	strict := base
	strict.DisableStrict = true
	if !NetworkChanged(BuildNetworkState(base), strict) {
		t.Error("changing disable-strict must recreate the VM")
	}
	if NetworkChanged(BuildNetworkState(withBypass), withBypass) {
		t.Error("an unchanged policy must not recreate the VM")
	}
}
