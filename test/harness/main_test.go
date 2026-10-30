package harness

import (
	"testing"

	mocks "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

// TestMain installs the fail-fast seam mocks and nothing else: under
// `compile-all`'s -run=^$, `m.Run()` returns immediately, so the package stays
// inert without a VM.
func TestMain(m *testing.M) {
	mocks.InitFailFastMocks(m)
}
