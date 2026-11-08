package ports

import (
	"net"
	"slices"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

func TestParsePortMapping(t *testing.T) {
	cases := []struct {
		spec string
		want PortMapping
	}{
		{spec: "3000", want: PortMapping{HostPort: 3000, GuestPort: 3000}},
		{spec: "8080:3000", want: PortMapping{HostPort: 8080, GuestPort: 3000}},
		{spec: " 3000 ", want: PortMapping{HostPort: 3000, GuestPort: 3000}},
	}
	for _, tc := range cases {
		got, err := ParsePortMapping(tc.spec)
		if err != nil {
			t.Fatalf("ParsePortMapping(%q): %v", tc.spec, err)
		}
		if got != tc.want {
			t.Errorf("ParsePortMapping(%q) = %+v, want %+v", tc.spec, got, tc.want)
		}
	}
}

func TestParsePortMappingRejects(t *testing.T) {
	for _, spec := range []string{"", "0", "70000", "8080:0", "8080:70000", "a", "8080:3000:5", "8080:"} {
		if _, err := ParsePortMapping(spec); err == nil {
			t.Errorf("ParsePortMapping(%q) = no error, want one", spec)
		}
	}
}

func TestParsePortMappingsRejectsInvalidSpecAmongValid(t *testing.T) {
	if _, err := ParsePortMappings([]string{"3000", "70000"}); err == nil {
		t.Fatal("expected an error for the invalid spec among valid ones")
	}
}

func TestParsePortMappingsRejectsDuplicateHostPorts(t *testing.T) {
	if _, err := ParsePortMappings([]string{"3000", "3000:5173"}); err == nil {
		t.Fatal("expected an error for the duplicate host port")
	}
}

func TestParsePortMappingsEmpty(t *testing.T) {
	if got, err := ParsePortMappings(nil); err != nil || got != nil {
		t.Errorf("ParsePortMappings(nil) = %v, %v; want nil, nil", got, err)
	}
}

func TestHostPorts(t *testing.T) {
	got := HostPorts([]PortMapping{{HostPort: 3000, GuestPort: 3000}, {HostPort: 8080, GuestPort: 5173}})
	if !slices.Equal(got, []int{3000, 8080}) {
		t.Errorf("HostPorts = %v, want [3000 8080]", got)
	}
}

func TestProbeHostPortReportsRealLoopbackState(t *testing.T) {
	//testenv:allow binds loopback to assert the real probe
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("bind loopback listener: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	if probeHostPort(port) {
		t.Errorf("probeHostPort(%d) = true while the port is bound, want false", port)
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("close loopback listener: %v", err)
	}
	if !probeHostPort(port) {
		t.Errorf("probeHostPort(%d) = false after the port was released, want true", port)
	}
}

func TestFirstFreeHostPortExcludingSkipsBoundAndExcluded(t *testing.T) {
	probe := func(port int) bool { return port != 5999 }
	if got := firstFreeHostPortExcluding(5999, []int{6000}, probe); got != 6001 {
		t.Errorf("firstFreeHostPortExcluding(5999, [6000]) = %d, want 6001", got)
	}
}

func TestFirstFreeHostPortExcludingWithoutExclusions(t *testing.T) {
	probe := func(port int) bool { return port != 5999 }
	if got := firstFreeHostPortExcluding(5999, nil, probe); got != 6000 {
		t.Errorf("firstFreeHostPortExcluding(5999, nil) = %d, want 6000", got)
	}
}

func TestFirstFreeHostPortExcludingExhaustedReturnsZero(t *testing.T) {
	excluded := make([]int, 0, ProbeRange)
	for port := 6000; port < 6000+ProbeRange; port++ {
		excluded = append(excluded, port)
	}
	if got := firstFreeHostPortExcluding(6000, excluded, func(int) bool { return false }); got != 0 {
		t.Errorf("firstFreeHostPortExcluding(every port excluded) = %d, want 0 (nothing free)", got)
	}
	if got := firstFreeHostPortExcluding(6000, nil, func(int) bool { return false }); got != 0 {
		t.Errorf("firstFreeHostPortExcluding(every port bound) = %d, want 0 (nothing free)", got)
	}
}

func TestServeOnlyBindings(t *testing.T) {
	got := ServeOnlyBindings(4097)
	want := []msb.PortBinding{{
		Bind: ServeOnlyBindAddr, HostPort: 4097, GuestPort: ServeOnlyBasePort, Protocol: msb.PortProtocolTCP,
	}}
	if !slices.Equal(got, want) {
		t.Errorf("ServeOnlyBindings(4097) = %+v, want %+v", got, want)
	}
}

func TestUserPortBindings(t *testing.T) {
	got := UserPortBindings([]PortMapping{{HostPort: 8080, GuestPort: 5173}})
	want := []msb.PortBinding{{
		Bind: ServeOnlyBindAddr, HostPort: 8080, GuestPort: 5173, Protocol: msb.PortProtocolTCP,
	}}
	if !slices.Equal(got, want) {
		t.Errorf("UserPortBindings = %+v, want %+v", got, want)
	}
}

func TestPublishBindings(t *testing.T) {
	mappings := []PortMapping{{HostPort: 3000, GuestPort: 3000}}
	cases := []struct {
		name        string
		serveOnly   bool
		serveHost   int
		wantServe   bool
		wantUserLen int
	}{
		{name: "serve-only with host port", serveOnly: true, serveHost: 4096, wantServe: true, wantUserLen: 1},
		{name: "serve-only without host port", serveOnly: true, serveHost: 0, wantServe: false, wantUserLen: 1},
		{name: "attached mode", serveOnly: false, serveHost: 4096, wantServe: false, wantUserLen: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bindings := PublishBindings(tc.serveOnly, tc.serveHost, mappings)
			_, hasServe := ServeHostBinding(bindings)
			if hasServe != tc.wantServe {
				t.Errorf("serve binding present = %v, want %v", hasServe, tc.wantServe)
			}
			user := UserPortBindings(mappings)
			if got := len(bindings) - boolToInt(hasServe); got != tc.wantUserLen {
				t.Errorf("user bindings = %d, want %d (bindings: %+v, user: %+v)", got, tc.wantUserLen, bindings, user)
			}
		})
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestServeHostBinding(t *testing.T) {
	if _, ok := ServeHostBinding(nil); ok {
		t.Error("ServeHostBinding(nil) reported a binding, want none")
	}
	userOnly := UserPortBindings([]PortMapping{{HostPort: 3000, GuestPort: 3000}})
	if _, ok := ServeHostBinding(userOnly); ok {
		t.Error("ServeHostBinding(user bindings only) reported a binding, want none")
	}
	bindings := append(ServeOnlyBindings(4097), userOnly...)
	if port, ok := ServeHostBinding(bindings); !ok || port != 4097 {
		t.Errorf("ServeHostBinding = %d, %v; want 4097, true", port, ok)
	}
}

func TestBindingsEqualIgnoresOrder(t *testing.T) {
	a := UserPortBindings([]PortMapping{{HostPort: 3000, GuestPort: 3000}, {HostPort: 8080, GuestPort: 5173}})
	b := UserPortBindings([]PortMapping{{HostPort: 8080, GuestPort: 5173}, {HostPort: 3000, GuestPort: 3000}})
	if !BindingsEqual(a, b) {
		t.Error("BindingsEqual(same set, different order) = false, want true")
	}
	if BindingsEqual(a, a[:1]) {
		t.Error("BindingsEqual(different lengths) = true, want false")
	}
	other := UserPortBindings([]PortMapping{{HostPort: 3000, GuestPort: 3001}})
	if BindingsEqual(a, other) {
		t.Error("BindingsEqual(different guest port) = true, want false")
	}
}

func TestFirstFreeHostPortExcludingReachesWindowEdge(t *testing.T) {
	const base = 9000
	// The window is [base, base+ProbeRange); its last port is base+ProbeRange-1.
	if got := firstFreeHostPortExcluding(
		base,
		nil,
		func(port int) bool { return port == base+ProbeRange-1 },
	); got != base+ProbeRange-1 {
		t.Errorf("firstFreeHostPortExcluding = %d, want %d (the window's last port)", got, base+ProbeRange-1)
	}
	if got := firstFreeHostPortExcluding(base, nil, func(port int) bool { return port == base+ProbeRange }); got != 0 {
		t.Errorf("firstFreeHostPortExcluding = %d, want 0 (a port outside the window is not considered)", got)
	}
}

func TestServeHostBindingSkipsZeroHostPort(t *testing.T) {
	zeroHost := []msb.PortBinding{
		{Bind: ServeOnlyBindAddr, HostPort: 0, GuestPort: ServeOnlyBasePort, Protocol: msb.PortProtocolTCP},
	}
	if _, ok := ServeHostBinding(zeroHost); ok {
		t.Error("ServeHostBinding(zero host port) reported a binding, want none")
	}
}

func TestResolveServeHostPortUsesPortOne(t *testing.T) {
	cfg := &msb.SandboxConfig{PortBindings: ServeOnlyBindings(1)}
	if got := ResolveServeHostPort(cfg, true, nil); got != 1 {
		t.Errorf("ResolveServeHostPort = %d, want 1", got)
	}
}

func TestResolveServeHostPort(t *testing.T) {
	if got := ResolveServeHostPort(nil, false, nil); got != 0 {
		t.Errorf("ResolveServeHostPort(not serve-only) = %d, want 0", got)
	}
	cfg := &msb.SandboxConfig{PortBindings: ServeOnlyBindings(4096)}
	if got := ResolveServeHostPort(cfg, true, nil); got != 4096 {
		t.Errorf("ResolveServeHostPort(existing binding) = %d, want 4096", got)
	}
}

func TestResolveServeHostPortProbesWhenNoBinding(t *testing.T) {
	if got := ResolveServeHostPort(nil, true, nil); got == 0 {
		t.Error("ResolveServeHostPort(no config) = 0, want a probed port")
	}
}

func TestResolveServeHostPortPicksAgentBindingAmongUserPorts(t *testing.T) {
	cfg := &msb.SandboxConfig{PortBindings: append(
		UserPortBindings([]PortMapping{{HostPort: 3000, GuestPort: 3000}}),
		ServeOnlyBindings(4097)...,
	)}
	got := ResolveServeHostPort(cfg, true, []PortMapping{{HostPort: 3000, GuestPort: 3000}})
	if got != 4097 {
		t.Errorf("ResolveServeHostPort = %d, want 4097 (the agent guest port binding)", got)
	}
}

// TestResolveServeHostPortKeepsServeBindingBeforeStaleUserBinding covers a live
// VM that still carries a user binding this run no longer maps (the user dropped
// the --port). The stable agent serve port must win over that earlier, unclaimed
// binding.
func TestResolveServeHostPortKeepsServeBindingBeforeStaleUserBinding(t *testing.T) {
	cfg := &msb.SandboxConfig{PortBindings: append(
		UserPortBindings([]PortMapping{{HostPort: 3000, GuestPort: 3000}}),
		ServeOnlyBindings(4097)...,
	)}
	if got := ResolveServeHostPort(cfg, true, nil); got != 4097 {
		t.Errorf("ResolveServeHostPort = %d, want 4097 (the serve binding, not the stale user port)", got)
	}
}

func TestResolveServeHostPortProbesAroundUserPorts(t *testing.T) {
	mappings := []PortMapping{{HostPort: ServeOnlyBasePort, GuestPort: 3000}}
	if got := ResolveServeHostPort(nil, true, mappings); got == ServeOnlyBasePort {
		t.Errorf("ResolveServeHostPort = %d, want a probe above the user-published host port", got)
	}
}

func TestResolveServeHostPortSkipsAgentPortClaimedByNewMapping(t *testing.T) {
	cfg := &msb.SandboxConfig{PortBindings: ServeOnlyBindings(ServeOnlyBasePort)}
	got := ResolveServeHostPort(cfg, true, []PortMapping{{HostPort: ServeOnlyBasePort, GuestPort: 3000}})
	if got == ServeOnlyBasePort {
		t.Errorf("ResolveServeHostPort = %d, want a port the new --port mapping does not claim", got)
	}
}

// TestParsePortMappingsAcceptsValidSpecs covers the success return: valid specs
// are parsed into their mappings rather than rejected.
func TestParsePortMappingsAcceptsValidSpecs(t *testing.T) {
	got, err := ParsePortMappings([]string{"3000", "8080:5173"})
	if err != nil {
		t.Fatalf("ParsePortMappings() error = %v", err)
	}
	want := []PortMapping{
		{HostPort: 3000, GuestPort: 3000},
		{HostPort: 8080, GuestPort: 5173},
	}
	if !slices.Equal(got, want) {
		t.Errorf("ParsePortMappings() = %+v, want %+v", got, want)
	}
}

// TestResolveServeHostPortFallsBackToUntaggedBinding covers the fallback for a
// persisted binding that read back without its guest port: with no serve
// binding to match, the first binding no user port claims is reused.
func TestResolveServeHostPortFallsBackToUntaggedBinding(t *testing.T) {
	cfg := &msb.SandboxConfig{PortBindings: []msb.PortBinding{
		{Bind: ServeOnlyBindAddr, HostPort: 5000, GuestPort: 4000, Protocol: msb.PortProtocolTCP},
	}}
	if got := ResolveServeHostPort(cfg, true, nil); got != 5000 {
		t.Errorf("ResolveServeHostPort = %d, want 5000 (the untagged binding's host port)", got)
	}
}
