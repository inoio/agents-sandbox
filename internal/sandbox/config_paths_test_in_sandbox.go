package sandbox

import (
	"path/filepath"
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/state"
)

func TestStateFileAbsoluteUnderUserStateDir(t *testing.T) {
	configpaths.WithRealConfigPaths(t)
	cfgState := t.TempDir()
	t.Setenv("XDG_STATE_HOME", cfgState)
	t.Setenv("HOME", t.TempDir())
	got := state.StateFile("proj")
	want := filepath.Join(cfgState, "opencode-msb", "proj", "state.yaml")
	if got != want {
		t.Errorf("StateFile() = %q, want %q", got, want)
	}
}
