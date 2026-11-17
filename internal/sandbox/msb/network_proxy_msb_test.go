//go:build msb

package msb

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest/runtime"
)

// TestRealOutboundProxyTraversal proves against a real VM that a configured
// outbound proxy is what carries guest egress, and that the launcher needs no
// egress-allow entry for the proxy address itself (#149; the composition rule in
// specs/network-and-secrets/interception-and-proxy.md §Composition with the
// egress policy).
//
// The test creates a `none` sandbox whose only egress-allow entry is the
// destination, deliberately omitting the proxy address from the allow set, then
// has the guest connect to the destination. The runtime dials the proxy
// host-side, so an in-process SOCKS5 listener on the host loopback receives the
// CONNECT without any guest egress rule for its address: egress reached the
// configured proxy with no `egress-allow` entry for the proxy address, which
// proves the host-side dial needs none.
//
// What this does not prove — that the proxy forwards to the real upstream — is
// the human-accept step against a real proxy (the mitmdump run recorded in the
// PR). The listener here completes the handshake and discards the payload, so
// the test pins only that egress traversed the configured proxy.
func TestRealOutboundProxyTraversal(t *testing.T) {
	runtime.EnsureIsolatedRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	proxyAddress, observed := startSOCKS5Recorder(t)

	const (
		destinationHost = "203.0.113.10"
		destinationPort = 80
	)
	// `none` (no composable profile) allows only the destination. The proxy
	// address is absent from the allow set on purpose: the traversal below
	// proves the runtime reached it anyway, i.e. the host-side dial needs no
	// egress-allow entry for the proxy. DisableStrict lets the plain
	// destination allow match without TLS interception.
	spec := NetworkSpec{
		EgressAllow:   []string{destinationHost},
		DisableStrict: true,
		Proxy:         ProxySpec{Protocol: ProxySOCKS5, Address: proxyAddress},
	}
	const sandboxName = "agents-sandbox-proxy-egress"
	sandbox := createSandboxWithPolicy(t, sandboxName, spec)

	// A diagnostic only: the runtime's stored config is logged to help the
	// human run, but is not asserted on. Its shape is unverified, and a
	// normalization that dropped the proxy from it would not mean the
	// composition contract failed.
	if handle, err := msbSdk.GetSandbox(ctx, sandboxName); err == nil {
		t.Logf("sandbox config: %s", handle.ConfigJSON())
	} else {
		t.Logf("GetSandbox(%q) for the config diagnostic: %v", sandboxName, err)
	}

	// The guest opens a TCP connection to the destination; the runtime must
	// relay it through the proxy. The command is best-effort — the recorder
	// never forwards, so the guest connection ends in a timeout regardless.
	guestCommand := fmt.Sprintf(
		"printf 'GET / HTTP/1.0\\r\\n\\r\\n' | nc -w 5 %s %d; true",
		destinationHost, destinationPort,
	)
	output, err := sandbox.Shell(ctx, guestCommand)
	if err != nil {
		t.Fatalf("guest connection to %s:%d: %v", destinationHost, destinationPort, err)
	}
	t.Logf(
		"guest command %q (exit %d): %s",
		guestCommand, output.ExitCode(), strings.TrimSpace(output.Stdout()),
	)

	select {
	case got := <-observed:
		if got.host != destinationHost || got.port != destinationPort {
			t.Errorf("proxy CONNECT = %s:%d, want %s:%d", got.host, got.port, destinationHost, destinationPort)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf(
			"the runtime never dialed the configured proxy: the guest command produced no observer record "+
				"(guest command %q exited %d; the proxy was not used, or nc could not reach %s:%d)",
			guestCommand, output.ExitCode(), destinationHost, destinationPort,
		)
	}
}

// observedSOCKS5Connect is one SOCKS5 CONNECT destination the recorder captured.
type observedSOCKS5Connect struct {
	host string
	port uint16
}

// startSOCKS5Recorder listens on the host loopback and reports the address it
// bound. Every SOCKS5 CONNECT it accepts is answered with a success so the
// dialing runtime keeps the tunnel open; the recorder never forwards, because
// the test only asserts that the runtime chose the proxy at all.
func startSOCKS5Recorder(t *testing.T) (string, <-chan observedSOCKS5Connect) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for the SOCKS5 recorder: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	observed := make(chan observedSOCKS5Connect, 16)
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go recordSOCKS5Connect(conn, observed)
		}
	}()
	return listener.Addr().String(), observed
}

// recordSOCKS5Connect completes a no-auth SOCKS5 greeting and CONNECT on conn,
// reports the requested destination, then holds the tunnel until the client
// closes it.
func recordSOCKS5Connect(conn net.Conn, observed chan<- observedSOCKS5Connect) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Minute))
	if err := socks5Greeting(conn); err != nil {
		return
	}
	destination, err := socks5ConnectRequest(conn)
	if err != nil {
		return
	}
	select {
	case observed <- destination:
	default:
	}
	// Reply success with a placeholder bound address (0.0.0.0:0); the tunnel's
	// payload never reaches a real destination.
	_, _ = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	_, _ = io.Copy(io.Discard, conn)
}

// socks5Greeting reads the client's method-negotiation message and selects
// "no authentication".
func socks5Greeting(conn net.Conn) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if header[0] != 0x05 {
		return fmt.Errorf("unexpected SOCKS version %d", header[0])
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}
	_, err := conn.Write([]byte{0x05, 0x00})
	return err
}

// socks5ConnectRequest reads a SOCKS5 request and returns the CONNECT
// destination. Only CONNECT (0x01) is accepted; any other command is an error.
func socks5ConnectRequest(conn net.Conn) (observedSOCKS5Connect, error) {
	var destination observedSOCKS5Connect
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return destination, err
	}
	if header[0] != 0x05 || header[1] != 0x01 {
		return destination, fmt.Errorf("unsupported SOCKS5 request %d/%d", header[0], header[1])
	}
	host, err := socks5Address(conn, header[3])
	if err != nil {
		return destination, err
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(conn, port); err != nil {
		return destination, err
	}
	destination.host = host
	destination.port = uint16(port[0])<<8 | uint16(port[1])
	return destination, nil
}

// socks5Address reads the address field a SOCKS5 request carries, decoding the
// IPv4, domain-name, and IPv6 address types.
func socks5Address(conn net.Conn, addressType byte) (string, error) {
	switch addressType {
	case 0x01: // IPv4
		address := make([]byte, 4)
		if _, err := io.ReadFull(conn, address); err != nil {
			return "", err
		}
		return net.IP(address).String(), nil
	case 0x03: // domain name
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return "", err
		}
		domain := make([]byte, int(length[0]))
		if _, err := io.ReadFull(conn, domain); err != nil {
			return "", err
		}
		return string(domain), nil
	case 0x04: // IPv6
		address := make([]byte, 16)
		if _, err := io.ReadFull(conn, address); err != nil {
			return "", err
		}
		return net.IP(address).String(), nil
	default:
		return "", fmt.Errorf("unsupported SOCKS5 address type %d", addressType)
	}
}
