package vm

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/term"

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

// hookFDIsTerminal is the terminal probe behind the production hook
// interactivity default; a variable so a test can pin the two-fd requirement.
var hookFDIsTerminal = term.IsTerminal

// hookInteractiveDefault reports whether a startup hook may run interactively:
// both the launcher's stdin and stdout are terminals. Requiring stdout keeps a
// redirected or piped run (agents-sandbox | tee log) on the non-interactive
// path, so a prompting hook fails visibly instead of blocking on a prompt the
// user cannot see.
func hookInteractiveDefault() bool {
	return hookFDIsTerminal(int(os.Stdin.Fd())) && hookFDIsTerminal(int(os.Stdout.Fd()))
}

// hookInteractiveFor resolves the hook interactivity the run was given: the
// injected seam when set, the production default otherwise.
func hookInteractiveFor(seams options.Seams) bool {
	if seams.HookInteractive != nil {
		return seams.HookInteractive()
	}
	return hookInteractiveDefault()
}

// runStartupHooks runs each configured startup hook inside the VM. When the
// launcher has a terminal (interactive) each hook runs through the interactive
// PTY attach, so a hook may prompt for a password or MFA; otherwise it runs
// through a non-interactive streaming exec with stdin closed, so a run without a
// TTY still runs the hooks. A hook with a shebang is executed directly so the VM
// kernel resolves the interpreter; one without falls back to /bin/sh. Hook
// scripts are marked executable during provisioning, so no +x bit is required on
// the host source. The hook's HOME is set so scripts can rely on it. The
// non-interactive arm's output is written verbatim to the session's
// stdout/stderr as the hook produces it; the interactive arm's output goes to
// the terminal through the PTY. The agent waits for the hook to finish and a
// hook that must outlive it is responsible for daemonizing itself. A hook that
// cannot start, or that exits non-zero, is reported with its target and the
// reason. Failures are logged, not fatal.
func runStartupHooks(
	ctx context.Context,
	sb msb.Sandbox,
	hooks []homeconfig.HookSpec,
	ui termio.UI,
	interactive bool,
) {
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
		runStartupHook(ctx, sb, h.Target, command, args, user, home, interactive, ui)
	}
}

// stderrTailBytes is the cap on stderr retained for a hook's failure warning.
// The hook's stderr is streamed to the UI in full; only the bounded tail is
// kept in launcher memory and echoed in the warning, so a hook with large
// stderr neither grows the launcher nor produces an unbounded log line.
const stderrTailBytes = 8 << 10

// stderrTail retains the last stderrTailBytes written bytes, discarding the
// oldest bytes first. It keeps its buffer at a fixed size once filled.
type stderrTail struct {
	buf []byte
}

func (t *stderrTail) Write(p []byte) {
	if len(p) >= stderrTailBytes {
		t.buf = append(t.buf[:0], p[len(p)-stderrTailBytes:]...)
		return
	}
	t.buf = append(t.buf, p...)
	if len(t.buf) > stderrTailBytes {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-stderrTailBytes:]...)
	}
}

// String returns the retained tail with surrounding whitespace trimmed.
func (t *stderrTail) String() string { return strings.TrimSpace(string(t.buf)) }

// runStartupHook runs one hook through the interactive PTY attach when the
// launcher has a terminal, or through the streaming exec seam otherwise. The
// interactive path streams the hook's output to the terminal itself; the
// streaming path is required, not the collected Exec: the collected response
// buffer is capped at ~1 MiB, so a hook that emits more would be cut off and
// misreported as failed even though it exited 0. A chunk is an arbitrary byte
// range, not a line, so the bytes go to the raw streams rather than through a
// line logger, which would add a newline per chunk and split lines that straddle
// chunks. The exit code is surfaced by the exited event; a hook that never
// starts, or a stream error, is logged as a warning, not fatal.
func runStartupHook(
	ctx context.Context,
	sb msb.Sandbox,
	target, command string,
	args []string,
	user, home string,
	interactive bool,
	ui termio.UI,
) {
	if interactive {
		runStartupHookInteractive(ctx, sb, target, command, args, user, home, ui)
		return
	}
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

	var stderr stderrTail
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
			_, _ = ui.StdOut().Write(event.Data)
		case msb.StreamEventStderr:
			stderr.Write(event.Data)
			_, _ = ui.StdErr().Write(event.Data)
		case msb.StreamEventExited:
			if event.ExitCode != 0 {
				ui.Warnf("startup hook %s failed: %s", target, exitReason(event.ExitCode, stderr.String()))
			}
			return
		case msb.StreamEventFailed:
			if event.Failure != "" {
				ui.Warnf("startup hook %s failed: %s", target, event.Failure)
			} else {
				ui.Warnf("startup hook %s failed: could not start", target)
			}
			return
		}
	}
}

// runStartupHookInteractive runs one hook through the interactive PTY attach, so
// a hook that prompts for a password or MFA can read it from the launcher's
// terminal. The attach streams the hook's output itself and returns only an exit
// code, so the failure warning names the code without a stderr tail. It runs
// under SIGINT/SIGTERM cancellation like the agent attach, so Ctrl-C ends the
// hook and its terminal is restored instead of killing the launcher mid-attach,
// and it clears any guest-TUI emulator modes after an abnormal end (a signal
// death or an attach error) before the next launcher message. A failure is
// logged, not fatal; a failed attach is not retried through the streaming exec,
// because re-running a hook whose side effects may have started is worse than
// skipping it.
func runStartupHookInteractive(
	ctx context.Context,
	sb msb.Sandbox,
	target, command string,
	args []string,
	user, home string,
	ui termio.UI,
) {
	attachCtx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	code, err := sb.AttachWith(
		attachCtx,
		command,
		args,
		msb.AttachRequest{User: user, Env: map[string]string{"HOME": home}},
	)
	interrupted := attachCtx.Err() != nil && ctx.Err() == nil
	if err != nil || code == -1 || interrupted {
		termio.ResetTerminal(ui.StdOut())
	}
	if ctx.Err() != nil {
		return
	}
	if interrupted {
		ui.Warnf("startup hook %s interrupted", target)
		return
	}
	if err != nil {
		ui.Warnf("startup hook %s failed: %v", target, err)
		return
	}
	if code != 0 {
		ui.Warnf("startup hook %s failed: exit %d", target, code)
	}
}

// exitReason describes a hook's non-zero exit for its failure warning: the exit
// code plus the retained stderr tail, which runStartupHook bounds.
func exitReason(exitCode int, tail string) string {
	if tail == "" {
		return fmt.Sprintf("exit %d", exitCode)
	}
	return fmt.Sprintf("exit %d: %s", exitCode, tail)
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
	ui termio.UI,
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
		// Serve-only owns stdin with its stop watcher (serveOnlyContext), so a
		// hook prompt could not reach a reader; every other run may prompt when
		// the launcher has a terminal.
		interactive := hookInteractiveFor(seams) && !opts.ServeOnly
		runStartupHooks(ctx, sb, cfs.Hooks, ui, interactive)
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
