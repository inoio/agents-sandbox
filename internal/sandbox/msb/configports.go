package msb

import (
	"encoding/json"
	"fmt"
	"strings"
)

// persistedConfigPorts mirrors the `network.ports` array of a persisted sandbox
// config. The runtime stores a SandboxSpec whose NetworkSpec carries published
// ports as PublishedPortSpec values (snake_case fields, the protocol spelled as
// the Rust variant name), while the Go SDK's SandboxConfig.UnmarshalJSON drops
// them: Config().PortBindings is empty on every read-back. See
// .operator-shared/specs/vm/live-mount-readback.md.
type persistedConfigPorts struct {
	Network persistedNetworkPorts `json:"network"`
}

// persistedNetworkPorts is the NetworkSpec subdocument; only its ports are
// read back here.
type persistedNetworkPorts struct {
	Ports []persistedPort `json:"ports"`
}

// persistedPort is one PublishedPortSpec entry: host_port and guest_port are
// u16, the protocol defaults to TCP when absent, host_bind defaults to the
// loopback address.
type persistedPort struct {
	HostPort  uint16 `json:"host_port"`
	GuestPort uint16 `json:"guest_port"`
	Protocol  string `json:"protocol"`
	HostBind  string `json:"host_bind"`
}

// ParseConfigPorts decodes the published port bindings a persisted sandbox
// config records. It is the launcher's source of truth for a live VM's port
// bindings, because Config() cannot supply them.
//
// A config without `network.ports` yields no bindings and no error: a record
// that publishes nothing is valid and must not read as "every port changed".
// An entry whose protocol this launcher does not know is skipped, mirroring
// ParseConfigMounts' treatment of unknown mount variants.
func ParseConfigPorts(configJSON string) ([]PortBinding, error) {
	var persisted persistedConfigPorts
	if err := json.Unmarshal([]byte(configJSON), &persisted); err != nil {
		return nil, fmt.Errorf("parsing persisted sandbox config: %w", err)
	}
	bindings := make([]PortBinding, 0, len(persisted.Network.Ports))
	for _, entry := range persisted.Network.Ports {
		protocol, known := portProtocol(entry.Protocol)
		if !known {
			continue
		}
		bindings = append(bindings, PortBinding{
			Bind:      entry.HostBind,
			HostPort:  entry.HostPort,
			GuestPort: entry.GuestPort,
			Protocol:  protocol,
		})
	}
	return bindings, nil
}

// portProtocol maps the persisted protocol onto the launcher constant. Upstream
// serializes the Rust variant name ("Tcp", "Udp"); the launcher constants are
// the lowercase spellings. An absent protocol is TCP, upstream's serde default.
func portProtocol(persisted string) (PortProtocol, bool) {
	switch strings.ToLower(persisted) {
	case "", "tcp":
		return PortProtocolTCP, true
	case "udp":
		return PortProtocolUDP, true
	default:
		return "", false
	}
}
