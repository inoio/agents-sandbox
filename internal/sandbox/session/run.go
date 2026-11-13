package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/sandbox/lifecycle"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/ports"
	"github.com/inoio/agents-sandbox/internal/sandbox/prepare"
	"github.com/inoio/agents-sandbox/internal/sandbox/pressure"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// preparedSandbox is the subset of a prepared session that Run and Shell use.
// It is satisfied by *prepare.Session and by the fake used in tests.
type preparedSandbox interface {
	Cleanup()
	Sandbox() msb.Sandbox
	Target() string
	ServeHostPort() int
	ServePortDeferred() bool
	GuestCWD() string
	Key() state.Key
	HomeVolume() string
}

// prepareSandbox is a test seam swapped in tests to avoid real VM setup.
var prepareSandbox = func(ctx context.Context, opts options.RunOptions, skipVM bool, ui termio.UI) (preparedSandbox, error) {
	return prepare.PrepareSandbox(ctx, opts, skipVM, ui)
}

// previewSandbox is a test seam swapped in tests to avoid real host inspection.
var previewSandbox = prepare.PreviewSandbox

// renderPreview reports what a real run would do, without doing it.
func renderPreview(ui termio.Logger, p prepare.Preview, shell bool) {
	switch {
	case p.ResolveVersionAtBuild:
		ui.Infof("dry-run: agent version resolved at build time")
	case p.UpgradeCheckSkipped:
		ui.Infof("dry-run: agent version %s (upgrade check not run)", p.AgentVersion)
	default:
		ui.Infof("dry-run: agent version %s", p.AgentVersion)
	}
	switch {
	case p.BuildImage:
		ui.Infof("dry-run: would build runner image %s (%s)", p.ImageTag, strings.Join(p.BuildReasons, "; "))
	case len(p.BuildReasons) > 0:
		ui.Infof("dry-run: would reuse runner image %s despite changes (--no-rebuild): %s",
			p.ImageTag, strings.Join(p.BuildReasons, "; "))
	default:
		ui.Infof("dry-run: would reuse runner image %s", p.ImageTag)
	}
	if p.CreateHome {
		ui.Infof("dry-run: would create home volume")
	} else {
		ui.Infof("dry-run: would reuse home volume %s", p.HomeVolume)
	}
	if shell {
		ui.Infof("dry-run: would start interactive shell session")
	} else {
		ui.Infof("dry-run: would start agent session")
	}
}

// previewAndRender resolves the read-only preview and reports it, returning nil
// so a dry run ends here: nothing is built, created, written, started, or
// attached.
func previewAndRender(ctx context.Context, opts options.RunOptions, ui termio.Logger, shell bool) error {
	p, err := previewSandbox(ctx, opts, ui)
	if err != nil {
		return err
	}
	renderPreview(ui, p, shell)
	return nil
}

// notifyWatch is a test seam; production uses notify.Watch.
var notifyWatch = notify.Watch

// startNotifyWatcher launches the notify watcher in a goroutine and returns a
// stop function. It is a no-op when notifications are inactive, the sandbox is
// nil (dry-run), or the agent provides no EventStreamSpec. The watcher runs
// until the session ctx is done or stop is called, whichever comes first. The
// stop function cancels the watcher, waits for it to finish, and returns the
// watcher's error (the drop summary) for the caller to log.
func startNotifyWatcher(
	ctx context.Context,
	sb msb.SandboxStreamer,
	cfg notify.Config,
	ui termio.Logger,
	spec *agent.EventStreamSpec,
	projectSlug string,
) func() error {
	if sb == nil || !cfg.Active() || spec == nil {
		ui.Verbosef(
			"not starting notify watcher, sandbox nil %v, active %v, spec set %v",
			sb == nil,
			cfg.Active(),
			spec != nil,
		)
		return func() error { return nil }
	}
	backend := notify.NewBackend(cfg, ui)
	if projectSlug != "" {
		backend = notify.NewDedup(projectSlug, notify.StateClaimer{}, backend)
	}
	watchCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var watchErr error
	go func() {
		defer close(done)
		ui.Verbosef("starting notify watcher, project %s, active %v, spec %+v", projectSlug, cfg.Active(), spec)
		watchErr = notifyWatch(watchCtx, notify.SandboxStreamOpener{Sandbox: sb}, *spec, backend)
	}()
	return func() error {
		cancel()
		<-done
		return watchErr
	}
}

