package prepare

import (
	"context"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	// Pin the host's available memory so the memory-raise offer does not depend
	// on the machine running the suite; the seam-contract test asserts the
	// captured default is the production read.
	hostAvailableMemoryMiB = func() (uint32, bool) { return 64 << 10, true }
	// Seal PrepareSandbox off from the developer's real legacy home: the default
	// offer is a no-op, and the tests that exercise it opt in explicitly.
	offerRuntimeHomeMigration = func(context.Context, msb.Client, termio.UI) error { return nil }
	// No existing runner image by default; recovery tests opt in explicitly.
	existingAgentVersion = func(context.Context, docker.Client, string, string) (string, bool) { return "", false }
	mocks.InitFailFastMocks(m)
}

// capturedHostAvailableMemoryMiB holds the production available-memory read,
// captured before TestMain pins the seam.
var capturedHostAvailableMemoryMiB = hostAvailableMemoryMiB
