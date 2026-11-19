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

// TestPruneArtefactsHonoursTheThirtyDayBoundary pins LearnArtefactAge at exactly
// 30 days: an artefact 29 days old survives and one 31 days old is removed.
func TestPruneArtefactsHonoursTheThirtyDayBoundary(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	insidePath, err := channel.Save("boundary", 1, channel.Observation{})
	if err != nil {
		t.Fatalf("save inside artefact: %v", err)
	}
	outsidePath, err := channel.Save("boundary", 2, channel.Observation{})
	if err != nil {
		t.Fatalf("save outside artefact: %v", err)
	}

	now := time.Now()
	insideAge := now.Add(-29 * 24 * time.Hour)
	outsideAge := now.Add(-31 * 24 * time.Hour)
	if err := os.Chtimes(insidePath, insideAge, insideAge); err != nil {
		t.Fatalf("age the inside artefact: %v", err)
	}
	if err := os.Chtimes(outsidePath, outsideAge, outsideAge); err != nil {
		t.Fatalf("age the outside artefact: %v", err)
	}

	removed, err := PruneArtefacts(now)
	if err != nil {
		t.Fatalf("PruneArtefacts: %v", err)
	}
	if removed != 1 {
		t.Errorf("PruneArtefacts removed %d, want 1 (only the 31-day-old artefact)", removed)
	}
	if _, err := os.Stat(insidePath); err != nil {
		t.Errorf("the 29-day-old artefact was removed: %v", err)
	}
	if _, err := os.Stat(outsidePath); !os.IsNotExist(err) {
		t.Errorf("the 31-day-old artefact survived PruneArtefacts (err = %v)", err)
	}
}
