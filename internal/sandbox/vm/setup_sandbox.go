package vm

import (
	"context"
	"fmt"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// DefaultSandboxUser is the user hooks run as unless the hook opts into root.
const DefaultSandboxUser = "dev"

// RootUser is the VM root user, used to run privileged startup hooks.
const RootUser = "root"

// defaultHookInterpreter is the interpreter used for a startup hook that does
// not declare a shebang.
const defaultHookInterpreter = "/bin/sh"

// runStartupHooks runs each configured startup hook inside the VM via a
// non-interactive streaming exec, so a run without a TTY still runs the hooks.
// A hook with a shebang is executed directly so the VM kernel resolves the
// interpreter; one without falls back to /bin/sh. Hook scripts are marked
// executable during provisioning, so no +x bit is required on the host source.
// The hook's HOME is set so scripts can rely on it. Output is streamed to the
// UI as the hook produces it; the agent waits for the exec to finish and a hook
// that must outlive it is responsible for daemonizing itself. A hook that
// cannot start, or that exits non-zero, is reported with its target and the
// reason. Failures are logged, not fatal.
func runStartupHooks(ctx context.Context, sb msb.Sandbox, hooks []homeconfig.HookSpec, ui termio.Logger) {
	for _, h := range hooks {
		user := DefaultSandboxUser
		home := "/home/dev"
		if h.Root {
			user = RootUser
			home = "/root"
		}
		command := h.Target
		var args []string
		if !h.HasShebang {
			command = defaultHookInterpreter
			args = []string{h.Target}
		}
		ui.Infof("running startup hook %s (as %s, via %s)", h.Target, user, command)
		runStartupHook(ctx, sb, h.Target, command, args, user, home, ui)
	}
}

// runStartupHook runs one hook through the streaming exec seam and forwards its
// output to the UI. The streaming seam is required, not the collected Exec: the
// collected response buffer is capped at ~1 MiB, so a hook that emits more
// would be cut off and misreported as failed even though it exited 0. The exit
// code is surfaced by the exited event; a hook that never starts, or a stream
// error, is logged as a warning, not fatal.
func runStartupHook(
	ctx context.Context,
	sb msb.Sandbox,
	target, command string,
	args []string,
	user, home string,
	ui termio.Logger,
) {
	handle, err := sb.ExecStream(
		ctx,
		command,
		args,
		msb.ExecRequest{User: user, Env: map[string]string{"HOME": home}},
	)
	if err != nil {
		ui.Warnf("startup hook %s failed: %v", target, err)
		return
	}
	defer handle.Close()

	var stderr strings.Builder
	for {
		event, err := handle.Recv(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			ui.Warnf("startup hook %s failed: %v", target, err)
			return
		}
		switch event.Kind {
		case msb.StreamEventStdout:
			ui.Info(string(event.Data))
		case msb.StreamEventStderr:
			stderr.Write(event.Data)
			ui.Info(string(event.Data))
		case msb.StreamEventExited:
			if event.ExitCode != 0 {
				ui.Warnf("startup hook %s failed: %s", target, exitReason(event.ExitCode, stderr.String()))
			}
			return
		case msb.StreamEventFailed:
			ui.Warnf("startup hook %s failed: could not start", target)
			return
		}
	}
}

// exitReason describes a hook's non-zero exit for its failure warning: the exit
// code plus a trimmed stderr tail when the hook produced one.
func exitReason(exitCode int, stderr string) string {
	reason := fmt.Sprintf("exit %d", exitCode)
	if tail := strings.TrimSpace(stderr); tail != "" {
		reason += ": " + tail
	}
	return reason
}

// SetUpSandbox provisions a running project VM and returns the attach target.
// It writes the agent config files, records image provenance on a fresh boot,
// and then either restarts the agent daemon (restart, for a config change the
// running VM must pick up) or runs the startup hooks, starts dockerd and
// ensures the agent daemon. It consumes the run's already-resolved agent
// capabilities (a) rather than re-looking-up the agent. The target is resolved
// from guestCWD and opts.Worktree.
func SetUpSandbox(
	ctx context.Context,
	k state.Key,
	sb msb.Sandbox,
	a agent.Resolved,
	opts options.RunOptions,
	seams options.Seams,
	cfs *reprovision.ConfigFiles,
	ui termio.Logger,
	restart bool,
	boot VMBoot,
	guestCWD string,
) (string, error) {
	ui.Verbosef("expected config files: %v", cfs.Keys)

	// Provisioning (writing files) is idempotent and non-disruptive, so it is
	// always performed; without config to write it does nothing. Whether the
	// daemon is restarted to pick the config up is decided separately (the
	// restart flag): on a "keep" decision the files are still updated on disk so
	// the next daemon start sees them, without disturbing the running instance.
	provisioned := true
	if provErr := reprovision.Provision(ctx, sb, cfs); provErr != nil {
		ui.Warnf("provision failed: %v (continuing)", provErr)
		provisioned = false
	}

	// Record image provenance and the agent version baseline on a fresh boot.
	if boot.Booted() {
		recordImageProvenance(ctx, k, a, sb, ui)
	}

	if restart {
		if provisioned {
			restartDaemons(ctx, a, sb, opts.ServeOnly, ui, seams)
		}
		return ResolveTarget(ctx, a, sb, opts.Worktree, guestCWD, ui)
	}

	if boot.Booted() {
		runStartupHooks(ctx, sb, cfs.Hooks, ui)
	}

	if dockerErr := startDockerdIfPresent(ctx, sb, ui); dockerErr != nil {
		return "", fmt.Errorf("docker startup: %w", dockerErr)
	}

	if daemonErr := ensureDaemon(ctx, a, opts.ServeOnly, false, sb, ui, seams); daemonErr != nil {
		return "", daemonErr
	}

	return ResolveTarget(ctx, a, sb, opts.Worktree, guestCWD, ui)
}

// restartDaemons restarts the agent's daemon so an already-provisioned
// agent-config change is picked up. Env/secret changes are never routed
// here: they require a VM rebuild and are handled by the recreate path instead.
func restartDaemons(
	ctx context.Context, a agent.Resolved, sb msb.Sandbox, serveOnly bool, ui termio.Logger, seams options.Seams,
) {
	ui.Infof("%s serve restarting…", a.Profile.Name())
	// ensureDaemon no-ops for an agent without a DaemonProvider.
	if err := ensureDaemon(ctx, a, serveOnly, true, sb, ui, seams); err != nil {
		ui.Warnf("daemon restart failed: %v (using existing)", err)
	}
}
