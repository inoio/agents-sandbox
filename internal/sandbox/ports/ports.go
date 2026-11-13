// Package ports holds the published-port grammar and the residency rules for
// port bindings on a project VM: parsing user specs, probing free host ports,
// and composing the binding list the VM is created with. It mirrors
// internal/sandbox/network as the self-contained domain layer for ports, so
// viperconfig can validate specs without importing the options package.
package ports

import (
	"cmp"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// ServeOnlyBindAddr is the loopback address every published port binds to,
// including the serve-only agent binding.
const ServeOnlyBindAddr = "127.0.0.1"

// ServeOnlyBasePort is the lowest host port probed when publishing the agent
// server in serve-only mode, and the guest port the agents serve on.
const ServeOnlyBasePort = 4096

// PortMapping is one user-published port: a guest port reachable from the
// host loopback, optionally under a different host port.
type PortMapping struct {
	HostPort  int
	GuestPort int
}

// ParsePortMapping parses a publish spec: "<port>" publishes the guest port
// under the same host port, "<host>:<guest>" under a different one.
func ParsePortMapping(spec string) (PortMapping, error) {
	trimmed := strings.TrimSpace(spec)
	parts := strings.Split(trimmed, ":")
	if len(parts) > 2 {
		return PortMapping{}, fmt.Errorf("invalid port spec %q: want <port> or <host>:<guest>", spec)
	}
	hostSpec, guestSpec := parts[0], parts[0]
	if len(parts) == 2 {
		guestSpec = parts[1]
	}
	hostPort, err := parsePortPart(spec, hostSpec, "host")
	if err != nil {
		return PortMapping{}, err
	}
	guestPort, err := parsePortPart(spec, guestSpec, "guest")
	if err != nil {
		return PortMapping{}, err
	}
	return PortMapping{HostPort: hostPort, GuestPort: guestPort}, nil
}

// parsePortPart parses and range-checks one side of a publish spec.
func parsePortPart(spec, part, side string) (int, error) {
	port, err := strconv.Atoi(part)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port spec %q: %s port must be between 1 and 65535", spec, side)
	}
	return port, nil
}

// ParsePortMappings parses publish specs, rejecting duplicate host ports.
func ParsePortMappings(specs []string) ([]PortMapping, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	mappings := make([]PortMapping, 0, len(specs))
	seen := make(map[int]struct{}, len(specs))
	for _, spec := range specs {
		mapping, err := ParsePortMapping(spec)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[mapping.HostPort]; duplicate {
			return nil, fmt.Errorf("duplicate host port %d in %q", mapping.HostPort, spec)
		}
		seen[mapping.HostPort] = struct{}{}
		mappings = append(mappings, mapping)
	}
	return mappings, nil
}

// HostPorts returns the host-side ports of the mappings.
func HostPorts(mappings []PortMapping) []int {
	hostPorts := make([]int, 0, len(mappings))
	for _, mapping := range mappings {
		hostPorts = append(hostPorts, mapping.HostPort)
	}
	return hostPorts
}

