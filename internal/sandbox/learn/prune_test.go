package learn

import (
	"os"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/channel"
)

// TestPruneArtefactsRemovesOldKeepsFresh pins the 30-day sweep: an artefact
// older than LearnArtefactAge is removed and a fresh one is kept.
func TestPruneArtefactsRemovesOldKeepsFresh(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	oldPath, err := channel.Save("slug", 1763000000, channel.Observation{})
	if err != nil {
		t.Fatalf("save old artefact: %v", err)
	}
	freshPath, err := channel.Save("slug", 1763458200, channel.Observation{})
	if err != nil {
		t.Fatalf("save fresh artefact: %v", err)
	}
	past := time.Now().Add(-45 * 24 * time.Hour)
	if err := os.Chtimes(oldPath, past, past); err != nil {
		t.Fatalf("age the old artefact: %v", err)
	}

	removed, err := PruneArtefacts(time.Now())
	if err != nil {
		t.Fatalf("PruneArtefacts: %v", err)
	}
	if removed != 1 {
		t.Errorf("PruneArtefacts removed %d, want 1", removed)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Errorf("old artefact still present after PruneArtefacts (err = %v)", err)
	}
	if _, err := os.Stat(freshPath); err != nil {
		t.Errorf("fresh artefact removed by PruneArtefacts: %v", err)
	}
}
