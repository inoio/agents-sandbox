package vm

import (
	"context"
	"errors"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// reconfigFixture wires the mocks resolveReconfigDecision reads its live VM from.
type reconfigFixture struct {
	client  *msb.MockMsbClient
	vm      *volume.Manager
	ui      *termio.Mock
	cfs     *reprovision.ConfigFiles
	homeVol string
}

// newReconfigFixture builds a project VM whose persisted config carries the
// given mounts. The mounts are seeded through the seam (ConfigMounts) rather
// than Cfg.Volumes, because that is how a live VM's mounts reach the decision.
func newReconfigFixture(t *testing.T, liveMounts map[string]msbSdk.MountConfig, mountsErr error) reconfigFixture {
	t.Helper()
	return newReconfigFixtureForImage(t, "img:tag", liveMounts, mountsErr)
}

// newReconfigFixtureForImage is newReconfigFixture with an explicit persisted
// runner-image reference, so a test can drive the image-identity comparison.
func newReconfigFixtureForImage(
	t *testing.T,
	cfgImage string,
	liveMounts map[string]msbSdk.MountConfig,
	mountsErr error,
) reconfigFixture {
	t.Helper()

	handle := &msb.MockSandboxHandle{
		Cfg:       &msbSdk.SandboxConfig{Image: cfgImage, CPUs: 4, MemoryMiB: 4096},
		Mounts:    liveMounts,
		MountsErr: mountsErr,
	}
	handle.ConnectSb = &msb.MockSandbox{}
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}
	msb.WithMsbMock(t, client)

	ui := termio.NewTestMock(t)
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	return reconfigFixture{
		client:  client,
		vm:      volume.NewManager(&ui),
		ui:      &ui,
		cfs:     cfs,
		homeVol: "vol",
	}
}

func (f reconfigFixture) decide(t *testing.T, opts options.RunOptions, hs state.HomeState) (bool, error) {
	t.Helper()
	decision, err := resolveReconfigDecision(
		context.Background(),
		f.client,
		f.vm,
		state.Key{Slug: "testproj", Agent: "opencode"},
		opts,
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: hs.ImageDigest},
		f.homeVol,
		hs,
		f.cfs,
		envsecret.EnvSecrets{},
		f.ui,
	)
	return decision.Recreate, err
}

// launcherLiveMounts is the mount set buildMounts creates: a tmpfs /tmp, a
// quota-bound /workspace bind, and the named home volume.
func launcherLiveMounts(tmpSizeMiB, workspaceQuotaMiB uint32, homeVolume string) map[string]msbSdk.MountConfig {
	return map[string]msbSdk.MountConfig{
		mounts.TmpMountPath:       msbSdk.Mount.Tmpfs(msbSdk.TmpfsOptions{SizeMiB: tmpSizeMiB}),
		mounts.WorkspaceMountPath: msbSdk.Mount.Bind("/host/repo", msbSdk.MountOptions{QuotaMiB: workspaceQuotaMiB}),
		mounts.VMHomeDir:          msbSdk.Mount.Named(homeVolume, msbSdk.MountOptions{}),
	}
}

// matchingHomeState is a persisted state that agrees with the live VM, so only
// the mounts can drive the decision. NetworkState must be seeded explicitly:
// an empty one always reads as "policy changed" and would recreate on its own.
func matchingHomeState(networkPolicy network.Policy) state.HomeState {
	return state.HomeState{
		HomeVolume:   "vol",
		ImageDigest:  "sha256:new",
		NetworkState: state.BuildNetworkState(networkPolicy),
	}
}

// assertRebuildReason fails unless the alone-rebuild log names want, proving the
// expected mount detector drove the recreate rather than some unrelated change.
func assertRebuildReason(t *testing.T, ui *termio.Mock, want string) {
	t.Helper()
	for _, line := range ui.VerboseCalls {
		if strings.Contains(line, "rebuild reasons:") && strings.Contains(line, want) {
			return
		}
	}
	t.Errorf("rebuild reasons %v do not name %q", ui.VerboseCalls, want)
}

// assertVerbose fails unless one verbose line contains want.
func assertVerbose(t *testing.T, ui *termio.Mock, want string) {
	t.Helper()
	for _, line := range ui.VerboseCalls {
		if strings.Contains(line, want) {
			return
		}
	}
	t.Errorf("verbose calls %v do not contain %q", ui.VerboseCalls, want)
}

