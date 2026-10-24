package doctor

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/runtime"
	"github.com/inoio/agents-sandbox/internal/termio"
)

var (
	checkAllFunc      = realCheckAll
	checkDockerFunc   = realCheckDocker
	collectChecksFunc = collectChecks
)

// docsBaseURL is the published documentation site that doctor links failures to.
const docsBaseURL = "https://inoio.github.io/agents-sandbox"

// troubleshootingURL returns the troubleshooting-page section for a resolved
// problem, so a failing check ends with where to read the fix.
func troubleshootingURL(anchor string) string {
	return docsBaseURL + "/troubleshooting.html#" + anchor
}

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
	_, err := docker.Get().Ping(ctx, client.PingOptions{})
	if err != nil {
		return fmt.Errorf(
			"docker API unreachable: %w\n%s",
			err, dockerRemedyHint(),
		)
	}
	return nil
}

// checkMsb ensures the private msb runtime is installed and reports where it
// lives. agents-sandbox owns the runtime at runtime.PrivateHome() and never
// advertises a user-facing msb, so no PATH or shell-rc guidance is emitted; a
// missing binary is not a user defect.
func checkMsb(ctx context.Context) ([]string, error) {
	if err := ensureMsbInstalled(ctx); err != nil {
		return nil, fmt.Errorf(
			"msb runtime setup failed: %w\n  Docs: %s",
			err, troubleshootingURL("microsandbox-runtime"),
		)
	}
	home := runtime.PrivateHome()
	return []string{fmt.Sprintf("msb runtime installed at %s", filepath.Join(home, "bin"))}, nil
}
