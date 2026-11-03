package prepare

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

func TestCurrentAgentSource(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if got := currentAgentSource(k); got != "" {
		t.Errorf("currentAgentSource() = %q, want empty", got)
	}
	if err := seedHomeState(k, state.HomeState{AgentSource: state.AgentSourceUser}); err != nil {
		t.Fatal(err)
	}
	if got := currentAgentSource(k); got != state.AgentSourceUser {
		t.Errorf("currentAgentSource() = %q, want user", got)
	}
}
