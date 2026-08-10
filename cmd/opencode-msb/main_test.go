package main

import (
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.InitMocks(m,
		sandbox.InstallFailFastConfigPaths,
		docker.InstallFailFastGet,
		msb.InstallFailFastGet,
	)
}
