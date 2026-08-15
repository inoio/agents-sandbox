package main

import (
	"testing"

	"github.com/spf13/cobra"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/doctor"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

func setupCommandFixtures(t *testing.T, args ...string) (*cobra.Command, *termio.Mock) {
	t.Helper()
	configpaths.WithMockConfigPaths(t)
	msb.WithMsbMock(t, &msb.MockMsbClient{})
	docker.WithNoopDockerMock(t)
	doctor.MockedCheckAll(t, true)
	doctor.MockedCheckDocker(t, true)
	ui := &termio.Mock{}
	cmd := buildRootCmd(ui)
	cmd.SetArgs(args)
	return cmd, ui
}
