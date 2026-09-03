package vm

import (
	"context"
	"errors"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/opencode-sandbox/internal/agent"
	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/git"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	"github.com/inoio/opencode-sandbox/internal/sandbox/volume"
	"github.com/inoio/opencode-sandbox/internal/termio"
	"github.com/inoio/opencode-sandbox/internal/testutil"
)

// upgradeCheckerAgent implements both the core Agent interface and the
// UpgradeChecker capability so agentLatestVersion's real closure reaches its
// LatestVersion call without touching the network.
type upgradeCheckerAgent struct{}

func (upgradeCheckerAgent) Name() string          { return "upgradechecker" }
func (upgradeCheckerAgent) ConfigDirName() string { return "upgradechecker" }
func (upgradeCheckerAgent) ImageSpec() agent.ImageSpec {
	return agent.ImageSpec{}
}
func (upgradeCheckerAgent) LatestVersion(context.Context) (string, error) { return "9.9.9", nil }
func (upgradeCheckerAgent) NewerThan(_, _ string) (bool, error)           { return true, nil }

// TestAgentLatestVersionCheckerAgent covers the real agentLatestVersion closure
// branch where the agent is an UpgradeChecker: it returns the checker's latest
// version.
func TestAgentLatestVersionCheckerAgent(t *testing.T) {
	got, err := agentLatestVersion(context.Background(), upgradeCheckerAgent{})
	if err != nil {
		t.Fatalf("agentLatestVersion: %v", err)
	}
	if got != "9.9.9" {
		t.Errorf("agentLatestVersion = %q, want %q", got, "9.9.9")
	}
}

// configErrorHandle is a MockSandboxHandle whose Config call fails, exercising
// the "reading existing VM config failed (continuing)" branch of decideReconfig.
type configErrorHandle struct {
	*msb.MockSandboxHandle

	connect msb.Sandbox
}

func (h *configErrorHandle) Config() (*msbSdk.SandboxConfig, error) {
	return nil, errors.New("config read failed")
}

func (h *configErrorHandle) Connect(_ context.Context) (msb.Sandbox, error) {
	return h.connect, nil
}

// TestDecideReconfigConfigError covers the branch where reading the existing
// VM config fails: it is logged as verbose (not fatal) and the reconfig
// decision proceeds.
func TestDecideReconfigConfigError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	handle := &configErrorHandle{
		MockSandboxHandle: &msb.MockSandboxHandle{},
		connect:           &msb.MockSandbox{},
	}
	mock := &msb.MockMsbClient{}
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}
	msb.WithMsbMock(t, mock)

	ui := termio.NewTestMock(t)
	vm := volume.NewManager(&ui)

	persisted := state.HomeState{HomeVolume: "vol", ImageDigest: "sha256:same"}

	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	_, _, _, err = decideReconfig(
		context.Background(),
		mock,
		vm,
		state.Key{Slug: "testproj", Agent: "opencode"},
		options.RunOptions{},
		"img:tag",
		"sha256:same",
		"vol",
		persisted,
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("decideReconfig: %v", err)
	}
	if !contains(joinStrings(ui.VerboseCalls), "reading existing VM config failed") {
		t.Errorf("expected a verbose message about the failed config read, got %v", ui.VerboseCalls)
	}
}

// TestDecideReconfigResolveReconfigError covers the branch where ResolveReconfig
// reports an error: with another client attached, an image change prompts the
// user, and choosing "quit" aborts the reconfig with an error.
func TestDecideReconfigResolveReconfigError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)
	slug := git.ProjectSlug()

	release, err := state.AcquireClientLease(state.Key{Slug: slug, Agent: "opencode"})
	if err != nil {
		t.Fatalf("AcquireClientLease: %v", err)
	}
	defer release()

	mock := reconfigMockClient()
	msb.WithMsbMock(t, mock)

	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(string, []termio.Choice, string) (string, error) {
			return "q", nil
		},
	}
	vm := volume.NewManager(ui)

	persisted := state.HomeState{HomeVolume: "vol", ImageDigest: "sha256:old"}

	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	_, _, _, err = decideReconfig(
		context.Background(),
		mock,
		vm,
		state.Key{Slug: slug, Agent: "opencode"},
		options.RunOptions{},
		"img:new",
		"sha256:new",
		"vol",
		persisted,
		cfs,
		ui,
	)
	if err == nil {
		t.Fatal("expected an error when the user quits the rebuild prompt")
	}
}

// TestDecideReconfigApplyHomeActionError covers the branch where applying the
// chosen home-volume action fails and the reconfig aborts with that error.
func TestDecideReconfigApplyHomeActionError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := reconfigMockClient()
	mock.CreateVolumeFn = func(_ context.Context, _ string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return nil, errors.New("create volume failed")
	}
	msb.WithMsbMock(t, mock)

	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(prompt string, _ []termio.Choice, _ string) (string, error) {
			if contains(prompt, "Docker image changed") {
				return "m", nil // migrate -> triggers ApplyHomeAction -> CreateVolume
			}
			return "", nil
		},
	}
	vm := volume.NewManager(ui)

	persisted := state.HomeState{HomeVolume: "vol", ImageDigest: "sha256:old"}

	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	_, _, _, err = decideReconfig(
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
		t.Fatal("expected an error when applying the home-volume action fails")
	}
	if !contains(err.Error(), "apply home action") {
		t.Errorf("error = %v, want it to mention the home action", err)
	}
}
