package main

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
	"github.com/inoio/opencode-sandbox/internal/sandbox/doctor"
	"github.com/inoio/opencode-sandbox/internal/sandbox/image"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// FlagSet is a permutation of CLI flag arguments (one variation per test run).
type FlagSet []string

// stopKillFlags contains --force/--dry-run flag variations for the stop and kill
// commands. All combinations are valid and should produce the same behavior.
//
//nolint:gochecknoglobals // fixture data shared across parameterized tests
var stopKillFlags = []FlagSet{
	{"--force", "--dry-run"},
	{"-f", "-n"},
}

// pruneAgeFlags contains --age threshold variations for the prune command.
// All represent different valid age specifications that should produce
// the same command structure (only the value differs at this layer).
//
//nolint:gochecknoglobals // fixture data shared across parameterized tests
var pruneAgeFlags = []FlagSet{
	{"--age", "7d"},
	{"-a", "7d"},
	{"-a", "2w"},
	{"--age", "14d"},
}

func setupCommandFixtures(t *testing.T, args ...string) (*cobra.Command, *termio.Mock) {
	t.Helper()
	configpaths.WithMockConfigPaths(t)
	image.WithMockOpenCodeVersion(t, "0.0.0-test")
	msb.WithMsbMock(t, &msb.MockMsbClient{})
	docker.WithNoopDockerMock(t)
	doctor.MockedCheckAll(t, true)
	doctor.MockedCheckDocker(t, true)
	ui := &termio.Mock{}
	cmd := buildRootCmd(ui)
	cmd.SetArgs(args)
	return cmd, ui
}
