package termio

import (
	"testing"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
)

func TestMain(m *testing.M) {
	// can't call testutil.InitFailFastMocks(m) because of cyclic dependency. Keep in sync with
	// testutil.InitFailFastMocks' body
	configpaths.InstallFailFastConfigPaths()
	docker.InstallFailFastGet()
	// cyclic dependency on termio
	// doctor.InstallFailFast()
	msb.InstallFailFastGet()
	m.Run()
}
