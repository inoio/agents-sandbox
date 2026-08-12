package sandbox

import (
	"context"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/image"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/session"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

// ExitError re-exported from session for cmd compatibility.
type ExitError = session.ExitError

// Re-exported session module symbols preserve the public API of the sandbox
// core so that cmd/opencode-msb and internal consumers continue to work
// without changing their import paths. Only cmd-referenced symbols are re-exported.

// Run creates (or reuses) the project VM, provisions config, starts opencode
// serve, and attaches a TUI client.
func Run(ctx context.Context, opts RunOptions, ui termio.UI) error {
	return session.Run(ctx, opts, ui)
}

// Shell creates (or reuses) the project VM and drops the user into an
// interactive shell session, without starting opencode serve.
func Shell(ctx context.Context, opts RunOptions, ui termio.UI) error {
	return session.Shell(ctx, opts, ui)
}

// BuildImage builds (or updates) the runner image for Docker-in-Docker support.
func BuildImage(ctx context.Context, force, dryRun bool, ui termio.UI) error {
	return session.BuildImage(ctx, force, dryRun, ui)
}

// StopProjectVM gracefully stops the project VM for the current directory.
func StopProjectVM(ctx context.Context, remove, dryRun bool, ui termio.UI) error {
	return session.StopProjectVM(ctx, remove, dryRun, ui)
}

// KillProjectVM force-kills the project VM for the current directory.
func KillProjectVM(ctx context.Context, remove, dryRun bool, ui termio.UI) error {
	return session.KillProjectVM(ctx, remove, dryRun, ui)
}

// ResolveWorktreeSpec parses a --worktree value and returns a WorktreeSpec.
//
//nolint:wrapcheck // wrapping would add no value over the session-level error
func ResolveWorktreeSpec(value string) (WorktreeSpec, error) {
	return session.ResolveWorktreeSpec(value)
}

// ListSandboxes returns a list of sandbox VMs for the current host.
//
//nolint:wrapcheck // wrapping would add no value over the session-level error
func ListSandboxes(ctx context.Context) ([]Info, error) {
	return session.ListSandboxes(ctx)
}

// SetDaemonShellFunc replaces the daemon shell function used by ensureDaemon.
// It returns the original function to restore after tests.
//
//nolint:wrapcheck // wrapper preserving the session test seam
func SetDaemonShellFunc(
	f func(ctx context.Context, sb Sandbox, command string) (string, int, error),
) func(ctx context.Context, sb Sandbox, command string) (string, int, error) {
	return session.SetDaemonShellFunc(f)
}

// Info is a re-export of session.Info so that cmd continues to compile.
type Info = session.Info

// ImageInfo is an alias for image.Info from the image module.
type ImageInfo = image.Info

// ListImages re-exports the image module's ListImages through the sandbox core.
func ListImages(ctx context.Context) ([]ImageInfo, error) {
	return image.ListImages(ctx)
}
