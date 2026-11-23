package firstrun

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStatResolved(t *testing.T) {
	file := filepath.Join(t.TempDir(), "CLAUDE.md")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if path, info, got := statResolved(file); got != pathResolved || path != file || info == nil {
		t.Errorf("statResolved(existing) = %q, %v, %v; want the resolved file", path, info, got)
	}
	// A path removed after EvalSymlinks resolved it counts as missing.
	if path, info, got := statResolved(
		filepath.Join(t.TempDir(), "gone.md"),
	); got != pathMissing || path != "" ||
		info != nil {
		t.Errorf("statResolved(removed) = %q, %v, %v; want missing", path, info, got)
	}
}
