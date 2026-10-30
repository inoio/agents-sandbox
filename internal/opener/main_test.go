package opener

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

func TestMain(m *testing.M) {
	// testutil.InitFailFastMocks installs this package's fail-fast seam, so
	// calling it here would be an import cycle. Keep in sync with its body.
	configpaths.InstallFailFastConfigPaths()
	docker.InstallFailFastGet()
	doctor.InstallFailFast()
	msb.InstallFailFastGet()
	notify.InstallFailFast()
	InstallFailFast()
	m.Run()
}
