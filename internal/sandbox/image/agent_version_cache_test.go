package image

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// cachedAgentVersion returns the newest release the launcher's real upgrade
// cache holds for the agent, read from the machine-global
// RootState.updates.agents map. The integration image tests read it before
// reaching for a live GitHub release, so a machine that has used the agent
// bakes a recent real release with no network dependency and no hard-coded
// version that goes stale. The boolean reports whether the cache held a
// release; a missing or unreadable root state reports false.
func cachedAgentVersion(a agent.Agent) (version string, cached bool) {
	root, err := state.ReadRootState()
	if err != nil {
		return "", false
	}
	version = root.AgentCheck(a.Name()).LatestVersion
	return version, version != ""
}

func TestCachedAgentVersionReportsTheLauncherCachedRelease(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}

	if version, cached := cachedAgentVersion(a); cached {
		t.Fatalf("cachedAgentVersion() = %q, true before any state is written; want no cached release", version)
	}

	if _, err := state.WithRootState(true, func(tx *state.RootTxn) error {
		tx.State().SetAgentCheck(a.Name(), state.AgentCheckState{LatestVersion: "2.9.7"})
		return tx.Commit()
	}); err != nil {
		t.Fatalf("seed root state: %v", err)
	}

	version, cached := cachedAgentVersion(a)
	if !cached || version != "2.9.7" {
		t.Fatalf("cachedAgentVersion() = %q, %v; want 2.9.7, true", version, cached)
	}
}
