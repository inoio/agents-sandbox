package session

import (
	"testing"
	"time"

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
	mocks.InitFailFastMocks(m)
}