// probeHostPort reports whether the loopback address can bind port. It is a
// test seam so tests need not touch a real socket.
var probeHostPort = func(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", ServeOnlyBindAddr, port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// ProbeRange is how many ports above base the free-port probes consider.
const ProbeRange = 1024

// FirstFreeHostPortExcluding returns the first host port at or above base that
// is not currently bound on the loopback address and not in excluded, or 0
// when every candidate is taken. Returning 0 rather than a taken port keeps an
// exhausted range loud at the caller instead of silently colliding.
func FirstFreeHostPortExcluding(base int, excluded []int) int {
	return firstFreeHostPortExcluding(base, excluded, probeHostPort)
}

// firstFreeHostPortExcluding is FirstFreeHostPortExcluding with the bind probe
// as a seam so tests need not bind sockets.
func firstFreeHostPortExcluding(base int, excluded []int, free func(int) bool) int {
	skip := make(map[int]struct{}, len(excluded))
	for _, port := range excluded {
		skip[port] = struct{}{}
	}
	for port := base; port < base+ProbeRange; port++ {
		if _, blocked := skip[port]; blocked {
			continue
		}
		if free(port) {
			return port
		}
	}
	return 0
}

// ServeOnlyBindings returns the launcher port binding for serve-only mode with
// the given host port, mapping it to the fixed guest port.
func ServeOnlyBindings(hostPort int) []msb.PortBinding {
	return []msb.PortBinding{
		//nolint:gosec // G115: host ports are well below the uint16 max
		{
			Bind:      ServeOnlyBindAddr,
			HostPort:  uint16(hostPort),
			GuestPort: ServeOnlyBasePort,
			Protocol:  msb.PortProtocolTCP,
		},
	}
}

// UserPortBindings returns the launcher port bindings for the user-published
// mappings, each on the host loopback.
func UserPortBindings(mappings []PortMapping) []msb.PortBinding {
	bindings := make([]msb.PortBinding, 0, len(mappings))
	for _, mapping := range mappings {
		//nolint:gosec // G115: host ports are well below the uint16 max
		bindings = append(bindings, msb.PortBinding{
			Bind:      ServeOnlyBindAddr,
			HostPort:  uint16(mapping.HostPort),
			GuestPort: uint16(mapping.GuestPort),
			Protocol:  msb.PortProtocolTCP,
		})
	}
	return bindings
}

// PublishBindings composes the complete port-binding list for a project VM:
// the serve-only agent binding (when serving, with a resolved host port) plus
// the user-published mappings. It is the single composer both the reconfig
// plan and VM creation build their binding lists from, so the equality check
// and the creation path cannot drift apart.
func PublishBindings(serveOnly bool, serveHostPort int, mappings []PortMapping) []msb.PortBinding {
	var bindings []msb.PortBinding
	if serveOnly && serveHostPort != 0 {
		bindings = append(bindings, ServeOnlyBindings(serveHostPort)...)
	}
	return append(bindings, UserPortBindings(mappings)...)
}

// ServeHostBinding returns the host port the binding list publishes for the
// serve-only agent guest port, and whether such a binding exists.
func ServeHostBinding(bindings []msb.PortBinding) (int, bool) {
	for _, pb := range bindings {
		if pb.GuestPort == ServeOnlyBasePort && pb.HostPort != 0 {
			return int(pb.HostPort), true
		}
	}
	return 0, false
}

// LiveServeHostPort returns the host port an existing VM's bindings already
// publish for the serve-only agent server, and whether they carry one. It is
// the live-config half of ResolveServeHostPort, which reuses its fallback
// before probing: a tag-matched serve binding, or — for a binding that records
// no guest port — the first such binding that no user-published host port
// claims. A binding that names another guest port targets a different service
// and is never accepted. Unlike ResolveServeHostPort it never probes, so ok
// false means the VM publishes no candidate, not that a fresh port could be
// allocated.
func LiveServeHostPort(bindings []msb.PortBinding, mappings []PortMapping) (int, bool) {
	if port, ok := ServeHostBinding(bindings); ok {
		return port, true
	}
	return untaggedServeHostPort(bindings, mappings)
}

// untaggedServeHostPort returns the first live binding whose guest port was not
// recorded and whose host port no user-published mapping claims. A binding that
// names another guest port is known to target a different service, never the
// agent server, so it is rejected rather than mistaken for the serve binding.
func untaggedServeHostPort(bindings []msb.PortBinding, mappings []PortMapping) (int, bool) {
	published := HostPorts(mappings)
	for _, pb := range bindings {
		if pb.GuestPort != 0 || pb.HostPort == 0 || slices.Contains(published, int(pb.HostPort)) {
			continue
		}
		return int(pb.HostPort), true
	}
	return 0, false
}

// BindingsEqual reports whether two binding lists carry the same bindings,
// ignoring order. It is the single comparison authority for published ports.
func BindingsEqual(a, b []msb.PortBinding) bool {
	if len(a) != len(b) {
		return false
	}
	return slices.Equal(sortBindings(a), sortBindings(b))
}

// sortBindings returns bindings ordered deterministically so publication drift
// compares sets rather than list order.
func sortBindings(bindings []msb.PortBinding) []msb.PortBinding {
	sorted := slices.Clone(bindings)
	slices.SortFunc(sorted, func(a, b msb.PortBinding) int {
		return cmp.Or(
			cmp.Compare(a.HostPort, b.HostPort),
			cmp.Compare(a.GuestPort, b.GuestPort),
			cmp.Compare(a.Bind, b.Bind),
			cmp.Compare(a.Protocol, b.Protocol),
		)
	})
	return sorted
}

// ResolveServeHostPort returns the host port to publish for serve-only mode:
// the port an existing VM already publishes for the agent guest port (kept
// stable across runs), or the first free host port at or above
// ServeOnlyBasePort that no user-published port takes. It returns 0 when not
// serving, and when no free port remains in the probe range.
func ResolveServeHostPort(cfg *msb.SandboxConfig, serveOnly bool, mappings []PortMapping) int {
	if !serveOnly {
		return 0
	}
	if cfg != nil {
		if port, ok := ServeHostBinding(cfg.PortBindings); ok && !slices.Contains(HostPorts(mappings), port) {
			return port
		}
		// A published binding can read back without its guest port, so fall
		// back to a binding that records no guest port and that no
		// user-published host port claims; a binding naming another guest port
		// is a different service.
		if port, ok := untaggedServeHostPort(cfg.PortBindings, mappings); ok {
			return port
		}
	}
	return FirstFreeHostPortExcluding(ServeOnlyBasePort, HostPorts(mappings))
}
