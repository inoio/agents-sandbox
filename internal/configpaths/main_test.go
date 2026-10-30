package configpaths

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/opener"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
)

func TestMain(m *testing.M) {
	// can't call testutil.InitFailFastMocks(m) because of cyclic dependency. Keep in sync with
	// testutil.InitFailFastMocks' body, except doctor.InstallFailFast() which is intentionally
	// omitted (configpaths never invokes doctor, and importing it now creates a
	// configpaths -> doctor -> runtime -> configpaths test cycle) and msb.InstallFailFastGet()
	// which is omitted because msb now imports configpaths (for configpaths.MSBHome), so
	// importing msb here would close a configpaths -> msb -> configpaths test cycle.
	// configpaths never invokes msb, so the fail-fast mock is not needed.
	InstallFailFastConfigPaths()
	docker.InstallFailFastGet()
	opener.InstallFailFast()
	m.Run()
}
