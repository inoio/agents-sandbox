package doctor

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/docslink"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/runtime"
	"github.com/inoio/agents-sandbox/internal/termio"
)

var (
	checkAllFunc      = realCheckAll
	checkDockerFunc   = realCheckDocker
	collectChecksFunc = collectChecks
)

// CheckDocker reports whether the docker binary is on PATH. The Docker client
// is injected from the command boundary rather than read from docker.Get().
func CheckDocker(ctx context.Context, dclient docker.Client) error {
	return checkDockerFunc(ctx, dclient)
}

// CheckAll runs every prerequisite check, rendering all failures and any
// non-fatal warnings through ui. It returns true only when all checks pass.
func CheckAll(ctx context.Context, dclient docker.Client, ui termio.Logger) bool {
	return checkAllFunc(ctx, dclient, ui)
}

// checkResults is what a prerequisite run produced: informational messages and
// the fatal failures.
type checkResults struct {
	infos []string
	errs  []error
}

// realCheckAll aggregates the platform checks and reports the results.
func realCheckAll(ctx context.Context, dclient docker.Client, ui termio.Logger) bool {
	results := collectChecksFunc(ctx, dclient)
	ok := len(results.errs) == 0
	for _, info := range results.infos {
		ui.Info(info)
	}
	for _, err := range results.errs {
		ui.Errorf("%s", err)
	}
	return ok
}

// collectChecks runs every prerequisite check, collecting all failures so
// CheckAll can report them together. checkPlatform is the only platform
// specific check and comes first.
func collectChecks(ctx context.Context, dclient docker.Client) checkResults {
	var results checkResults
	for _, err := range []error{
		checkPlatform(),
		CheckDocker(ctx, dclient),
	} {
		if err != nil {
			results.errs = append(results.errs, err)
		}
	}
	msbMessages, err := checkMsb(ctx)
	results.infos = append(results.infos, msbMessages...)
	if err != nil {
		results.errs = append(results.errs, err)
	}
	return results
}

// realCheckDocker pings the container engine's Docker API, describing how to
// fix it on failure.
func realCheckDocker(ctx context.Context, dclient docker.Client) error {
	_, err := dclient.Ping(ctx, client.PingOptions{})
	if err != nil {
		return fmt.Errorf(
			"container engine (Docker API) unreachable: %w\n%s",
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
			err, docslink.SectionURL("troubleshooting", "microsandbox-runtime"),
		)
	}
	home := runtime.PrivateHome()
	return []string{fmt.Sprintf("msb runtime installed at %s", filepath.Join(home, "bin"))}, nil
}
