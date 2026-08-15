package main

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
