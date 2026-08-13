package main

import (
	"testing"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.InitMocks(m,
		configpaths.InstallFailFastConfigPaths,
		docker.InstallFailFastGet,
		msb.InstallFailFastGet,
	)
}
