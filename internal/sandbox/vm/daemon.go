package vm

import (
	"context"
	"fmt"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

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

// ensureDaemon guarantees the agent's serve daemon is healthy inside the VM. It
// health checks via the provider's command inside the VM; if unhealthy, it
// kills any stale daemon process, starts a fresh one bound to the hostname
// appropriate for serveOnly mode, and polls until healthy or timeout. An agent
// without a daemon provider is a no-op.
func ensureDaemon(ctx context.Context, a agent.Agent, serveOnly bool, sb msb.Sandbox, ui termio.UI) error {
	provider, ok := agent.AsDaemonProvider(a)
	if !ok {
		return nil
	}

	if healthy := checkDaemonHealth(ctx, sb, provider); healthy {
		ui.Verbosef("%s daemon already healthy", a.Name())
		return nil
	}

	ui.Verbosef("starting %s serve daemon", a.Name())
	if _, _, err := daemonShellFunc(ctx, sb, provider.DaemonKillCmd()); err != nil {
		ui.Warnf("kill stale daemon failed (continuing): %v", err)
	}
	if _, _, err := daemonShellFunc(ctx, sb, provider.DaemonStartCmd(serveOnly)); err != nil {
		return fmt.Errorf("start %s serve: %w", a.Name(), err)
	}

	deadline := time.Now().Add(daemonReadyTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(daemonPollInterval):
		}
		ui.Verbose("Polling for daemon health")
		if healthy := checkDaemonHealth(ctx, sb, provider); healthy {
			ui.Verbosef("%s daemon is healthy", a.Name())
			return nil
		}
	}
	return fmt.Errorf("%s daemon did not become healthy within %s", a.Name(), daemonReadyTimeout)
}

func checkDaemonHealth(ctx context.Context, sb msb.Sandbox, provider agent.DaemonProvider) bool {
	stdout, exitCode, err := daemonShellFunc(ctx, sb, provider.DaemonHealthCmd())
	if err != nil || exitCode != 0 {
		return false
	}
	healthy, err := provider.DaemonHealthParse(stdout)
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
