// Package hostfigures_test keeps TestMain external: the fail-fast installer
// reaches this package's own seam through mocks, so an internal TestMain
// importing it would cycle.
package hostfigures_test

import (
	"testing"

	mocks "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	mocks.InitFailFastMocks(m)
}