func buildAttachCommand(a agent.Resolved, target string, args []string) string {
	if a.Attach == nil {
		return ""
	}
	return a.Attach.AttachCommand(target, args)
}

// logNotifyWatcherError stops the notify watcher and logs its drop-summary
// error, if any. It is deferred from Run so it runs only after the attach TUI
// has closed and can no longer be corrupted by the message.
func logNotifyWatcherError(stop func() error, ui termio.Logger) {
	if err := stop(); err != nil {
		ui.Verbosef("notify watcher: %v", err)
	}
}

// serveOnlyMessage builds the message printed when serving the agent daemon for
// external clients such as desktop apps. The basic-auth guidance is
// agent-specific: opencode's is opt-in, opencode2's is mandatory with a baked
// credential, so a client can only connect with the right hint.
//
// When a kept running VM has no reachable serve binding — the changed binding
// was deferred to the next recreate, or its bindings could not be read — no
// client can reach the agent yet, so the message states that the serve binding
// is unavailable instead of handing out a URL the VM does not carry.
func serveOnlyMessage(a agent.Resolved, port string, deferred bool) string {
	if deferred {
		return "The agent server is not reachable from the host: the running VM has no serve binding to keep.\n\n" +
			"Stop the attached clients and re-run to recreate the VM, then connect a client again."
	}
	authHint := ""
	if a.Daemon != nil {
		if hint := a.Daemon.DaemonAuthHint(); hint != "" {
			authHint = hint + "\n"
		}
	}
	return fmt.Sprintf("Connect a client to: http://%s:%s\n\n%sPress Ctrl-D to stop serving.",
		ports.ServeOnlyBindAddr, port, authHint)
}

// runServeOnly keeps the VM alive and blocks until ctx is done (CTRL-D or
// SIGINT), without attaching an in-VM TUI. It holds the VM via a keeper exec so
// the msb idle timeout does not stop it while serving.
func runServeOnly(
	ctx context.Context,
	a agent.Resolved,
	sb msb.SandboxExecutor,
	ui termio.Logger,
	hostPort int,
	deferred bool,
) error {
	message := serveOnlyMessage(a, strconv.Itoa(hostPort), deferred)
	if deferred {
		ui.Warnf("%s", message)
	} else {
		ui.Infof("%s", message)
	}
	keeperCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	keeperDone := keepVMAlive(keeperCtx, sb)
	defer func() { _ = keeperDone() }()
	<-ctx.Done()
	return ctx.Err()
}

// Run creates (or reuses) the project VM, provisions config, starts the agent
// serve daemon, and attaches a TUI client.
//
// Note: Run is called from cli.go after all flags are resolved.
func Run(ctx context.Context, opts options.RunOptions, mode options.Mode, ui termio.UI) error {
	if mode.Previews() {
		return previewAndRender(ctx, opts, ui, false)
	}

	ses, err := prepareSandbox(ctx, opts, mode.SkipsVM(), ui)
	if err != nil {
		return err
	}
	defer ses.Cleanup()
	sb := ses.Sandbox()

	// A skip-VM prepare leaves sb nil (PrepareSandbox's skipVM contract), so
	// this mode check alone decides the dry-run-vm path. Keeping only
	// mode.SkipsVM() avoids a redundant sb == nil operand whose mutations cannot
	// change behaviour.
	if mode.SkipsVM() {
		ui.Infof("dry-run: Would start agent session in VM")
		return nil
	}

	opts.Trace.Record(lifecycle.Attach)
	k := ses.Key()

	a := opts.ResolvedAgent
	var streamSpec *agent.EventStreamSpec
	if a.EventStream != nil {
		eventStream := a.EventStream.EventStream()
		streamSpec = &eventStream
	}
	stopNotify := startNotifyWatcher(ctx, sb, opts.Notify, ui, streamSpec, k.Slug)
	defer logNotifyWatcherError(stopNotify, ui)
	stopHostOpener := startHostOpener(ctx, ses, opts.OpenLinks, ui)
	defer stopHostOpener()

	if opts.ServeOnly {
		return runServeOnlySession(ctx, a, k, sb, opts.ReapPolicy, ui, ses.ServeHostPort(), ses.ServePortDeferred())
	}

	setup := buildAttachCommand(a, ses.Target(), opts.Args)
	ui.Verbosef("%s", setup)
	// Run as a login shell so /etc/profile and ~/.profile are sourced,
	// putting tools installed under /usr/local/go/bin and ~/go/bin on
	// PATH for the agent and its child shells. The image's composed PATH
	// is restored by the /etc/profile.d/agents-sandbox-path.sh merge.
	return runAttach(ctx, k, sb, ses.GuestCWD(), ui, opts, "-l", "-c", setup)
}

