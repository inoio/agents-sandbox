package volume

import (
	"testing"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

// setupVolumeOpsFixtures installs the isolate state dir and the default msb
// mock needed by the volume operations tests and returns the mock handles.
// Tests that need a pre-existing home state or a customized msb mock declare
// it after the call.
func setupVolumeOpsFixtures(t *testing.T) (*msb.MockMsbClient, *termio.Mock) {
	t.Helper()
	configpaths.WithMockConfigPaths(t)
	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)
	return mock, &termio.Mock{}
}
