//go:build integration

package builder

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// TestMicrosandboxHostAddressDiagnostic is a temporary diagnostic for the
// embedded registry bind. It creates an ordinary sandbox (default network pool,
// AllowAll), binds a wildcard host listener, and has the sandbox connect to
// `host.microsandbox.internal:<port>`; the accepted connection's local address
// is the host IP the guest reaches, which is the address the cache registry must
// bind. It then confirms a listener bound only on that address is reachable.
//
// Run on a KVM host:
//
//	AGENTS_SANDBOX_INTEGRATION=1 AGENTS_SANDBOX_BUILDER=msb CGO_ENABLED=1 \
//	  go test -tags integration -run TestMicrosandboxHostAddressDiagnostic -count=1 -v \
//	  ./internal/sandbox/builder/
func TestMicrosandboxHostAddressDiagnostic(t *testing.T) {
	if os.Getenv("AGENTS_SANDBOX_INTEGRATION") == "" {
		t.Skip("integration diagnostic: set AGENTS_SANDBOX_INTEGRATION=1 on a KVM host with msb")
	}
	configpaths.WithMockConfigPaths(t)
	ctx := context.Background()

	name := "agents-sandbox-netdiag"
	opts := []msbSdk.SandboxOption{
		msbSdk.WithImage(builderImage),
		msbSdk.WithNetwork(msbSdk.NetworkPolicy.AllowAll()),
		msbSdk.WithDetached(),
		msbSdk.WithReplace(),
		msbSdk.WithIdleTimeout(60 * time.Second),
	}
	sb, err := msb.Get().CreateSandbox(ctx, name, opts...)
	if err != nil {
		t.Fatalf("create diagnostic sandbox: %v", err)
	}
	t.Cleanup(func() { _ = msb.Get().RemoveSandbox(context.Background(), name) })

	logGuestNetworking(ctx, t, sb)

	hostIP := discoverHostAddress(ctx, t, sb)
	t.Logf("guest reaches the host at %s", hostIP)
	t.Logf("host interface addresses: %v", hostInterfaceAddresses())

	confirmConfinedBind(ctx, t, sb, hostIP)
}

// logGuestNetworking prints the guest's host alias resolution and routes.
func logGuestNetworking(ctx context.Context, t *testing.T, sb msb.Sandbox) {
	t.Helper()
	for _, command := range []string{
		"cat /etc/hosts",
		"cat /etc/resolv.conf",
		"nslookup host.microsandbox.internal",
		"ip -4 addr 2>/dev/null || cat /proc/net/fib_trie",
		"ip route 2>/dev/null || cat /proc/net/route",
	} {
		result, err := sb.Shell(ctx, command)
		if err != nil {
			t.Logf("$ %s\n  error: %v", command, err)
			continue
		}
		t.Logf("$ %s\n%s%s", command, result.Stdout(), result.Stderr())
	}
}

// discoverHostAddress binds a wildcard listener, has the sandbox connect to
// host.microsandbox.internal, and returns the local address of the accepted
// connection — the host IP the guest targets.
func discoverHostAddress(ctx context.Context, t *testing.T, sb msb.Sandbox) string {
	t.Helper()
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("bind wildcard listener: %v", err)
	}
	defer func() { _ = listener.Close() }()
	port := listener.Addr().(*net.TCPAddr).Port

	accepted := make(chan net.Addr, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		accepted <- conn.LocalAddr()
		_ = conn.Close()
	}()

	probe := fmt.Sprintf("nc -z -w 5 host.microsandbox.internal %d", port)
	result, err := sb.Shell(ctx, probe)
	if err != nil {
		t.Fatalf("guest probe %q: %v", probe, err)
	}
	t.Logf("guest probe %q: exit=%d stderr=%q", probe, result.ExitCode(), result.Stderr())

	select {
	case addr := <-accepted:
		return addr.(*net.TCPAddr).IP.String()
	case <-time.After(10 * time.Second):
		t.Fatalf("guest never connected to host.microsandbox.internal:%d", port)
		return ""
	}
}

// confirmConfinedBind binds a listener on hostIP only and verifies the sandbox
// reaches it via host.microsandbox.internal — the exact confinement we want.
func confirmConfinedBind(ctx context.Context, t *testing.T, sb msb.Sandbox, hostIP string) {
	t.Helper()
	listener, err := net.Listen("tcp", net.JoinHostPort(hostIP, "0"))
	if err != nil {
		t.Fatalf("bind %s only: %v", hostIP, err)
	}
	defer func() { _ = listener.Close() }()
	port := listener.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	probe := fmt.Sprintf("nc -z -w 5 host.microsandbox.internal %s", strconv.Itoa(port))
	result, err := sb.Shell(ctx, probe)
	if err != nil {
		t.Fatalf("guest probe confined bind: %v", err)
	}
	t.Logf("confined bind on %s:%d reachable from guest: %t (exit=%d stderr=%q)",
		hostIP, port, result.Success(), result.ExitCode(), result.Stderr())
}

// hostInterfaceAddresses lists the host's non-loopback IPv4 addresses.
func hostInterfaceAddresses() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() || ipNet.IP.To4() == nil {
			continue
		}
		out = append(out, ipNet.IP.String())
	}
	return out
}