// withClientSession owns the client-lease lifecycle shared by the attach and
// serve-only flows: it acquires a client lease for the run's resolved agent,
// runs body, explicitly releases the lease before reaping so
// state.CountActiveClients reflects only OTHER live clients, then reaps on the
// last client when shouldReap reports the session's error as reap-able. body's
// error is returned unchanged; the deferred release is a best-effort safety net
// (for a panicking body or reap).
func withClientSession(
	ctx context.Context,
	k state.Key,
	sb msb.SandboxExecutor,
	policy options.ReapPolicy,
	ui termio.UI,
	shouldReap func(sessionErr error) bool,
	a agent.Resolved,
	body func(a agent.Resolved) error,
) error {
	// Acquire a client lease so state tracks this session.
	release, acquireErr := state.AcquireClientLease(k)
	if acquireErr != nil {
		ui.Warnf("client lease failed: %v", acquireErr)
	}
	defer func() {
		if release != nil {
			release()
		}
	}()

	sessionErr := body(a)

	// Explicitly release the lease after the payload returns, before reaping.
	// This ensures state.CountActiveClients reflects only OTHER live clients.
	// The deferred release above is a safety net.
	if acquireErr == nil {
		release()
		release = nil
	}

	if shouldReap(sessionErr) {
		if err := reapOnLastClient(ctx, a, k, sb, policy, ui); err != nil {
			ui.Warnf("reap failed: %v", err)
		}
	}

	return sessionErr
}

// alwaysReap is the attach reap predicate: an attach reaps on the last client
// even when the attach itself failed.
func alwaysReap(error) bool { return true }

// serveOnlyShouldReap reports whether a serve-only session's end is reap-able: a
// clean exit (nil) or a user cancel reaps, while any other payload error is an
// abnormal end that skips the reap, leaving the VM to its idle timeout. The same
// predicate classifies the result the caller sees: a reap-able exit reports a
// clean ExitError 0, an abnormal one is returned to the caller.
func serveOnlyShouldReap(sessionErr error) bool {
	return sessionErr == nil || errors.Is(sessionErr, context.Canceled)
}

// runServeOnlySession supplies the serve-only payload around the shared client
// session lifecycle: hold the VM (keeper exec) until ctx is done, then report a
// clean exit.
func runServeOnlySession(
	ctx context.Context,
	a agent.Resolved,
	k state.Key,
	sb msb.SandboxExecutor,
	policy options.ReapPolicy,
	ui termio.UI,
	hostPort int,
	deferred bool,
) error {
	sessionErr := withClientSession(
		ctx,
		k,
		sb,
		policy,
		ui,
		serveOnlyShouldReap,
		a,
		func(a agent.Resolved) error {
			return runServeOnly(ctx, a, sb, ui, hostPort, deferred)
		},
	)
	if !serveOnlyShouldReap(sessionErr) {
		return sessionErr
	}
	return &sandbox.ExitError{Code: 0}
}

