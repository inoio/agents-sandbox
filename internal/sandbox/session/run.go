package session

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// preparedSandbox is the subset of a prepared session that Run and Shell use.
// It is satisfied by *sandbox.Session and by the fake used in tests.
type preparedSandbox interface {
	Cleanup()
	Sandbox() msb.Sandbox
	Target() string
	ServeHostPort() int
	GuestCWD() string
	Key() state.Key
	HomeVolume() string
}

// prepareSandbox is a test seam swapped in tests to avoid real VM setup.
var prepareSandbox = func(ctx context.Context, opts options.RunOptions, skipVM bool, ui termio.UI) (preparedSandbox, error) {
	return sandbox.PrepareSandbox(ctx, opts, skipVM, ui)
}

// previewSandbox is a test seam swapped in tests to avoid real host inspection.
var previewSandbox = sandbox.PreviewSandbox

// renderPreview reports what a real run would do, without doing it.
func renderPreview(ui termio.UI, p sandbox.Preview, shell bool) {
	switch {
	case p.ResolveVersionAtBuild:
		ui.Infof("dry-run: agent version resolved at build time")
	case p.UpgradeCheckSkipped:
		ui.Infof("dry-run: agent version %s (upgrade check not run)", p.AgentVersion)
	default:
		ui.Infof("dry-run: agent version %s", p.AgentVersion)
	}
	if p.BuildImage {
		ui.Infof("dry-run: would build runner image %s (%s)", p.ImageTag, strings.Join(p.BuildReasons, "; "))
	} else {
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
func previewAndRender(ctx context.Context, opts options.RunOptions, ui termio.UI, shell bool) error {
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
	sb msb.Sandbox,
	cfg notify.Config,
	ui termio.UI,
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

func buildAttachCommand(a agent.Agent, target string, args []string) string {
	runner, ok := agent.AsAttachRunner(a)
	if !ok {
		return ""
	}
	return runner.AttachCommand(target, args)
}

// logNotifyWatcherError stops the notify watcher and logs its drop-summary
// error, if any. It is deferred from Run so it runs only after the attach TUI
// has closed and can no longer be corrupted by the message.
func logNotifyWatcherError(stop func() error, ui termio.UI) {
	if err := stop(); err != nil {
		ui.Verbosef("notify watcher: %v", err)
	}
}

// serveOnlyMessage builds the message printed when serving the agent daemon for
// external clients such as desktop apps. The basic-auth guidance is
// agent-specific: opencode's is opt-in, opencode2's is mandatory with a baked
// credential, so a client can only connect with the right hint.
func serveOnlyMessage(a agent.Agent, host, port string) string {
	authHint := ""
	if provider, ok := agent.AsDaemonProvider(a); ok {
		if hint := provider.DaemonAuthHint(); hint != "" {
			authHint = hint + "\n"
		}
	}
	return fmt.Sprintf("Connect a client to: http://%s:%s\n\n%sPress Ctrl-D to stop serving.", host, port, authHint)
}

// runServeOnly keeps the VM alive and blocks until ctx is done (CTRL-D or
// SIGINT), without attaching an in-VM TUI. It holds the VM via a keeper exec so
// the msb idle timeout does not stop it while serving.
func runServeOnly(ctx context.Context, a agent.Agent, sb msb.Sandbox, ui termio.UI, hostPort int) error {
	host := options.ServeOnlyBindAddr
	port := strconv.Itoa(hostPort)
	ui.Infof("%s", serveOnlyMessage(a, host, port))
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

	projectSlug := ses.Key().Slug

	a, _ := agent.Lookup(opts.Agent)
	var streamSpec *agent.EventStreamSpec
	if provider, ok := agent.AsEventStreamProvider(a); ok {
		eventStream := provider.EventStream()
		streamSpec = &eventStream
	}
	stopNotify := startNotifyWatcher(ctx, sb, opts.Notify, ui, streamSpec, projectSlug)
	defer logNotifyWatcherError(stopNotify, ui)
	stopHostOpener := startHostOpener(ctx, ses, opts.OpenLinks, ui)
	defer stopHostOpener()

	if opts.ServeOnly {
		return runServeOnlySession(ctx, a, sb, opts, ui, ses.ServeHostPort(), projectSlug)
	}

	setup := buildAttachCommand(a, ses.Target(), opts.Args)
	ui.Verbosef("%s", setup)
	// Run as a login shell so /etc/profile and ~/.profile are sourced,
	// putting tools installed under /usr/local/go/bin and ~/go/bin on
	// PATH for the agent and its child shells. The image's composed PATH
	// is restored by the /etc/profile.d/agents-sandbox-path.sh merge.
	return runAttach(ctx, sb, projectSlug, ses.GuestCWD(), ui, opts, "-l", "-c", setup)
}

// runServeOnlySession owns the lease acquire/serve/release/reap sequence for
// serve-only mode.
func runServeOnlySession(
	ctx context.Context,
	a agent.Agent,
	sb msb.Sandbox,
	opts options.RunOptions,
	ui termio.UI,
	hostPort int,
	projectSlug string,
) error {
	k := state.Key{Slug: projectSlug, Agent: a.Name()}
	release, acquireErr := state.AcquireClientLease(k)
	if acquireErr != nil {
		ui.Warnf("client lease failed: %v", acquireErr)
	}
	defer func() {
		if acquireErr == nil && release != nil {
			release()
		}
	}()
	if err := runServeOnly(ctx, a, sb, ui, hostPort); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	if acquireErr == nil {
		release()
		release = nil
	}
	if err := reapOnLastClient(ctx, a, k, sb, opts.ReapPolicy, ui); err != nil {
		ui.Warnf("reap failed: %v", err)
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

	stopHostOpener := startHostOpener(ctx, ses, opts.OpenLinks, ui)
	defer stopHostOpener()

	projectSlug := ses.Key().Slug
	return runAttach(ctx, sb, projectSlug, ses.GuestCWD(), ui, opts, "-l")
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

// runAttach performs the shared lease-acquire, attach, explicit release,
// reap-on-last-client, and finalize sequence for Run and Shell. The guest CWD
// is applied to every agent attach; pi and Claude Code ignore the attach
// target and rely solely on the working directory.
func runAttach(
	ctx context.Context,
	sb msb.Sandbox,
	projectSlug string,
	guestCWD string,
	ui termio.UI,
	opts options.RunOptions,
	bashArgs ...string,
) error {
	a, _ := agent.Lookup(opts.Agent)
	k := state.Key{Slug: projectSlug, Agent: a.Name()}

	// Acquire a client lease so state tracks this session.
	release, acquireErr := state.AcquireClientLease(k)
	if acquireErr != nil {
		ui.Warnf("client lease failed: %v", acquireErr)
	}
	defer func() {
		if acquireErr == nil && release != nil {
			release()
		}
	}()

	// Attach to the sandbox and capture its exit code.
	var exitCode int
	var attachErr error
	attachOpts := []msbSdk.AttachOption{msbSdk.WithAttachCwd(guestCWD)}
	if opts.Root {
		attachOpts = append(attachOpts, msbSdk.WithAttachUser(sandbox.RootUser))
	}
	exitCode, attachErr = sb.AttachWith(ctx, "/bin/bash", bashArgs, attachOpts...)

	// Explicitly release the lease after attach returns, before reaping.
	// This ensures state.CountActiveClients reflects only OTHER live clients.
	// The deferred release above is a safety net.
	if acquireErr == nil {
		release()
		release = nil
	}

	if err := reapOnLastClient(ctx, a, k, sb, opts.ReapPolicy, ui); err != nil {
		ui.Warnf("reap failed: %v", err)
	}

	return finalizeRun(attachErr, exitCode)
}
