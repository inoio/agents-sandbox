package main

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/builder/cacheregistry"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	mocks "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	// Serve the embedded registry daemon if this test binary was re-exec'd as
	// it, instead of re-running the suite.
	cacheregistry.RunIfDaemonInvocation()
	// Keep delivery offline: resolving a pull ref would otherwise spawn the
	// registry daemon (and re-run the suite under `go test`).
	image.InstallOfflineImageDelivery()
	mocks.InstallFailFastMocks()
	doctor.InstallFailFast()
	m.Run()
}
