package doctor

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// TestMain installs the fail-fast defaults for the doctor factories so no
// doctor test can silently touch real msb.
func TestMain(m *testing.M) {
	configpaths.InstallFailFastConfigPaths()
	InstallFailFast()
	msb.InstallFailFastGet()
	m.Run()
}
