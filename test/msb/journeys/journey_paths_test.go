package journeys

import (
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

// TestWithJourneyStatePathsRestoresTheFactory pins the redirect's teardown: after
// the returned restore runs, the config-path factory must be the one the step
// replaced — the fail-fast test default, which panics on any call — not the
// journey resolver. A removed restore leaves the journey resolver installed and
// the next test writing the journey's isolated state.
func TestWithJourneyStatePathsRestoresTheFactory(t *testing.T) {
	home := t.TempDir()
	restore := withJourneyStatePaths(home)
	want := filepath.Join(home, isolatedStateDir, launcherConfigDirName)
	if got := configpaths.Get().UserStateDir(); got != want {
		t.Errorf("redirected UserStateDir() = %q, want %q", got, want)
	}

	restore()
	defer func() {
		if recover() == nil {
			t.Error("the config-path factory was not restored: the fail-fast default must panic after restore")
		}
	}()
	_ = configpaths.Get().UserStateDir()
}
