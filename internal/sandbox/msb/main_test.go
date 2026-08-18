package msb

import (
	"testing"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
)

func TestMain(m *testing.M) {
	// can't call testutil.InitFailFastMocks(m) because of cyclic dependency. Keep in sync with
	// testutil.InitFailFastMocks' body
	configpaths.InstallFailFastConfigPaths()
	docker.InstallFailFastGet()
	// cyclic dependency on msb
	// doctor.InstallFailFast()
	InstallFailFastGet()
	m.Run()
}
