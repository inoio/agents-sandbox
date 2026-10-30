package git

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/opener"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
)

// TestMain installs the fail-fast defaults directly: importing
// testutil/mocks would cycle, because configpaths imports this package, so the
// wrapper cannot be reached from here. Only docker is acyclic for git.
func TestMain(m *testing.M) {
	docker.InstallFailFastGet()
	opener.InstallFailFast()
	m.Run()
}
