package session

import (
	"context"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	mocks "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	// A mock whose sessions never quiesce must fail fast, not poll out the
	// production interval; a test that needs the real interval overrides it.
	waitQuiescentPollInterval = 10 * time.Millisecond
	// The host's RAM shapes a memory-raise offer (the boot maximum is the host's
	// whole-GiB RAM), so it is pinned rather than read from the machine running
	// the suite. A test that needs a different host pins its own value.
	bootMaxMemoryMiB = func() uint32 { return 64 << 10 }
	hostAvailableMemoryMiB = func() (uint32, bool) { return 32 << 10, true }
	// A failed attach looks the project VM up to tell a stopped VM from a lost
	// connection; the suite's VM is running unless a test says otherwise.
	projectVMStatus = func(context.Context, state.Key) (msb.SandboxStatus, error) {
		return msb.SandboxStatusRunning, nil
	}
	mocks.InitFailFastMocks(m)
}
