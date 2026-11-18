package state

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/network"
)

// TestNetworkProxyRecreate pins that the outbound proxy reaches the fingerprint
// microsandbox bakes into the VM at creation: changing it must recreate the
// project VM, while an emission-equal policy (a whitespace-padded proxy that
// reaches the VM identically) must not.
func TestNetworkProxyRecreate(t *testing.T) {
	base := network.Policy{Profiles: []network.Profile{network.ProfileNone}}
	withProxy := base
	withProxy.Proxy = network.Proxy{Protocol: network.ProxySOCKS5, Address: "10.0.0.1:1080"}
	if !NetworkChanged(BuildNetworkState(withProxy), base) {
		t.Error("adding a proxy must recreate the VM")
	}
	padded := base
	padded.Proxy = network.Proxy{
		Protocol: network.ProxyProtocol(" socks5 "),
		Address:  " 10.0.0.1:1080 ",
	}
	if NetworkChanged(BuildNetworkState(padded), withProxy) {
		t.Error("an emission-equal proxy must not recreate the VM")
	}
}
