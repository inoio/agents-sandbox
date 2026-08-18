package session

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

const (
	daemonHealthURL = "http://127.0.0.1:4096/global/health"
	daemonKillCmd   = "pkill -f 'opencode serve' || true"
)

// daemonStartCommand builds the shell command that starts the opencode serve
// daemon inside the VM. In serve-only mode it binds 0.0.0.0 so microsandbox's
// published-port forwarder (which dials the guest's external interface address)
// can reach the server; otherwise it binds loopback only.
func daemonStartCommand(serveOnly bool) string {
	hostname := "127.0.0.1"
	if serveOnly {
		hostname = "0.0.0.0"
	}
	return fmt.Sprintf(
		"nohup opencode serve --hostname %s --port %s > /tmp/opencode-serve.log 2>&1 &",
		hostname, options.ServeOnlyPort,
	)
}

var daemonReadyTimeout = 60 * time.Second //nolint:gochecknoglobals // test seam, swapped in tests

var daemonPollInterval = 2 * time.Second //nolint:gochecknoglobals // test seam, swapped in tests

// daemonShellFunc is the test seam for sb.Shell, matching the ensureInstalled
// pattern in doctor.go. Tests override this; production code leaves the default.
var daemonShellFunc = func(ctx context.Context, sb msb.Sandbox, command string) (string, int, error) { //nolint:gochecknoglobals // test seam, swapped in tests
	out, err := sb.Shell(ctx, command)
	if err != nil {
		return "", -1, err
	}
	return out.Stdout(), out.ExitCode(), nil
}

type healthResponse struct {
	Healthy bool   `json:"healthy"`
	Version string `json:"version"`
}

func parseHealthResponse(stdout string) (bool, error) {
	var resp healthResponse
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		return false, fmt.Errorf("parse health response: %w", err)
	}
	return resp.Healthy, nil
}

// ensureDaemon guarantees the opencode serve daemon is healthy inside the VM.
// It health checks via curl inside the VM; if unhealthy, it kills any stale
// daemon process, starts a fresh one bound to the hostname appropriate for
// serveOnly mode, and polls until healthy or timeout.
func ensureDaemon(ctx context.Context, serveOnly bool, sb msb.Sandbox, ui termio.UI) error {
	if healthy := checkDaemonHealth(ctx, sb); healthy {
		ui.Verbosef("opencode daemon already healthy")
		return nil
	}

	ui.Verbosef("starting opencode serve daemon")
	if _, _, err := daemonShellFunc(ctx, sb, daemonKillCmd); err != nil {
		ui.Warnf("kill stale daemon failed (continuing): %v", err)
	}
	if _, _, err := daemonShellFunc(ctx, sb, daemonStartCommand(serveOnly)); err != nil {
		return fmt.Errorf("start opencode serve: %w", err)
	}

	deadline := time.Now().Add(daemonReadyTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(daemonPollInterval):
		}
		ui.Verbose("Polling for daemon health")
		if healthy := checkDaemonHealth(ctx, sb); healthy {
			ui.Verbosef("opencode daemon is healthy")
			return nil
		}
	}
	return fmt.Errorf("opencode daemon did not become healthy within %s", daemonReadyTimeout)
}

func checkDaemonHealth(ctx context.Context, sb msb.Sandbox) bool {
	stdout, exitCode, err := daemonShellFunc(ctx, sb, "curl -sfm2 "+daemonHealthURL)
	if err != nil || exitCode != 0 {
		return false
	}
	healthy, err := parseHealthResponse(stdout)
	return err == nil && healthy
}

// SetDaemonShellFunc replaces the daemonShellFunc factory used by ensureDaemon
// with one that returns the provided function. The original factory is
// returned so callers can restore it after their test.
//
// Usage from an external test package:
//
//	orig := sandbox.SetDaemonShellFunc(func(ctx context.Context, sb sandbox.msb.Sandbox, command string) (string, int, error) {
//	    return "", 0, nil
//	})
//	t.Cleanup(func() { sandbox.SetDaemonShellFunc(orig) })
func SetDaemonShellFunc(
	f func(ctx context.Context, sb msb.Sandbox, command string) (string, int, error),
) func(ctx context.Context, sb msb.Sandbox, command string) (string, int, error) {
	orig := daemonShellFunc
	daemonShellFunc = f
	return orig
}
