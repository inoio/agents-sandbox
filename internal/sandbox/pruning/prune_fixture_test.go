package pruning

import (
	"testing"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"

	cp "gitlab.inoio.de/inoio/opencode-sandbox/internal/configpaths"
)

// setupPruningFixtures installs the default happy-path mocks shared by most
// pruning unit tests and returns the mock handles tests commonly inspect or
// customize. Tests that need a non-default fixture override it afterwards.
func setupPruningFixtures(
	t *testing.T,
) (client *msb.MockMsbClient, dockerMock *mockDockerClient, ui *termio.Mock, report *StaleReport) {
	t.Helper()
	cp.WithMockConfigPaths(t)
	client = &msb.MockMsbClient{}
	dockerMock = &mockDockerClient{}
	docker.WithDockerMock(t, dockerMock)
	ui = newMockUI()
	report = &StaleReport{}
	return client, dockerMock, ui, report
}
