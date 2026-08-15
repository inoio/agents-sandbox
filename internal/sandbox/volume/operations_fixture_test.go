package volume

import (
	"testing"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/state"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

// setupVolumeOpsFixtures installs the isolate state dir and the default msb
// mock needed by the volume operations tests and returns the mock handles.
// Tests that need a pre-existing home state or a customized msb mock declare
// it after the call.
//
//nolint:unused // used by volume operations_test.go conversion (Task 4)
func setupVolumeOpsFixtures(t *testing.T) (*msb.MockMsbClient, *termio.Mock) {
	t.Helper()
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-sandbox")
	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)
	return mock, &termio.Mock{}
}
