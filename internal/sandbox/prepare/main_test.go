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
	// Seal PrepareSandbox off from the developer's real legacy home: the default
	// offer is a no-op, and the tests that exercise it opt in explicitly.
	SetRuntimeHomeMigrationOffer(func(context.Context, msb.Client, termio.UI) error { return nil })
	// No existing runner image by default; recovery tests opt in explicitly.
	existingAgentVersion = func(context.Context, docker.Client, string, string) (string, bool) { return "", false }
	mocks.InitFailFastMocks(m)
}
