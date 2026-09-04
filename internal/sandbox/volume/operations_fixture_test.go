package volume

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
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
	docker.WithNoopDockerMock(t)
	return mock, &termio.Mock{}
}
