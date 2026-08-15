package session

import (
	"context"
	"fmt"
	"time"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

const (
	dockerdBinaryCheckCmd = "test -x /usr/bin/dockerd"
	dockerdReadyCmd       = "docker info"
	dockerdRestartCmd     = "pkill dockerd >/dev/null 2>&1 || true; " +
		"pkill containerd >/dev/null 2>&1 || true; " +
		"find /run /var/run -iname 'docker*.pid' -delete >/dev/null 2>&1 || true; " +
		"find /run /var/run \\( -iname '*containerd*.sock' -o -iname '*containerd*.pid' \\) -delete >/dev/null 2>&1 || true; " +
		"nohup dockerd -H unix:///var/run/docker.sock > /var/log/dockerd.log 2>&1 &"
)

var (
	dockerdReadyTimeout = 30 * time.Second //nolint:gochecknoglobals // test seam, swapped in tests
	dockerdPollInterval = time.Second      //nolint:gochecknoglobals // test seam, swapped in tests
)

// startDockerdIfPresent starts dockerd inside the VM if the dind image is in
// use, or does nothing otherwise. It is safe to call on every VM bootstrap
// because it checks for the dockerd binary and handles the case where dockerd
// is already running.
func startDockerdIfPresent(ctx context.Context, sb msb.Sandbox, ui termio.UI) error {
	out, checkErr := sb.Shell(ctx, dockerdBinaryCheckCmd, msbSdk.WithExecUser("root"))
	if checkErr != nil {
		return fmt.Errorf("while checking dockerd binary: %w", checkErr)
	}
	if !out.Success() {
		ui.Verbosef("/usr/bin/dockerd not present, skipping Docker startup")
		return nil
	}

	// Check if an existing dockerd is already healthy
	if infoOut, readyErr := sb.Shell(
		ctx,
		dockerdReadyCmd,
		msbSdk.WithExecUser("dev"),
	); readyErr == nil &&
		infoOut.Success() {
		ui.Verbose("using already running dockerd")
		return nil
	}

	ui.Verbosef("starting dockerd with vfs storage driver")
	// The command background-launches dockerd, so the shell always exits 0; a
	// startup failure surfaces through the readiness loop below, not the exit
	// code. Only a hard exec failure is fatal here.
	if _, restartErr := sb.Shell(ctx, dockerdRestartCmd, msbSdk.WithExecUser("root")); restartErr != nil {
		return fmt.Errorf("start dockerd: %w (no result)", restartErr)
	}

	deadline := time.Now().Add(dockerdReadyTimeout)
	for time.Now().Before(deadline) {
		out, pollErr := sb.Shell(ctx, dockerdReadyCmd, msbSdk.WithExecUser("dev"))
		if pollErr == nil && out.Success() {
			ui.Verbosef("dockerd is ready")
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(dockerdPollInterval):
			if out != nil {
				ui.Verbosef("dockerd readiness: err=%v, exit=%d, stdout=%q, stderr=%q",
					pollErr, out.ExitCode(), out.Stdout(), out.Stderr())
			} else {
				ui.Verbosef("dockerd readiness: err=%v", pollErr)
			}
		}
	}
	data, err := sb.FS().ReadString(ctx, "/var/log/dockerd.log")
	if err == nil && data != "" {
		return fmt.Errorf(
			"dockerd did not become ready within %s; dockerd log:\n%s",
			dockerdReadyTimeout, data,
		)
	}
	return fmt.Errorf("dockerd did not become ready within %s (dockerd log unavailable)", dockerdReadyTimeout)
}
