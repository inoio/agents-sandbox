package vm

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestSetUpSandboxReturnsGuestCWD(t *testing.T) {
	seams := healthyOpencodeDaemon(t)

	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{Name_: "test-vm", FSValue_: fs}
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	snippet := filepath.Join(
		configpaths.CreateDir(filepath.Join(cp.UserConfigDir(), opencodeAgent(t).ConfigDirName())),
		"opencode-x.json5",
	)
	testutil.WritePath(t, snippet, `{"model":"x"}`)

	ui := &termio.Mock{}
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeResolved(t), t.TempDir(), reprovision.VMHomeDir, ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	target, err := SetUpSandbox(
		context.Background(),
		testVMKey(),
		sb,
		opencodeResolved(t),
		options.RunOptions{Agent: "opencode"},
		seams,
		cfs,
		ui,
		false,
		VMBootStarted,
		"/workspace/sub",
	)
	if err != nil {
		t.Fatalf("SetUpSandbox: %v", err)
	}
	if target != "/workspace/sub" {
		t.Errorf("target = %q, want %q", target, "/workspace/sub")
	}
}
