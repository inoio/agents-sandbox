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
// non-interactive exec, so a run without a TTY still runs the hooks. A hook
// with a shebang is executed directly so the VM kernel resolves the
// interpreter; one without falls back to /bin/sh. Hook scripts are marked
// executable during provisioning, so no +x bit is required on the host source.
// The hook's HOME is set so scripts can rely on it. The agent waits for the
// exec to finish; a hook that must outlive it is responsible for daemonizing
// itself. A hook that cannot run, or that exits non-zero, is reported with its
// target and the reason. Failures are logged, not fatal.
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
		result, err := sb.Exec(
			ctx,
			command,
			args,
			msb.ExecRequest{User: user, Env: map[string]string{"HOME": home}},
		)
		if err != nil {
			ui.Warnf("startup hook %s failed: %v", h.Target, err)
			continue
		}
		if !result.Success() {
			reason := fmt.Sprintf("exit %d", result.ExitCode())
			if stderr := strings.TrimSpace(result.Stderr()); stderr != "" {
				reason += ": " + stderr
			}
			ui.Warnf("startup hook %s failed: %s", h.Target, reason)
		}
	}
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
