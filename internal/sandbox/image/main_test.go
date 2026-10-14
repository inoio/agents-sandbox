package image

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/builder/cacheregistry"
	termio "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

// integrationTestCleanup, when set by an integration-tagged file, removes
// process-shared resources (e.g. the run-wide registry cache directory) after
// the tests finish. It is a variable so the default build carries no dependency
// on integration-only files.
var integrationTestCleanup func()

func TestMain(m *testing.M) {
	if isRegistryDaemonInvocation() {
		runRegistryDaemon()
	}
	termio.InstallFailFastMocks()
	code := m.Run()
	if integrationTestCleanup != nil {
		integrationTestCleanup()
	}
	os.Exit(code)
}

// isRegistryDaemonInvocation reports whether this test binary was re-exec'd as
// the embedded cache registry daemon. cacheregistry.Ensure spawns
// os.Executable(), which under `go test` is this test binary rather than the
// agents-sandbox CLI, so integration builds that need the registry must serve
// the daemon here instead of re-running the whole suite.
func isRegistryDaemonInvocation() bool {
	return len(os.Args) >= 2 && os.Args[1] == cacheregistry.CommandName
}

func runRegistryDaemon() {
	if err := cacheregistry.ServeCommandArgs(context.Background(), os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "builder registry daemon:", err)
		os.Exit(1)
	}
	os.Exit(0)
}
