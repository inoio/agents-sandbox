package runtime

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/opener"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	sandboxmsb "github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// TestMain installs the fail-fast defaults for the seam factories so no runtime
// test can silently touch real config paths, Docker, msb, or notifications. It
// cannot call testutil.InitFailFastMocks: doctor imports runtime, so that would
// close a test import cycle. Doctor is not reachable from runtime, so its
// fail-fast is not installed here.
func TestMain(m *testing.M) {
	configpaths.InstallFailFastConfigPaths()
	docker.InstallFailFastGet()
	sandboxmsb.InstallFailFastGet()
	notify.InstallFailFast()
	opener.InstallFailFast()
	m.Run()
}
