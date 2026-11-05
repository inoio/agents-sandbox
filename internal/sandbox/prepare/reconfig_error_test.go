package prepare

import (
	"context"
	"errors"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestResolveReconfigDecisionDetachErrorVerbose covers the failed-detach verbose branch
// in resolveReconfigDecision: a connected sandbox whose Detach fails is logged (not
// fatal).
func TestResolveReconfigDecisionDetachErrorVerbose(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	sh := &msb.MockSandboxHandle{
		Cfg: &msbSdk.SandboxConfig{Image: "img:tag", CPUs: 4, MemoryMiB: 4096},
	}
	liveSb := &msb.MockSandbox{DetachErr: errors.New("detach failed")}
	sh.ConnectSb = liveSb
	mock := &msb.MockMsbClient{}
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return sh, nil
	}
	msb.WithMsbMock(t, mock)

	volumes := volume.NewManager(docker.Get())
	persisted := state.HomeState{HomeVolume: "vol", ImageDigest: "sha256:same"}

	ui := termio.NewTestMock(t)
	cfs, err := reprovision.LoadConfigFilesForHost(
		opencodeResolved(t),
		"",
		t.TempDir(),
		reprovision.VMHomeDir,
		&ui,
		true,
	)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	_, err = readAndResolveReconfigDecision(
		context.Background(),
		mock,
		volumes,
		state.Key{Slug: "testproj", Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:same"},
		"vol",
		persisted,
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if !contains(joinStrings(ui.VerboseCalls), "failed to detach") {
		t.Errorf("expected a verbose message about the failed detach, got %v", ui.VerboseCalls)
	}
}
