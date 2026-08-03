package sandbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

const (
	dockerdBinaryCheckCmd = "test -x /usr/bin/dockerd"
	dockerdReadyCmd       = "docker info"
	dockerdRestartCmd     = "pkill dockerd 2>/dev/null || : && find /run /var/run -iname 'docker*.pid' -delete 2>/dev/null && sleep 1 && dockerd -H unix:///var/run/docker.sock > /var/log/dockerd.log 2>&1 &"
	dockerdReadyTimeout   = 10 * time.Second
	dockerdPollInterval   = time.Second
)

func startDockerdIfPresent(ctx context.Context, sb msbSandbox, ui stdio.UI) error {
	out, err := sb.Shell(ctx, dockerdBinaryCheckCmd, msb.WithExecUser("root"))
	if err != nil {
		return fmt.Errorf("while checking dockerd binary: %w", err)
	}
	if !out.Success() {
		ui.Verbosef("/usr/bin/dockerd not present, skipping Docker startup")
		return nil
	}

	// Check if an existing dockerd is already healthy
	if infoOut, err := sb.Shell(ctx, dockerdReadyCmd, msb.WithExecUser("dev")); err == nil && infoOut.Success() {
		ui.Verbose("using already running dockerd")
		return nil
	}

	ui.Verbosef("starting dockerd with vfs storage driver")
	if _, err := sb.Shell(ctx, dockerdRestartCmd, msb.WithExecUser("root")); err != nil {
		return fmt.Errorf("start dockerd: %w", err)
	}

	deadline := time.Now().Add(dockerdReadyTimeout)
	for time.Now().Before(deadline) {
		out, err := sb.Shell(ctx, dockerdReadyCmd, msb.WithExecUser("dev"))
		if err == nil && out.Success() {
			ui.Verbosef("dockerd is ready")
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(dockerdPollInterval):
			if out != nil {
				ui.Verbosef("dockerd readiness: err=%v, exit=%d, stdout=%q, stderr=%q",
					err, out.ExitCode(), out.Stdout(), out.Stderr())
			} else {
				ui.Verbosef("dockerd readiness: err=%v", err)
			}
		}
	}
	data, err := sb.FS().ReadString(ctx, "/var/log/dockerd.log")
	if err != nil {
		return err
	}
	ui.Verbosef("dockerd log:\n%s", data)
	return errors.New("dockerd did not become ready within " + dockerdReadyTimeout.String())
}
