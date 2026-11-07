package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// withMockAgentVersion pins the agent release the command boundary resolves for
// a build and the release the run path's upgrade check resolves, restoring both
// seams when the test ends. It replaces the retired image.WithMockAgentVersion.
func withMockAgentVersion(t *testing.T, v string) {
	t.Helper()
	withMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, requested string) (string, error) {
		if requested != "" {
			return requested, nil
		}
		return v, nil
	})
}

// withMockAgentVersionResolver installs a custom resolver on both the command
// boundary (build/volume image ensure) and the run path (prepare's build
// resolver and upgrade check).
func withMockAgentVersionResolver(t *testing.T, resolve image.AgentVersionResolver) {
	t.Helper()
	origResolver := agentVersionResolver
	agentVersionResolver = resolve
	origSeams := runSeams
	runSeams.AgentLatestVersion = func(ctx context.Context, a agent.Agent) (string, error) {
		return resolve(ctx, a, "")
	}
	t.Cleanup(func() {
		agentVersionResolver = origResolver
		runSeams = origSeams
	})
}

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
	withMockAgentVersion(t, "0.0.0-test")
	msb.WithMsbMock(t, &msb.MockMsbClient{})
	docker.WithNoopDockerMock(t)
	doctor.MockedCheckAll(t, true)
	doctor.MockedCheckDocker(t, true)
	ui := &termio.Mock{}
	cmd := buildRootCmd(ui)
	cmd.SetArgs(args)
	return cmd, ui
}

// originCloneURL is the shared origin URL for the identity fixtures, so two
// clones map to one repo slug while keeping distinct checkout slugs.
const originCloneURL = "https://example.com/acme/widget.git"

// newOriginClone builds a minimal origin-holding repo layout (a .git dir whose
// config names the shared origin), the same fixture shape internal/git's
// identity tests use. It returns the symlink-resolved path so assertions match
// what ResolveIdentity derives.
func newOriginClone(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	config := "[remote \"origin\"]\n\turl = " + originCloneURL + "\n"
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte(config), 0o644); err != nil {
		t.Fatalf("write git config: %v", err)
	}
	return root
}
