package main

import (
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
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

// overrideMsbClient saves the original factory, replaces it with one that
// returns the provided mock, and restores the original on test cleanup.
func overrideMsbClient(t *testing.T, mock sandbox.MsbClient) {
	t.Helper()
	orig := sandbox.SetNewMsbClient(func() sandbox.MsbClient {
		return mock
	})
	t.Cleanup(func() {
		sandbox.SetNewMsbClient(orig)
	})
}

// TestFixtureHelpers compiles-check that all fixture helpers and flags are
// accessible from a test context. No assertions are performed.
func TestFixtureHelpers(t *testing.T) {
	_ = FlagSet(nil)
	_ = stopKillFlags
	_ = pruneAgeFlags
	_ = overrideMsbClient
	t.Log("fixture helpers compile-ok")
}
