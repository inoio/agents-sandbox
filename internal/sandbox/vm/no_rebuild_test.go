package vm

import (
	"context"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestResolveBuildVersionNoRebuildSkipsUpgradeCheck pins that --no-rebuild never
// runs the update check: accepting an upgrade would rebuild the image the user
// asked to keep. The version stays the one the existing image reports.
func TestResolveBuildVersionNoRebuildSkipsUpgradeCheck(t *testing.T) {
	origLatest := agentLatestVersion
	t.Cleanup(func() { agentLatestVersion = origLatest })
	agentLatestVersion = func(context.Context, agent.Agent) (string, error) {
		t.Fatal("--no-rebuild must not run the update check")
		return "", nil
	}
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool)
	withExistingAgentVersion(t)

	got, upgraded, offered, err := resolveBuildVersion(
		context.Background(), k, opencodeAgent(t), &termio.Mock{},
		options.RunOptions{Agent: "opencode", NoRebuild: true},
	)
	if err != nil {
		t.Fatalf("resolveBuildVersion: %v", err)
	}
	if got != "1.0.0" || upgraded || offered != "" {
		t.Errorf("resolveBuildVersion = (%q, %v, %q), want (\"1.0.0\", false, \"\")", got, upgraded, offered)
	}
}

func TestBuildOptionsCarriesNoRebuild(t *testing.T) {
	got := buildOptions(options.RunOptions{NoRebuild: true}, "1.0.0", false, false)
	if !got.NoRebuild {
		t.Error("buildOptions dropped NoRebuild")
	}
}
