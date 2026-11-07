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

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
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

// ServeOnlyBindings returns the msb port binding for serve-only mode with the
// given host port, mapping it to the fixed guest port.
func ServeOnlyBindings(hostPort int) []msbSdk.PortBinding {
	return []msbSdk.PortBinding{
		//nolint:gosec // G115: host ports are well below the uint16 max
		{
			Bind:      ServeOnlyBindAddr,
			HostPort:  uint16(hostPort),
			GuestPort: ServeOnlyBasePort,
			Protocol:  msbSdk.PortProtocolTCP,
		},
	}
}

// UserPortBindings returns the msb port bindings for the user-published
// mappings, each on the host loopback.
func UserPortBindings(mappings []PortMapping) []msbSdk.PortBinding {
	bindings := make([]msbSdk.PortBinding, 0, len(mappings))
	for _, mapping := range mappings {
		//nolint:gosec // G115: host ports are well below the uint16 max
		bindings = append(bindings, msbSdk.PortBinding{
			Bind:      ServeOnlyBindAddr,
			HostPort:  uint16(mapping.HostPort),
			GuestPort: uint16(mapping.GuestPort),
			Protocol:  msbSdk.PortProtocolTCP,
		})
	}
	return bindings
}

// PublishBindings composes the complete port-binding list for a project VM:
// the serve-only agent binding (when serving, with a resolved host port) plus
// the user-published mappings. It is the single composer both the reconfig
// plan and VM creation build their binding lists from, so the equality check
// and the creation path cannot drift apart.
func PublishBindings(serveOnly bool, serveHostPort int, mappings []PortMapping) []msbSdk.PortBinding {
	var bindings []msbSdk.PortBinding
	if serveOnly && serveHostPort != 0 {
		bindings = append(bindings, ServeOnlyBindings(serveHostPort)...)
	}
	return append(bindings, UserPortBindings(mappings)...)
}

// ServeHostBinding returns the host port the binding list publishes for the
// serve-only agent guest port, and whether such a binding exists.
func ServeHostBinding(bindings []msbSdk.PortBinding) (int, bool) {
	for _, pb := range bindings {
		if pb.GuestPort == ServeOnlyBasePort && pb.HostPort != 0 {
			return int(pb.HostPort), true
		}
	}
	return 0, false
}

// BindingsEqual reports whether two binding lists carry the same bindings,
// ignoring order. It is the single comparison authority for published ports.
func BindingsEqual(a, b []msbSdk.PortBinding) bool {
	if len(a) != len(b) {
		return false
	}
	return slices.Equal(sortBindings(a), sortBindings(b))
}

// sortBindings returns bindings ordered deterministically so publication drift
// compares sets rather than list order.
func sortBindings(bindings []msbSdk.PortBinding) []msbSdk.PortBinding {
	sorted := slices.Clone(bindings)
	slices.SortFunc(sorted, func(a, b msbSdk.PortBinding) int {
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
func ResolveServeHostPort(cfg *msbSdk.SandboxConfig, serveOnly bool, mappings []PortMapping) int {
	if !serveOnly {
		return 0
	}
	published := HostPorts(mappings)
	if cfg != nil {
		if port, ok := ServeHostBinding(cfg.PortBindings); ok && !slices.Contains(published, port) {
			return port
		}
		// A published binding can read back without its guest port, so fall
		// back to the first binding no user-published host port claims: the
		// serve binding is the only other one a VM carries.
		for _, pb := range cfg.PortBindings {
			if pb.HostPort != 0 && !slices.Contains(published, int(pb.HostPort)) {
				return int(pb.HostPort)
			}
		}
	}
	return FirstFreeHostPortExcluding(ServeOnlyBasePort, published)
}
