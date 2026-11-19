package scripts

import (
	"strings"
	"syscall"
	"testing"

	mocks "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	Register("gh", fakeGhHandler)
	Register("asb", surfaceSweepAsbHandler)
	Register("docker", surfaceSweepDockerHandler)
	Register("faketool", func(args []string) (string, string, int) {
		return "out:" + strings.Join(args, ","), "err\n", 3
	})
	// A fake exits through syscall.Exit, not os.Exit: this harness package has
	// only _test.go files, so its test binary carries no coverage metadata and
	// the Go coverage runtime's exit hook would print "program not built with
	// -cover" to the fake's stderr under `go test -cover` (the codecov and
	// mutation runs). The raw exit skips that hook.
	if status := Dispatch(); status >= 0 {
		syscall.Exit(status)
	}
	mocks.InitFailFastMocks(m)
}
