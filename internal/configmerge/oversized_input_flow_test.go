package configmerge

import (
	"errors"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configread"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestBuildMergedSkipsOversizedSnippet pins that an agent snippet larger than the
// cap is skipped rather than parsed, so an agent-writable project snippet cannot
// drive unbounded allocation.
func TestBuildMergedSkipsOversizedSnippet(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "opencode-big.json", `{"a":"`+strings.Repeat("x", configread.MaxBytes)+`"}`)

	built, err := BuildMerged("", "opencode*.json*", []string{dir})
	if err != nil {
		t.Fatalf("BuildMerged: %v", err)
	}
	if built.HasSnippets {
		t.Errorf("HasSnippets = true, want the oversized snippet skipped")
	}
	if len(built.Skipped) != 1 || !errors.Is(built.Skipped[0].Err, configread.ErrTooLarge) {
		t.Errorf("Skipped = %+v, want one entry wrapping configread.ErrTooLarge", built.Skipped)
	}
}
