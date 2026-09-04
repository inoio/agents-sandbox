package vm

import (
	"context"
	"errors"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestDecideReconfigHomeQuit covers the user-quit branch of decideReconfig:
// when the home-volume prompt is answered with "quit", the session is aborted
// with an ExitError.
func TestDecideReconfigHomeQuit(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	sh := &msb.MockSandboxHandle{
		Cfg: &msbSdk.SandboxConfig{Image: "img:tag", CPUs: 4, MemoryMiB: 4096},
	}
	sh.ConnectSb = &msb.MockSandbox{}
	mock := &msb.MockMsbClient{}
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return sh, nil
	}
	msb.WithMsbMock(t, mock)

	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(prompt string, _ []termio.Choice, _ string) (string, error) {
			if contains(prompt, "Docker image changed") {
				return "q", nil
			}
			return "", nil
		},
	}
	vm := volume.NewManager(ui)

	persisted := state.HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:old",
	}

	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	_, _, _, _, err = decideReconfig(
		context.Background(),
		mock,
		vm,
		state.Key{Slug: "testproj", Agent: "opencode"},
		options.RunOptions{},
		"img:new",
		"sha256:new",
		"vol",
		persisted,
		cfs,
		ui,
	)
	if err == nil {
		t.Fatal("expected an ExitError when the user quits the home-volume prompt")
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != 1 {
		t.Errorf("expected exit code 1, got %d", exitErr.Code)
	}
}
