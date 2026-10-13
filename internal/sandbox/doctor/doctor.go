package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/inoio/agents-sandbox/internal/sandbox/builder"
	"github.com/inoio/agents-sandbox/internal/termio"
)

const backendMoby = "moby"

var (
	checkAllFunc      = realCheckAll
	collectChecksFunc = collectChecks
	selectedBackend   = builder.SelectedName
	checkDocker       = realCheckDocker
)

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
// specific check and comes first. The Docker check applies only to the moby
// builder backend; the default msb backend needs no host Docker.
func collectChecks(ctx context.Context) ([]string, []error) {
	var errs []error
	if err := checkPlatform(); err != nil {
		errs = append(errs, err)
	}
	if selectedBackend() == backendMoby {
		if err := checkDocker(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	msbMessages, err := checkMsb(ctx)
	infos := append([]string{}, msbMessages...)
	if err != nil {
		errs = append(errs, err)
	}
	return infos, errs
}

// realCheckDocker reports whether the Docker daemon the moby backend targets is
// reachable.
func realCheckDocker(ctx context.Context) error {
	return builder.PingDocker(ctx)
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
