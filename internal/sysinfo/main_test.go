// Package sysinfo_test keeps TestMain external: the fail-fast installer now
// reaches the host-figure seam, which reads sysinfo, so an internal TestMain
// importing it would cycle.
package sysinfo_test

import (
	"testing"

	mocks "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	mocks.InitFailFastMocks(m)
}
