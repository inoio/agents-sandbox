package filewalk

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/opener"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
)

// TestMain installs the docker fail-fast. filewalk cannot import
// internal/testutil/mocks or any seam reached through configpaths (that would
// cycle), leaving docker as the only family its tests may import; the isolation
// contract test requires every acyclically importable family to be installed.
func TestMain(m *testing.M) {
	docker.InstallFailFastGet()
	opener.InstallFailFast()
	m.Run()
}