// Shell creates (or reuses) the project VM and drops the user into an
// interactive shell session, without starting the agent serve daemon.
func Shell(ctx context.Context, opts options.RunOptions, mode options.Mode, ui termio.UI) error {
	if mode.Previews() {
		return previewAndRender(ctx, opts, ui, true)
	}

	ses, err := prepareSandbox(ctx, opts, mode.SkipsVM(), ui)
	if err != nil {
		return err
	}
	defer ses.Cleanup()
	sb := ses.Sandbox()

	// See Run: the skip-VM mode alone decides this path because a skip-VM
	// prepare leaves sb nil.
	if mode.SkipsVM() {
		ui.Infof("dry-run: Would start interactive shell session")
		return nil
	}

	opts.Trace.Record(lifecycle.Attach)
	stopHostOpener := startHostOpener(ctx, ses, opts.OpenLinks, ui)
	defer stopHostOpener()

	return runAttach(ctx, ses.Key(), sb, ses.GuestCWD(), ui, opts, "-l")
}

func finalizeRun(attachErr error, exitCode int) error {
	if attachErr != nil {
		return fmt.Errorf("agent session failed: %w", attachErr)
	}
	if exitCode == 0 {
		return nil
	}
	return &sandbox.ExitError{Code: exitCode}
}

// runAttach supplies the attach payload around the shared client session
// lifecycle. The guest CWD is applied to every agent attach; pi and Claude Code
// ignore the attach target and rely solely on the working directory.
//
// Both Run and Shell route their attach through here, so the SIGINT/SIGTERM
// handling is shared: a signal cancels the attach context, AttachWith returns,
// and the reset below clears the guest TUI's emulator modes (the residue the
// SDK's termios restore does not cover) before the process exits.
func runAttach(
	ctx context.Context,
	k state.Key,
	sb msb.Sandbox,
	guestCWD string,
	ui termio.UI,
	opts options.RunOptions,
	bashArgs ...string,
) error {
	return withClientSession(
		ctx,
		k,
		sb,
		opts.ReapPolicy,
		ui,
		alwaysReap,
		opts.ResolvedAgent,
		func(agent.Resolved) error {
			stopPressure := startPressureWatcher(ctx, sb, pressure.DefaultThreshold())
			attachReq := msb.AttachRequest{Cwd: guestCWD}
			if opts.Root {
				attachReq.User = sandbox.RootUser
			}
			exitCode, interrupted, attachErr := attachUntilSignal(ctx, sb, bashArgs, attachReq)
			// The guest TUI's emulator modes (alternate screen, mouse) survive
			// a killed TUI; clear them before the launcher's post-attach
			// messages re-enter the terminal. A clean exit restored them
			// itself, and leaving the alternate screen again would restore a
			// stale saved cursor. ui.StdOut() is the real os.Stdout here;
			// resetTerminal's TTY check only accepts an *os.File, so a
			// wrapping writer would silently disable the reset.
			endedAbnormally := abnormalEnd(attachErr, exitCode) || interrupted
			if endedAbnormally {
				resetTerminal(ui.StdOut())
			}
			// Stop sampling and read the summary while the VM is still alive
			// and before the lease is released and the VM reaped.
			summary := stopPressure()
			if summary.ReadErr != nil {
				ui.Verbosef("pressure watcher: metrics read failed: %v", summary.ReadErr)
			}
			ui.Verbosef(
				"memory pressure during attach: min headroom fraction %.2f, tripped %v",
				summary.MinHeadroomFraction,
				summary.Tripped,
			)
			if endedAbnormally && !interrupted {
				offerMemoryRaiseAfterAttach(ctx, k, sb, summary, opts, ui)
			}
			if !interrupted {
				offerDiskRaiseAfterAttach(ctx, k, sb, opts, ui)
			}
			return finalizeRun(attachErr, exitCode)
		},
	)
}

// attachUntilSignal runs the attach with SIGINT/SIGTERM canceling it, so a
// signal ends the attach and the caller restores the terminal before exiting.
// The signal handling is released when the attach returns: the post-attach
// prompt keeps the default Ctrl-C behavior. interrupted reports whether a
// signal ended the attach; the guest exit code and attach error are returned
// alongside it.
func attachUntilSignal(
	ctx context.Context,
	sb msb.SandboxAttacher,
	bashArgs []string,
	attachReq msb.AttachRequest,
) (int, bool, error) {
	attachCtx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	exitCode, attachErr := sb.AttachWith(attachCtx, "/bin/bash", bashArgs, attachReq)
	return exitCode, attachCtx.Err() != nil && ctx.Err() == nil, attachErr
}
