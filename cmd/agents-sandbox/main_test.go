package main

import (
	"context"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
	mocks "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	// Seal the run/shell commands off from the developer's real legacy home: the
	// boundary seam var defaults to a no-op migration offer, and the tests that
	// exercise it opt in. Keep the upgrade check off the network: any test that
	// reaches it must stub the seam explicitly (a panic, not an error, so a
	// swallowed warning cannot hide the leak).
	runSeams.RuntimeHomeMigrationOffer = func(context.Context, msb.Client, termio.UI) error { return nil }
	runSeams.AgentLatestVersion = func(context.Context, agent.Agent) (string, error) {
		panic("agent latest-version lookup not stubbed in this test")
	}
	mocks.InitFailFastMocks(m)
}
