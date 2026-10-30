package main

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// FlagSet is a permutation of CLI flag arguments (one variation per test run).
type FlagSet []string

// stopKillFlags contains --force/--dry-run flag variations for the stop and kill
// commands. All combinations are valid and should produce the same behavior.
var stopKillFlags = []FlagSet{
	{"--force", "--dry-run"},
	{"-f", "-n"},
}

// pruneAgeFlags contains --older-than threshold variations for the prune
// command, plus one deprecated --age/-a case proving the old spelling still
// works. All represent different valid age specifications that should produce
// the same command structure (only the value differs at this layer).
var pruneAgeFlags = []FlagSet{
	{"--older-than", "7d"},
	{"--older-than", "2w"},
	{"--older-than", "14d"},
	{"--age", "7d"},
	{"-a", "2w"},
}

func setupCommandFixtures(t *testing.T, args ...string) (*cobra.Command, *termio.Mock) {
	t.Helper()
	configpaths.WithMockConfigPaths(t)
	image.WithMockAgentVersion(t, "0.0.0-test")
	msb.WithMsbMock(t, &msb.MockMsbClient{})
	docker.WithNoopDockerMock(t)
	doctor.MockedCheckAll(t, true)
	doctor.MockedCheckDocker(t, true)
	ui := &termio.Mock{}
	cmd := buildRootCmd(ui)
	cmd.SetArgs(args)
	return cmd, ui
}