func TestResolveReconfigDecisionDetectsTmpSizeChangeFromLiveMounts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	opts := options.RunOptions{TmpSize: "4G"}
	f := newReconfigFixture(t, launcherLiveMounts(1024, 16384, "vol"), nil)

	recreate, err := f.decide(t, opts, matchingHomeState(opts.Network))
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if !recreate {
		t.Error("expected a recreate for a tmp size change read from the live VM's mounts")
	}
	assertRebuildReason(t, f.ui, "/tmp tmpfs size")
}

func TestResolveReconfigDecisionNoRecreateWhenLiveMountsMatch(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	opts := options.RunOptions{TmpSize: "2G", WorkspaceQuota: "16G"}
	f := newReconfigFixture(t, launcherLiveMounts(2048, 16384, "vol"), nil)

	recreate, err := f.decide(t, opts, matchingHomeState(opts.Network))
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if recreate {
		t.Error("expected no recreate when the live mounts already match the options")
	}
	for _, line := range f.ui.VerboseCalls {
		if strings.Contains(line, "rebuild reasons:") {
			t.Errorf("expected no rebuild reason, got %q", line)
		}
	}
}

func TestResolveReconfigDecisionRecreatesOnWorkspaceQuotaChange(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	opts := options.RunOptions{TmpSize: "2G", WorkspaceQuota: "32G"}
	f := newReconfigFixture(t, launcherLiveMounts(2048, 8192, "vol"), nil)

	recreate, err := f.decide(t, opts, matchingHomeState(opts.Network))
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if !recreate {
		t.Error("expected a recreate for a workspace quota change read from the live VM's mounts")
	}
	assertRebuildReason(t, f.ui, "/workspace write quota")
}

// TestResolveReconfigDecisionRecreatesOnHomeVolumeChangeFromLiveMounts covers the third
// detector the read-back restores: a live VM still mounting the previous home
// volume must recreate when home resolution points at a different one.
func TestResolveReconfigDecisionRecreatesOnHomeVolumeChangeFromLiveMounts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	opts := options.RunOptions{TmpSize: "2G", WorkspaceQuota: "16G"}
	f := newReconfigFixture(t, launcherLiveMounts(2048, 16384, "old-vol"), nil)

	recreate, err := f.decide(t, opts, matchingHomeState(opts.Network))
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if !recreate {
		t.Error("expected a recreate when the live home volume differs from the resolved one")
	}
	assertRebuildReason(t, f.ui, "home volume")
}

func TestResolveReconfigDecisionMountsReadFailureDegrades(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	opts := options.RunOptions{TmpSize: "2G", WorkspaceQuota: "16G"}
	f := newReconfigFixture(t, nil, errors.New("mounts boom"))

	recreate, err := f.decide(t, opts, matchingHomeState(opts.Network))
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v, want a degraded decision instead of aborting the startup", err)
	}
	if recreate {
		t.Error("expected no recreate when the mounts cannot be read")
	}
	assertVerbose(t, f.ui, "reading VM mounts failed")
}

// TestResolveReconfigDecisionNoHomeActionOnUnchangedDigest pins that the
// home-volume action is resolved only when the image digest changed: a rebuild
// driven by a non-image change must leave HomeAction nil, so no home-volume
// prompt or state write happens for it.
func TestResolveReconfigDecisionNoHomeActionOnUnchangedDigest(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	opts := options.RunOptions{TmpSize: "4G"} // the tmp-size change forces a recreate
	f := newReconfigFixture(t, launcherLiveMounts(1024, 16384, "vol"), nil)
	hs := matchingHomeState(opts.Network) // the seeded digest matches the run's image

	decision, err := resolveReconfigDecision(
		context.Background(),
		f.client,
		f.vm,
		state.Key{Slug: "testproj", Agent: "opencode"},
		opts,
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: hs.ImageDigest},
		f.homeVol,
		hs,
		f.cfs,
		envsecret.EnvSecrets{},
		f.ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if !decision.Recreate {
		t.Fatal("expected a recreate for the tmp-size change")
	}
	if decision.HomeAction != nil {
		t.Error("expected no home-volume action when the image digest is unchanged")
	}
}
