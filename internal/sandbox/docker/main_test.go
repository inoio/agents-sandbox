package docker_test

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/opener"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

func TestMain(m *testing.M) {
	configpaths.InstallFailFastConfigPaths()
	dockertest.InstallFailFastGet()
	doctor.InstallFailFast()
	msb.InstallFailFastGet()
	notify.InstallFailFast()
	opener.InstallFailFast()
	m.Run()
}
