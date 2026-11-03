package vm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

var daemonReadyTimeout = 60 * time.Second

var daemonPollInterval = 2 * time.Second

// daemonHealthFunc is the test seam for the daemon health probe, matching the
// ensureInstalled pattern in doctor.go. Tests override this; production code
// leaves the default. Only the health probe goes through it: the daemon itself
// is started and stopped as a runtime-owned job, not by a shell command.
var daemonHealthFunc = func(ctx context.Context, sb msb.Sandbox, command string) (string, int, error) {
	out, err := sb.Shell(ctx, command)
	if err != nil {
		return "", -1, err
	}
	return out.Stdout(), out.ExitCode(), nil
}

// SetDaemonHealthFunc replaces the daemonHealthFunc factory used by
// ensureDaemon with one that returns the provided function. The original
// factory is returned so callers can restore it after their test.
//
//deadcode:ignore reason="test seam replacing the daemon health probe"
func SetDaemonHealthFunc(
	f func(ctx context.Context, sb msb.Sandbox, command string) (string, int, error),
) func(ctx context.Context, sb msb.Sandbox, command string) (string, int, error) {
	orig := daemonHealthFunc
	daemonHealthFunc = f
	return orig
}

// ensureDaemon guarantees the agent's serve daemon is healthy inside the VM as
// a runtime-owned detached job, through the shared ensureDetachedService recipe.
// It health checks via the provider's command; if unhealthy (or force is set, as
// for a config restart), it kills any stale daemon job, launches a fresh one,
// and waits until healthy or timeout. An agent without a daemon provider is a
// no-op.
func ensureDaemon(
	ctx context.Context,
	a agent.Resolved,
	serveOnly, force bool,
	sb msb.Sandbox,
	ui termio.Logger,
) error {
	provider := a.Daemon
	if provider == nil {
		return nil
	}

	command := provider.DaemonCommand(serveOnly)
	return ensureDetachedService(ctx, sb, ui, detachedService{
		helper: agentDaemonHelper(command.Binary),
		probe: func(probeCtx context.Context) (bool, string) {
			health := probeDaemonHealth(probeCtx, sb, provider)
			return health.healthy, health.detail
		},
		onUnhealthy: func(detail string) {
			ui.Verbosef("Polling for daemon health: %s", detail)
		},
		label:      a.Profile.Name() + " daemon",
		startLabel: a.Profile.Name() + " serve",
		launchArgs: func(context.Context) ([]string, string, error) {
			return command.Args, "starting " + a.Profile.Name() + " serve daemon", nil
		},
		timeout:               daemonReadyTimeout,
		pollInterval:          daemonPollInterval,
		skipPreCheck:          force,
		alreadyHealthyNote:    a.Profile.Name() + " daemon already healthy",
		preCheckUnhealthyNote: a.Profile.Name() + " daemon not healthy: %s",
		readyNote:             a.Profile.Name() + " daemon is healthy",
		closeNote:             "closing " + a.Profile.Name() + " job handle failed: %v",
	})
}

// daemonProbe is the outcome of a single health probe, carrying enough detail
// (exit code, parse error, or raw body) to diagnose a daemon that never becomes
// healthy.
type daemonProbe struct {
	healthy bool
	detail  string
}

// probeDaemonHealth runs the provider's health command once and classifies the
// result. A healthy probe carries no detail; an unhealthy one carries a
// human-readable explanation for verbose logging.
func probeDaemonHealth(ctx context.Context, sb msb.Sandbox, provider agent.DaemonProvider) daemonProbe {
	stdout, exitCode, err := daemonHealthFunc(ctx, sb, provider.DaemonHealthCmd())
	if err != nil {
		return daemonProbe{healthy: false, detail: fmt.Sprintf("health probe failed: %v", err)}
	}
	if exitCode != 0 {
		return daemonProbe{healthy: false, detail: fmt.Sprintf("health probe exit %d: %s", exitCode, probeBody(stdout))}
	}
	healthy, err := provider.DaemonHealthParse(stdout)
	if err != nil {
		return daemonProbe{
			healthy: false,
			detail:  fmt.Sprintf("health response unparsable: %v: %s", err, probeBody(stdout)),
		}
	}
	if !healthy {
		return daemonProbe{healthy: false, detail: fmt.Sprintf("server reported unhealthy: %s", probeBody(stdout))}
	}
	return daemonProbe{healthy: true, detail: ""}
}

// probeBody renders a health probe's stdout for logging, flagging an empty body
// (e.g. no response, or a response with an empty body).
func probeBody(stdout string) string {
	if body := strings.TrimSpace(stdout); body != "" {
		return body
	}
	return "(no output)"
}
