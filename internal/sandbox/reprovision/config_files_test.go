package reprovision

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/homeconfig"
	"github.com/inoio/opencode-sandbox/internal/termio"
	"github.com/inoio/opencode-sandbox/internal/testutil"
)

func TestLoadConfigFilesPopulatesHooks(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()

	testutil.WritePath(t, filepath.Join(cp.ProjectConfigDir(), "connect.sh"), "#!/bin/sh\n")
	testutil.WriteFile(t, cp.ProjectConfigDir(), "home.yaml",
		".vpn/connect.sh:\n  source: connect.sh\n  hook: startup\n  user: root\n")

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFiles(configpaths.Get().UserOpencodeConfigDir(), &ui)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	want := []homeconfig.HookSpec{
		{Target: "/home/dev/.vpn/connect.sh", Source: filepath.Join(cp.ProjectConfigDir(), "connect.sh"), User: "root"},
	}
	if !reflect.DeepEqual(cf.Hooks, want) {
		t.Errorf("Hooks = %v, want %v", cf.Hooks, want)
	}
}
