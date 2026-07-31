package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

const (
	daemonHealthURL = "http://127.0.0.1:4096/global/health"
	daemonStartCmd  = "nohup opencode serve --hostname 127.0.0.1 --port 4096 > /tmp/opencode-serve.log 2>&1 &"
	daemonKillCmd   = "pkill -f 'opencode serve' || true"
)

var daemonReadyTimeout = 60 * time.Second //nolint:gochecknoglobals // test seam, swapped in tests

var daemonPollInterval = 2 * time.Second //nolint:gochecknoglobals // test seam, swapped in tests

// daemonShellFunc is the test seam for sb.Shell, matching the ensureInstalled
// pattern in doctor.go. Tests override this; production code leaves the default.
var daemonShellFunc = func(ctx context.Context, sb *msb.Sandbox, command string) (string, int, error) { //nolint:gochecknoglobals // test seam, swapped in tests
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

// EnsureDaemon guarantees the opencode serve daemon is healthy inside the VM.
// It healthchecks via curl inside the VM; if unhealthy, it kills any stale
// daemon process, starts a fresh one, and polls until healthy or timeout.
func EnsureDaemon(ctx context.Context, sb *msb.Sandbox, ui stdio.UI) error {
	if healthy := checkDaemonHealth(ctx, sb); healthy {
		ui.Verbosef("opencode daemon already healthy")
		return nil
	}

	ui.Verbosef("starting opencode serve daemon")
	if _, _, err := daemonShellFunc(ctx, sb, daemonKillCmd); err != nil {
		ui.Warnf("kill stale daemon failed (continuing): %v", err)
	}
	if _, _, err := daemonShellFunc(ctx, sb, daemonStartCmd); err != nil {
		return fmt.Errorf("start opencode serve: %w", err)
	}

	deadline := time.Now().Add(daemonReadyTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(daemonPollInterval):
		}
		if healthy := checkDaemonHealth(ctx, sb); healthy {
			ui.Verbosef("opencode daemon is healthy")
			return nil
		}
	}
	return fmt.Errorf("opencode daemon did not become healthy within %s", daemonReadyTimeout)
}

func checkDaemonHealth(ctx context.Context, sb *msb.Sandbox) bool {
	stdout, exitCode, err := daemonShellFunc(ctx, sb, "curl -sf "+daemonHealthURL)
	if err != nil || exitCode != 0 {
		return false
	}
	healthy, err := parseHealthResponse(stdout)
	return err == nil && healthy
}
