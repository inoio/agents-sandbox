package session

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/opencode-sandbox/internal/agent"
	"github.com/inoio/opencode-sandbox/internal/git"
	"github.com/inoio/opencode-sandbox/internal/notify"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	sandbox "github.com/inoio/opencode-sandbox/internal/sandbox/vm"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// preparedSandbox is the subset of a prepared session that Run and Shell use.
// It is satisfied by *sandbox.Session and by the fake used in tests.
type preparedSandbox interface {
	Cleanup()
	Sandbox() msb.Sandbox
	Target() string
	ServeHostPort() int
}

// prepareSandbox is a test seam swapped in tests to avoid real VM setup.
var prepareSandbox = func(ctx context.Context, opts options.RunOptions, ui termio.UI) (preparedSandbox, error) { //nolint:gochecknoglobals // test seam
	return sandbox.PrepareSandbox(ctx, opts, ui)
}

// notifyWatch is a test seam; production uses notify.Watch.
//
//nolint:gochecknoglobals // test seam
var notifyWatch = notify.Watch

// startNotifyWatcher launches the notify watcher in a goroutine and returns a
// stop function. It is a no-op when notifications are inactive, the sandbox is
// nil (dry-run), or the agent provides no EventStreamSpec. The watcher runs
// until the session ctx is done or stop is called, whichever comes first.
func startNotifyWatcher(
	ctx context.Context,
	sb msb.Sandbox,
	cfg notify.Config,
	ui termio.UI,
	spec *agent.EventStreamSpec,
	projectSlug string,
) func() {
	if sb == nil || !cfg.Active() || spec == nil {
		ui.Verbosef("not starting notify watcher, sandbox %s, active %v, spec %s", sb, cfg.Active(), spec)
		return func() {}
	}
	backend := notify.NewBackend(cfg, ui)
	if projectSlug != "" {
		backend = notify.NewDedup(projectSlug, notify.StateClaimer{}, backend)
	}
	watchCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		ui.Verbosef("starting notify watcher, sandbox %s, active %v, spec %s", sb, cfg.Active(), spec)

		defer close(done)
		if err := notifyWatch(watchCtx, sb, *spec, backend); err != nil {
			ui.Verbosef("notify watcher stopped: %v", err)
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func buildAttachCommand(a agent.Agent, target string, args []string) string {
	runner, ok := agent.AsAttachRunner(a)
	if !ok {
		return ""
	}
	return runner.AttachCommand(target, args)
}

// serveOnlyMessage builds the message printed when serving the agent daemon for
// external clients such as desktop apps.
func serveOnlyMessage(host, port string) string {
	return fmt.Sprintf("Connect a client to: http://%s:%s\n\n"+
		"Optional: set OPENCODE_SERVER_PASSWORD (and OPENCODE_SERVER_USERNAME) to protect the server with basic auth.\n"+
		"Press Ctrl-D to stop serving.", host, port)
}

// runServeOnly keeps the VM alive and blocks until ctx is done (CTRL-D or
// SIGINT), without attaching an in-VM TUI. It holds the VM via a keeper exec so
// the msb idle timeout does not stop it while serving.
func runServeOnly(ctx context.Context, sb msb.Sandbox, ui termio.UI, hostPort int) error {
	host := options.ServeOnlyBindAddr
	port := strconv.Itoa(hostPort)
	ui.Infof("%s", serveOnlyMessage(host, port))
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
func Run(ctx context.Context, opts options.RunOptions, ui termio.UI) error {
	ses, err := prepareSandbox(ctx, opts, ui)
	if err != nil {
		return err
	}
	defer ses.Cleanup()
	sb := ses.Sandbox()

	if opts.DryRun {
		ui.Infof("dry-run: Would run agent session")
		return nil
	}
	if opts.DryRunVM && sb == nil {
		ui.Infof("dry-run: Would start agent session in VM")
		return nil
	}

	projectSlug := git.ProjectSlug()

	a, _ := agent.Lookup(opts.Agent)
	var streamSpec *agent.EventStreamSpec
	if provider, ok := agent.AsEventStreamProvider(a); ok {
		eventStream := provider.EventStream()
		streamSpec = &eventStream
	}
	stopNotify := startNotifyWatcher(ctx, sb, opts.Notify, ui, streamSpec, projectSlug)
	defer stopNotify()

	if opts.ServeOnly { //nolint:nestif // lease acquire/serve/release/reap sequence requires this structure
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
		if err := runServeOnly(ctx, sb, ui, ses.ServeHostPort()); err != nil && !errors.Is(err, context.Canceled) {
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

	setup := buildAttachCommand(a, ses.Target(), opts.Args)
	ui.Verbosef("%s", setup)
	// Run as a login shell so /etc/profile and ~/.profile are sourced,
	// putting tools installed under /usr/local/go/bin, ~/go/bin and
	// ~/.microsandbox/bin on PATH for the agent and its child shells.
	return runAttach(ctx, sb, projectSlug, ui, opts, "-l", "-c", setup)
}

// Shell creates (or reuses) the project VM and drops the user into an
// interactive shell session, without starting the agent serve daemon.
func Shell(ctx context.Context, opts options.RunOptions, ui termio.UI) error {
	ses, err := prepareSandbox(ctx, opts, ui)
	if err != nil {
		return err
	}
	defer ses.Cleanup()
	sb := ses.Sandbox()

	if opts.DryRun {
		ui.Infof("dry-run: Would start interactive shell session")
		return nil
	}
	if opts.DryRunVM && sb == nil {
		ui.Infof("dry-run: Would start interactive shell session")
		return nil
	}

	projectSlug := git.ProjectSlug()
	return runAttach(ctx, sb, projectSlug, ui, opts, "-l")
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
// reap-on-last-client, and finalize sequence for Run and Shell.
func runAttach(
	ctx context.Context,
	sb msb.Sandbox,
	projectSlug string,
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
	if opts.Root {
		exitCode, attachErr = sb.AttachWith(ctx, "/bin/bash", bashArgs, msbSdk.WithAttachUser(sandbox.RootUser))
	} else {
		exitCode, attachErr = sb.Attach(ctx, "/bin/bash", bashArgs...)
	}

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
