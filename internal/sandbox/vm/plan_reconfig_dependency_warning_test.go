package vm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestPlanReconfigStageWarnsAboutUnprovisionedConfigDirDependencies pins that
// plan-reconfig warns when the host opencode config dir declares dependencies
// the drop-in copy leaves behind (#288).
func TestPlanReconfigStageWarnsAboutUnprovisionedConfigDirDependencies(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	hostHome, err := configpaths.Get().UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	configDir := filepath.Join(hostHome, ".config", "opencode")
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		t.Fatalf("create %s: %v", configDir, err)
	}
	manifest := filepath.Join(configDir, "package.json")
	if err := os.WriteFile(manifest, []byte(`{"dependencies": {"zod": "4.1.8"}}`), 0o600); err != nil {
		t.Fatalf("write %s: %v", manifest, err)
	}
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	msb.WithMsbMock(t, client)
	session := sessionIdentity{key: testVMKey(), agent: opencodeAgent(t)}
	home := preparedHome{
		client:     client,
		volumes:    volume.NewManager(&ui),
		homeVolume: "vol",
		homeState:  state.HomeState{HomeVolume: "vol"},
	}

	if _, err := planReconfigStage(
		context.Background(), session, image.ImageInfo{}, home, false, options.RunOptions{Agent: "opencode"}, &ui,
	); err != nil {
		t.Fatalf("planReconfigStage: %v", err)
	}

	warned := false
	for _, warning := range ui.WarnCalls {
		if strings.Contains(warning, manifest) {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no warning mentions %s; got: %v", manifest, ui.WarnCalls)
	}
}
