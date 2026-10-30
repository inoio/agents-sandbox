package vm

import (
	"context"
	"errors"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// upgradeCheckerAgent implements both the core Agent interface and the
// UpgradeChecker capability so the shared release resolver reaches its
// LatestVersion call without touching the network.
type upgradeCheckerAgent struct{}

func (upgradeCheckerAgent) Name() string          { return "upgradechecker" }
func (upgradeCheckerAgent) ConfigDirName() string { return "upgradechecker" }
func (upgradeCheckerAgent) ImageSpec() agent.ImageSpec {
	return agent.ImageSpec{}
}
func (upgradeCheckerAgent) LatestVersion(context.Context) (string, error) { return "9.9.9", nil }
func (upgradeCheckerAgent) NewerThan(_, _ string) (bool, error)           { return true, nil }

// TestAgentLatestVersionCheckerAgent covers the real agentLatestVersion branch
// where the agent is an UpgradeChecker: it returns the checker's latest
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

// TestResolveReconfigDecisionConfigError covers the branch where reading the existing
// VM config fails: the failure is surfaced as a warning and the decision is
// forced to recreate, so the run cannot silently reuse a VM whose image, env,
// or network may differ from the desired config (#339).
func TestResolveReconfigDecisionConfigError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	handle := &msb.MockSandboxHandle{
		ConfigErr: errors.New("config read failed"),
		ConnectSb: &msb.MockSandbox{},
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
	decision, err := resolveReconfigDecision(
		context.Background(),
		mock,
		vm,
		state.Key{Slug: "testproj", Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:same"},
		"vol",
		persisted,
		cfs,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if !decision.Recreate {
		t.Error("expected a forced recreate when the existing VM's config cannot be read")
	}
	if !contains(joinStrings(ui.WarnCalls), "config read failed") {
		t.Errorf("expected a warning naming the config read failure, got %v", ui.WarnCalls)
	}
	if !contains(joinStrings(ui.WarnCalls), "testproj-opencode") {
		t.Errorf("expected the warning to name the VM, got %v", ui.WarnCalls)
	}
}

// TestResolveReconfigDecisionConfigErrorPromptsAttachedClients covers the attached-client
// branch of the forced recreate: with another client attached, the unreadable
// config is offered through the rebuild prompt, and choosing "keep" defers it.
func TestResolveReconfigDecisionConfigErrorPromptsAttachedClients(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)
	slug := git.MustCheckoutSlug()

	release, err := state.AcquireClientLease(state.Key{Slug: slug, Agent: "opencode"})
	if err != nil {
		t.Fatalf("AcquireClientLease: %v", err)
	}
	defer release()

	handle := &msb.MockSandboxHandle{
		ConfigErr: errors.New("config read failed"),
		ConnectSb: &msb.MockSandbox{},
	}
	mock := &msb.MockMsbClient{}
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}
	msb.WithMsbMock(t, mock)

	var prompt string
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(p string, _ []termio.Choice, _ string) (string, error) {
			prompt = p
			return "k", nil
		},
	}
	vm := volume.NewManager(ui)

	persisted := state.HomeState{HomeVolume: "vol", ImageDigest: "sha256:same"}

	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	decision, err := resolveReconfigDecision(
		context.Background(),
		mock,
		vm,
		state.Key{Slug: slug, Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:same"},
		"vol",
		persisted,
		cfs,
		envsecret.EnvSecrets{},
		ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if decision.Recreate {
		t.Error("expected the forced recreate to be deferred when the user keeps the current VM")
	}
	if !contains(prompt, "unreadable VM config") {
		t.Errorf("rebuild prompt %q does not name the unreadable VM config", prompt)
	}
}

// TestResolveReconfigDecisionFindProjectVMError covers the fail-closed branch where the
// VM lookup itself fails: resolveReconfigDecision returns the error instead of treating
// the failure as "no VM" and silently skipping reconfiguration (#339).
func TestResolveReconfigDecisionFindProjectVMError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := &msb.MockMsbClient{}
	mock.SetGetSandboxErr(errors.New("msb unreachable"))
	msb.WithMsbMock(t, mock)

	ui := termio.NewTestMock(t)
	vm := volume.NewManager(&ui)

	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	_, err = resolveReconfigDecision(
		context.Background(),
		mock,
		vm,
		state.Key{Slug: "testproj", Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:same"},
		"vol",
		state.HomeState{HomeVolume: "vol", ImageDigest: "sha256:same"},
		cfs,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err == nil {
		t.Fatal("expected an error when the VM lookup fails")
	}
	if !contains(err.Error(), "locate project VM") {
		t.Errorf("error = %v, want it to mention locating the project VM", err)
	}
}

// TestResolveReconfigDecisionNoVMSkipsRecreate guards against over-triggering: with no VM
// present, a changed setting must not force a recreate (the VM is created fresh
// with the desired config anyway).
func TestResolveReconfigDecisionNoVMSkipsRecreate(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := &msb.MockMsbClient{}
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "no VM"}
	}
	msb.WithMsbMock(t, mock)

	ui := termio.NewTestMock(t)
	vm := volume.NewManager(&ui)

	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	decision, err := resolveReconfigDecision(
		context.Background(),
		mock,
		vm,
		state.Key{Slug: "testproj", Agent: "opencode"},
		options.RunOptions{
			Agent:   "opencode",
			Network: network.Policy{Profiles: []network.Profile{network.ProfileNone}},
		},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:new"},
		"vol",
		state.HomeState{HomeVolume: "vol", ImageDigest: "sha256:same"},
		cfs,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if decision.Recreate {
		t.Error("expected no recreate when no VM exists, even with changed settings")
	}
}

// TestResolveReconfigDecisionQuitAtRecreatePrompt covers the branch where ResolveReconfig
// reports an error: with another client attached, an image change prompts the
// user, and choosing "quit" aborts the reconfig with an error.
func TestResolveReconfigDecisionQuitAtRecreatePrompt(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)
	slug := git.MustCheckoutSlug()

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
	_, err = resolveReconfigDecision(
		context.Background(),
		mock,
		vm,
		state.Key{Slug: slug, Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:new", Ref: "img:new", Digest: "sha256:new"},
		"vol",
		persisted,
		cfs,
		envsecret.EnvSecrets{},
		ui,
	)
	if err == nil {
		t.Fatal("expected an error when the user quits the rebuild prompt")
	}
}

// TestApplyReconfigHomeActionError covers the branch where applying the
// chosen home-volume action fails and the reconfig aborts with that error.
func TestApplyReconfigHomeActionError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := reconfigMockClient()
	createErr := errors.New("create volume failed")
	mock.CreateVolumeFn = func(_ context.Context, _ string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return nil, createErr
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
	decision, err := resolveReconfigDecision(
		context.Background(),
		mock,
		vm,
		state.Key{Slug: "testproj", Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:new", Ref: "img:new", Digest: "sha256:new"},
		"vol",
		persisted,
		cfs,
		envsecret.EnvSecrets{},
		ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	_, err = applyReconfigHomeAction(
		context.Background(),
		mock,
		vm,
		state.Key{Slug: "testproj", Agent: "opencode"},
		"vol",
		image.ImageInfo{DockerTag: "img:new", Ref: "img:new", Digest: "sha256:new"},
		false,
		decision,
		ui,
	)
	if err == nil {
		t.Fatal("expected an error when applying the home-volume action fails")
	}
	if !contains(err.Error(), "apply home action") {
		t.Errorf("error = %v, want it to mention the home action", err)
	}
	if !errors.Is(err, createErr) {
		t.Errorf("error = %v, want it to wrap the create-volume failure", err)
	}
}
