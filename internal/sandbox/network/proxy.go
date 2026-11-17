package network

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// ProxyProtocol is a supported outbound-proxy protocol. It mirrors the
// microsandbox SDK's proxy constructors; the SDK carries credentials for
// SOCKS5 and HTTP CONNECT but has no credential support for SOCKS4.
type ProxyProtocol string

const (
	ProxyHTTPConnect ProxyProtocol = "http-connect"
	ProxySOCKS5      ProxyProtocol = "socks5"
	ProxySOCKS4      ProxyProtocol = "socks4"
)

// ParseProxyProtocol parses a proxy protocol, erroring on unknown values.
func ParseProxyProtocol(s string) (ProxyProtocol, error) {
	switch protocol := ProxyProtocol(strings.TrimSpace(s)); protocol {
	case ProxyHTTPConnect, ProxySOCKS5, ProxySOCKS4:
		return protocol, nil
	default:
		return "", fmt.Errorf("unknown proxy protocol %q (want http-connect, socks5, or socks4)", s)
	}
}

// Proxy is a configured outbound proxy. It is value-typed so the derived
// config-key registry recurses into the `network.proxy.*` keys. The zero value
// means no proxy.
type Proxy struct {
	Protocol    ProxyProtocol `mapstructure:"protocol"`
	Address     string        `mapstructure:"address"`
	Username    string        `mapstructure:"username"`
	PasswordEnv string        `mapstructure:"password-env"`
}

// Empty reports whether no proxy is configured (zero value).
func (p Proxy) Empty() bool {
	return p.Protocol == "" && p.Address == "" && p.Username == "" && p.PasswordEnv == ""
}

// ParseProxy parses a `protocol://host:port` proxy string, as accepted by the
// `--proxy` flag. Credentials are not part of the URL; they come from the
// `network.proxy.username`/`password-env` keys.
func ParseProxy(s string) (*Proxy, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("empty proxy: want protocol://host:port")
	}
	protocol, address, found := strings.Cut(s, "://")
	if !found {
		return nil, fmt.Errorf("invalid proxy %q: want protocol://host:port", s)
	}
	parsedProtocol, err := ParseProxyProtocol(protocol)
	if err != nil {
		return nil, err
	}
	address = strings.TrimSpace(address)
	if err := validateProxyAddress(address); err != nil {
		return nil, err
	}
	return &Proxy{Protocol: parsedProtocol, Address: address, Username: "", PasswordEnv: ""}, nil
}

// spec returns the proxy exactly as the launcher network spec carries it: the
// protocol parsed and the address trimmed, so a whitespace-padded configuration
// that reaches the VM identically fingerprints and displays identically. A zero
// proxy yields the zero ProxySpec. It enforces the same rules as validate.
func (p Proxy) spec() (msb.ProxySpec, error) {
	if p.Empty() {
		return msb.ProxySpec{}, nil
	}
	if err := p.validate(); err != nil {
		return msb.ProxySpec{}, err
	}
	protocol, err := ParseProxyProtocol(string(p.Protocol))
	if err != nil {
		return msb.ProxySpec{}, err
	}
	return msb.ProxySpec{
		Protocol:    msb.ProxyProtocol(protocol),
		Address:     strings.TrimSpace(p.Address),
		Username:    p.Username,
		PasswordEnv: p.PasswordEnv,
	}, nil
}

// validate checks a configured proxy: known protocol, a well-formed host:port
// address, and no credentials when the protocol cannot carry them. It reports
// only the real configuration rules; the composition with the egress policy is
// not a validity concern (the runtime dials the proxy host-side).
func (p Proxy) validate() error {
	if p.Empty() {
		return nil
	}
	protocol, err := ParseProxyProtocol(string(p.Protocol))
	if err != nil {
		return err
	}
	if err := validateProxyAddress(p.Address); err != nil {
		return err
	}
	if protocol == ProxySOCKS4 && (p.Username != "" || p.PasswordEnv != "") {
		return errors.New(
			"network.proxy: protocol socks4 carries no credentials; " +
				"remove username/password-env or use socks5 or http-connect",
		)
	}
	// The SDK's Credentials needs both halves, so a lone credential would reach
	// the VM as an anonymous proxy while still changing the fingerprint. Reject
	// it rather than dropping either half.
	if (p.Username == "") != (p.PasswordEnv == "") {
		return errors.New("network.proxy: username and password-env must be set together")
	}
	return nil
}

// validateProxyAddress requires a host:port with a host and a port in 1-65535,
// mirroring the DNS-server address contract.
func validateProxyAddress(address string) error {
	address = strings.TrimSpace(address)
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid proxy address %q: want host:port", address)
	}
	if host == "" {
		return fmt.Errorf("invalid proxy address %q: missing host", address)
	}
	if port == "" {
		return fmt.Errorf("invalid proxy address %q: missing port", address)
	}
	// strconv.Atoi returns 0 on a syntax error and MaxInt on out-of-range
	// input, so the range check alone rejects every invalid value.
	portNumber, _ := strconv.Atoi(port)
	if portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("invalid proxy address %q: invalid port %q", address, port)
	}
	return nil
}
