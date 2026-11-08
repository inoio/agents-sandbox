package msb

// PortProtocol identifies the protocol for an exposed sandbox service.
type PortProtocol string

const (
	PortProtocolTCP PortProtocol = "tcp"
	PortProtocolUDP PortProtocol = "udp"
)

// PortBinding describes how to expose a service running in the sandbox on a
// specific host address and port. Protocol defaults to TCP when empty.
//
//exhaustruct:ignore
type PortBinding struct {
	Bind      string
	HostPort  uint16
	GuestPort uint16
	Protocol  PortProtocol
}
