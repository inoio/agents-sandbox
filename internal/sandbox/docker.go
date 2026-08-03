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
	dockerdCheckCmd     = "test -x /usr/bin/dockerd"
	dockerdStartCmd     = "find /run /var/run -iname 'docker*.pid' -delete 2>/dev/null || : && dockerd -H unix:///var/run/docker.sock > /var/log/dockerd.log 2>&1 &"
	dockerdReadyCmd     = "docker info"
	dockerdReadyTimeout = 30 * time.Second
	dockerdPollInterval = time.Second
)

func startDockerdIfPresent(ctx context.Context, sb msbSandbox, ui stdio.UI) error {
	out, err := sb.Shell(ctx, dockerdCheckCmd, msb.WithExecUser("root"))
	if err != nil {
		return fmt.Errorf("check dockerd binary: %w", err)
	}
	if !out.Success() {
		ui.Verbosef("dockerd not present, skipping Docker startup")
		return nil
	}

	ui.Verbosef("starting dockerd with vfs storage driver")
	if _, err := sb.Shell(ctx, dockerdStartCmd, msb.WithExecUser("root")); err != nil {
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
		}
	}
	return errors.New("dockerd did not become ready within " + dockerdReadyTimeout.String())
}
