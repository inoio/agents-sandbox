package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/moby/moby/client"

	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

//nolint:gochecknoglobals // test seams
var (
	checkAllFunc      = realCheckAll
	checkDockerFunc   = realCheckDocker
	collectChecksFunc = collectChecks
)

// CheckDocker reports whether the docker binary is on PATH.
func CheckDocker(ctx context.Context) error {
	return checkDockerFunc(ctx)
}

// CheckAll runs every prerequisite check, rendering all failures and any
// non-fatal warnings through ui. It returns true only when all checks pass.
func CheckAll(ctx context.Context, ui termio.UI) bool {
	return checkAllFunc(ctx, ui)
}

// realCheckAll aggregates the platform checks and reports the results.
func realCheckAll(ctx context.Context, ui termio.UI) bool {
	infos, errs := collectChecksFunc(ctx)
	ok := len(errs) == 0
	for _, info := range infos {
		ui.Info(info)
	}
	for _, err := range errs {
		ui.Errorf("%s", err)
	}
	return ok
}

// collectChecks runs every prerequisite check, collecting all failures so
// CheckAll can report them together. checkPlatform is the only platform
// specific check and comes first.
func collectChecks(ctx context.Context) ([]string, []error) {
	var infos []string
	var errs []error
	for _, err := range []error{
		checkPlatform(),
		CheckDocker(ctx),
	} {
		if err != nil {
			errs = append(errs, err)
		}
	}
	msbMessages, err := checkMsb(ctx)
	infos = append(infos, msbMessages...)
	if err != nil {
		errs = append(errs, err)
	}
	return infos, errs
}

// realCheckDocker pings the Docker daemon, describing how to fix it on failure.
func realCheckDocker(ctx context.Context) error {
	//nolint:exhaustruct // NegotiateAPIVersion/ForceNegotiate not needed for a simple ping check
	_, err := docker.Get().Ping(ctx, client.PingOptions{})
	if err != nil {
		return fmt.Errorf(
			"docker API unreachable: %w; ensure Docker Desktop or colima is running, or verify DOCKER_HOST",
			err,
		)
	}
	return nil
}

// checkMsb ensures the msb runtime is installed, returning non-fatal PATH
// guidance as warnings when msb is installed but not on PATH.
func checkMsb(ctx context.Context) ([]string, error) {
	if err := ensureMsbInstalled(ctx); err != nil {
		return nil, fmt.Errorf("msb runtime setup failed: %w", err)
	}
	if _, err := exec.LookPath("msb"); err == nil {
		return nil, nil
	}
	home, binDir, binPath, err := msbBinPath()
	if err != nil {
		return nil, err
	}
	return pathHints(home, os.Getenv("SHELL"), binDir, binPath), nil
}
