package session

import (
	"context"
	"errors"
	"fmt"
	"strings"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/opencode-sandbox/internal/git"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/sandbox/sandbox"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

func buildAttachCommand(target string, args []string) string {
	parts := []string{"opencode", "attach", "http://127.0.0.1:4096", "--dir", target}
	parts = append(parts, args...)

	return strings.Join(parts, " ")
}

// serveOnlyMessage builds the message printed when serving opencode for
// external clients such as Opencode Desktop.
func serveOnlyMessage(host, port string) string {
	return fmt.Sprintf("Connect Opencode Desktop to: http://%s:%s\n\n"+
		"Optional: set OPENCODE_SERVER_PASSWORD (and OPENCODE_SERVER_USERNAME) to protect the server with basic auth.\n"+
		"Press Ctrl-D to stop serving.", host, port)
}

// runServeOnly keeps the VM alive and blocks until ctx is done (CTRL-D or
// SIGINT), without attaching an in-VM TUI. It holds the VM via a keeper exec so
// the msb idle timeout does not stop it while serving.
func runServeOnly(ctx context.Context, sb msb.Sandbox, ui termio.UI) error {
	host := options.ServeOnlyBindAddr
	port := options.ServeOnlyPort
	ui.Infof("%s", serveOnlyMessage(host, port))
	keeperCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	keeperDone := keepVMAlive(keeperCtx, sb)
	defer func() { _ = keeperDone() }()
	<-ctx.Done()
	return ctx.Err()
}

// Run creates (or reuses) the project VM, provisions config, starts opencode
// serve, and attaches a TUI client.
//
// Note: Run is called from cli.go after all flags are resolved.
func Run(ctx context.Context, opts options.RunOptions, ui termio.UI) error {
	ses, err := sandbox.PrepareSandbox(ctx, opts, ui)
	if err != nil {
		return err
	}
	defer ses.Cleanup()
	sb := ses.Sandbox()

	if opts.DryRun {
		ui.Infof("dry-run: Would run opencode")
		return nil
	}
	if opts.DryRunVM && sb == nil {
		ui.Infof("dry-run: Would start opencode in VM")
		return nil
	}

	projectSlug := git.ProjectSlug()

	if opts.ServeOnly { //nolint:nestif // lease acquire/serve/release/reap sequence requires this structure
		release, acquireErr := state.AcquireClientLease(projectSlug)
		if acquireErr != nil {
			ui.Warnf("client lease failed: %v", acquireErr)
		}
		defer func() {
			if acquireErr == nil && release != nil {
				release()
			}
		}()
		if err := runServeOnly(ctx, sb, ui); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		if acquireErr == nil {
			release()
			release = nil
		}
		if err := reapOnLastClient(ctx, projectSlug, sb, opts.ReapPolicy, ui); err != nil {
			ui.Warnf("reap failed: %v", err)
		}
		return &sandbox.ExitError{Code: 0}
	}

	setup := buildAttachCommand(ses.Target(), opts.Args)
	ui.Verbosef("%s", setup)
	// Run as a login shell so /etc/profile and ~/.profile are sourced,
	// putting tools installed under /usr/local/go/bin, ~/go/bin and
	// ~/.microsandbox/bin on PATH for opencode and its child shells.
	return runAttach(ctx, sb, projectSlug, ui, opts, "-l", "-c", setup)
}

// Shell creates (or reuses) the project VM and drops the user into an
// interactive shell session, without starting opencode serve.
func Shell(ctx context.Context, opts options.RunOptions, ui termio.UI) error {
	ses, err := sandbox.PrepareSandbox(ctx, opts, ui)
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
		return fmt.Errorf("opencode session failed: %w", attachErr)
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
	// Acquire a client lease so state tracks this session.
	release, acquireErr := state.AcquireClientLease(projectSlug)
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

	if err := reapOnLastClient(ctx, projectSlug, sb, opts.ReapPolicy, ui); err != nil {
		ui.Warnf("reap failed: %v", err)
	}

	return finalizeRun(attachErr, exitCode)
}
