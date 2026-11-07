package homeconfig

import (
	"errors"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configread"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestReadHomeFromConfigDirRejectsOversizedConfig pins that the home manifest
// read is bound at the shared cap, so a hostile project config cannot drive
// unbounded allocation here.
func TestReadHomeFromConfigDirRejectsOversizedConfig(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(
		t,
		dir,
		"config.yaml",
		"home:\n  ~/.gitconfig: /host/gitconfig\n# "+strings.Repeat("x", configread.MaxBytes)+"\n",
	)

	_, _, err := ReadHomeFromConfigDir(dir)
	if !errors.Is(err, configread.ErrTooLarge) {
		t.Fatalf("ReadHomeFromConfigDir err = %v, want ErrTooLarge", err)
	}
}
