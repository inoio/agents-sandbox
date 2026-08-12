package sandbox

import (
	"os"
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
	want := filepath.Join(cfgState, "opencode-msb", "proj", "state.yaml")
	slugDir := filepath.Join(state.StateDir, "proj")
	if err := os.MkdirAll(slugDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(slugDir, "state.yaml"),
		[]byte("home_volume: test\nimage_digest: sha256:abc\n"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got := filepath.Join(state.StateDir, "proj", "state.yaml")
	if got != want {
		t.Errorf("StateDir + slug path = %q, want %q", got, want)
	}
}
