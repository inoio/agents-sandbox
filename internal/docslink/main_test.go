package docslink

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/opener"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

func TestMain(m *testing.M) {
	// can't call testutil.InitFailFastMocks(m) because of cyclic dependency: it installs doctor, which imports this
	// package. Keep in sync with testutil.InitFailFastMocks' body.
	configpaths.InstallFailFastConfigPaths()
	docker.InstallFailFastGet()
	msb.InstallFailFastGet()
	notify.InstallFailFast()
	opener.InstallFailFast()
	m.Run()
}
