package doctor

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/opener"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// TestMain installs the fail-fast defaults for the doctor factories so no
// doctor test can silently touch real msb or Docker.
func TestMain(m *testing.M) {
	configpaths.InstallFailFastConfigPaths()
	docker.InstallFailFastGet()
	InstallFailFast()
	msb.InstallFailFastGet()
	notify.InstallFailFast()
	opener.InstallFailFast()
	m.Run()
}
