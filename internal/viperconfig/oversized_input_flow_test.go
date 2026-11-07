package viperconfig

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/inoio/agents-sandbox/internal/configread"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestMergeDirRejectsOversizedConfig pins that the launcher config read is bound
// at the shared cap: a project config larger than configread.MaxBytes is refused
// instead of being handed to the parser.
func TestMergeDirRejectsOversizedConfig(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "config.yaml", "memory: 4G\n# "+strings.Repeat("x", configread.MaxBytes)+"\n")

	err := mergeDir(viper.New(), dir)
	if !errors.Is(err, configread.ErrTooLarge) {
		t.Fatalf("mergeDir err = %v, want ErrTooLarge", err)
	}
}
