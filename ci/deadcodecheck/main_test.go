package main

import (
	"testing"

	mocks "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

// TestMain installs the fail-fast seam defaults. This package reaches no seam
// itself, but the test-isolation contract requires every test package to
// install the families its import graph allows.
func TestMain(m *testing.M) {
	mocks.InitFailFastMocks(m)
}
