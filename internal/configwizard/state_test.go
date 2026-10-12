package configwizard

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

func TestStateRoundTripsAskedKeys(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	state := LoadState()
	if state.FirstRunDone || len(state.Asked) != 0 {
		t.Fatalf("fresh state = %+v, want zero value", state)
	}
	state.FirstRunDone = true
	state.MarkAsked("agent", "cpus", "agent")
	if err := SaveState(state); err != nil {
		t.Fatal(err)
	}
	reloaded := LoadState()
	if !reloaded.FirstRunDone {
		t.Error("FirstRunDone not persisted")
	}
	if !reloaded.WasAsked("cpus") || reloaded.WasAsked("nope") {
		t.Errorf("WasAsked wrong: %v", reloaded.Asked)
	}
	if len(reloaded.Asked) != 2 {
		t.Errorf("Asked = %v, want 2 unique keys", reloaded.Asked)
	}
}
