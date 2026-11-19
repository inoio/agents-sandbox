package state

import (
	"errors"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
)

// TestOverrideStateWriteForTestInstallsAndRestores pins the seam's contract: it
// replaces the root-state writer so a caller can force a write failure, and the
// returned function restores the previous writer.
func TestOverrideStateWriteForTestInstallsAndRestores(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	sentinel := errors.New("override boom")
	restore := OverrideStateWriteForTest(func(string, []byte) error { return sentinel })

	if err := RecordLearnDecline("widget-abc", "h1"); !errors.Is(err, sentinel) {
		t.Fatalf("RecordLearnDecline under the override = %v, want %v", err, sentinel)
	}

	restore()

	if err := RecordLearnDecline("widget-abc", "h1"); err != nil {
		t.Fatalf("RecordLearnDecline after restore = %v, want nil", err)
	}
}
