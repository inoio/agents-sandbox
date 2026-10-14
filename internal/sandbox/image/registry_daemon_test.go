//go:build integration

package image

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/builder/cacheregistry"
)

// registryDaemonChildEnv marks a process that is a re-exec'd registry daemon, so
// the guard test does not recurse if the dispatch under test ever breaks.
const registryDaemonChildEnv = "AGENTS_SANDBOX_TEST_REGISTRY_CHILD"

// TestTestBinaryServesRegistryDaemon guards the helper-process dispatch in
// TestMain: under `go test` the registry spawner re-execs os.Executable(), which
// is this test binary rather than the agents-sandbox CLI, so the hidden
// __builder-registry command must be served here. Without the dispatch the child
// re-runs the test suite and the daemon never becomes ready, which presents as
// integration builds failing to acquire the cache registry.
func TestTestBinaryServesRegistryDaemon(t *testing.T) {
	if os.Getenv(registryDaemonChildEnv) == "1" {
		t.Skip("registry daemon child process")
	}

	port := freeLoopbackPort(t)
	configPath := writeRegistryDaemonConfig(t, port, "dispatch-guard-token")

	cmd := exec.Command(os.Args[0], cacheregistry.CommandName, "--config", configPath)
	cmd.Env = append(os.Environ(), registryDaemonChildEnv+"=1")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start registry daemon child: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	address := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if !waitForRegistryDaemon(t, address, "dispatch-guard-token") {
		t.Fatal("test binary did not serve the registry daemon on the hidden __builder-registry command")
	}
}

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

func writeRegistryDaemonConfig(t *testing.T, port int, token string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "registry-config.json")
	config, err := json.Marshal(map[string]any{
		"root":        filepath.Join(t.TempDir(), "cache"),
		"host":        "127.0.0.1",
		"port":        port,
		"token":       token,
		"idleTimeout": 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("encode registry config: %v", err)
	}
	if err := os.WriteFile(path, config, 0o600); err != nil {
		t.Fatalf("write registry config: %v", err)
	}
	return path
}

func waitForRegistryDaemon(t *testing.T, address, token string) bool {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, address+"/v2/", nil)
		if err != nil {
			t.Fatalf("build health request: %v", err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}
