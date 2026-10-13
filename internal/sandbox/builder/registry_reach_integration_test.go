//go:build integration

package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/builder/cacheregistry"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// TestBuilderVMReachesEmbeddedRegistry proves the bind + reachability invariant
// the registry-backed cache depends on: the embedded cache registry binds the
// host loopback (never 0.0.0.0), and the builder VM reaches it through the
// sandbox host alias at the exact host:port the BuildKit cache ref uses.
// Without this, warm builds can never reuse the registry cache.
//
// It drives the real Serve bind path (an empty configured host falls back to
// registryBindHost, i.e. loopback) and probes reachability from inside the
// builder VM with a plain TCP connect. Run on a KVM host via
// `make test-integration-msb`.
func TestBuilderVMReachesEmbeddedRegistry(t *testing.T) {
	requireMsbIntegrationHost(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	if _, err := newVM().Ensure(ctx); err != nil {
		t.Fatalf("ensure builder VM: %v", err)
	}

	port := freePortOn(t, "127.0.0.1")
	configPath := writeRegistryProbeConfig(t, port)
	address := net.JoinHostPort(cacheregistry.GuestHost, strconv.Itoa(port))

	serveErr := make(chan error, 1)
	go func() { serveErr <- cacheregistry.Serve(ctx, configPath) }()

	probe := fmt.Sprintf("nc -z -w 5 %s %d", cacheregistry.GuestHost, port)
	if reached, status := waitForGuestReach(ctx, t, probe, serveErr); !reached {
		t.Fatalf(
			"builder VM could not reach the embedded registry at %s; "+
				"the loopback bind is not reachable through the sandbox host alias (%s)",
			address, status,
		)
	}
}

// requireMsbIntegrationHost skips a test that needs a real msb runtime unless
// the integration environment is armed and the msb backend is selected.
func requireMsbIntegrationHost(t *testing.T) {
	t.Helper()
	if os.Getenv("AGENTS_SANDBOX_INTEGRATION") == "" {
		t.Skipf("integration test: set AGENTS_SANDBOX_INTEGRATION=1 on a KVM host with msb")
	}
	configpaths.WithMockConfigPaths(t)
	if SelectedName() != "msb" {
		t.Skip("msb-only test: requires the microsandbox backend")
	}
}

// freePortOn returns a currently free port on host, the interface the registry
// actually serves on.
func freePortOn(t *testing.T, host string) int {
	t.Helper()
	listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatalf("find free registry port on %s: %v", host, err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

// writeRegistryProbeConfig writes a Serve config that pins only the port; the
// empty host makes Serve fall back to the production registryBindHost (loopback).
func writeRegistryProbeConfig(t *testing.T, port int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "registry-config.json")
	config, err := json.Marshal(map[string]any{
		"root":  filepath.Join(t.TempDir(), "cache"),
		"port":  port,
		"token": "integration-probe-token",
	})
	if err != nil {
		t.Fatalf("encode registry config: %v", err)
	}
	if err := os.WriteFile(path, config, 0o600); err != nil {
		t.Fatalf("write registry config: %v", err)
	}
	return path
}

// waitForGuestReach polls the builder VM until it can reach probe or the budget
// expires, failing fast if the registry could not bind. It returns whether the
// VM reached the registry and a human-readable last status.
func waitForGuestReach(
	ctx context.Context,
	t *testing.T,
	probe string,
	serveErr <-chan error,
) (bool, string) {
	t.Helper()
	handle, err := msb.Get().GetSandbox(ctx, builderSandboxName())
	if err != nil {
		t.Fatalf("get builder sandbox: %v", err)
	}
	sb, err := handle.Connect(ctx)
	if err != nil {
		t.Fatalf("connect builder sandbox: %v", err)
	}
	defer func() { _ = sb.Close() }()

	deadline := time.Now().Add(20 * time.Second)
	status := "no probe completed"
	for time.Now().Before(deadline) {
		select {
		case err := <-serveErr:
			t.Fatalf("cache registry failed to bind: %v", err)
		default:
		}
		result, shellErr := sb.Shell(ctx, probe)
		switch {
		case shellErr != nil:
			status = fmt.Sprintf("shell error: %v", shellErr)
		case result.Success():
			return true, "reachable"
		default:
			status = fmt.Sprintf("exit=%d stderr=%q", result.ExitCode(), result.Stderr())
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false, status
}
