package vm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	sandboximage "github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestPrepareHomeVolumeAlwaysOffersRuntimeHomeMigration pins that the hub offers
// the legacy home copy on every prepare path (the --dry-run gate is gone; a
// preview never reaches the hub).
func TestPrepareHomeVolumeAlwaysOffersRuntimeHomeMigration(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	msb.WithMsbMock(t, &msb.MockMsbClient{
		GetVolumeFn: func(_ context.Context, name string) (msb.VolumeHandle, error) {
			return &msb.MockVolumeHandle{Name_: name}, nil
		},
	})

	k := state.Key{Slug: "slug", Agent: "opencode"}
	if err := seedHomeState(k, state.HomeState{HomeVolume: "home-vol", ImageDigest: "sha256:abc123"}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	offered := false
	origOffer := offerRuntimeHomeMigration
	offerRuntimeHomeMigration = func(_ context.Context, _ msb.Client, _ termio.UI) error {
		offered = true
		return nil
	}
	t.Cleanup(func() { offerRuntimeHomeMigration = origOffer })

	ui := termio.NewTestMock(t)
	if _, _, _, _, err := prepareHomeVolume(
		context.Background(), k, sandboximage.ImageInfo{},
		true, /* skipVM */
		state.Fingerprints{},
		&ui,
	); err != nil {
		t.Fatalf("prepareHomeVolume() error = %v", err)
	}
	if !offered {
		t.Error("the migration offer must run on the prepare path")
	}
}

// TestPrepareHomeVolumeOfferErrorPropagates verifies that a failure from the
// runtime-home migration offer aborts volume setup with a wrapped error.
func TestPrepareHomeVolumeOfferErrorPropagates(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	msb.WithMsbMock(t, &msb.MockMsbClient{})

	k := state.Key{Slug: "slug", Agent: "opencode"}
	wantErr := errors.New("offer boom")
	origOffer := offerRuntimeHomeMigration
	offerRuntimeHomeMigration = func(_ context.Context, _ msb.Client, _ termio.UI) error {
		return wantErr
	}
	t.Cleanup(func() { offerRuntimeHomeMigration = origOffer })

	ui := termio.NewTestMock(t)
	_, _, _, _, err := prepareHomeVolume(
		context.Background(),
		k,
		sandboximage.ImageInfo{},
		false,
		state.Fingerprints{},
		&ui,
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("prepareHomeVolume() error = %v, want %v", err, wantErr)
	}
}

func TestLiveHomeVolumeReturnsMountedVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	k := state.Key{Slug: "slug", Agent: "opencode"}
	mock := &msb.MockMsbClient{
		GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
			return &msb.MockSandboxHandle{
				Cfg: &msbSdk.SandboxConfig{Image: "img:tag"},
				Mounts: map[string]msbSdk.MountConfig{
					mounts.VMHomeDir: msbSdk.Mount.Named("home-vol", msbSdk.MountOptions{}),
				},
			}, nil
		},
	}

	ui := termio.NewTestMock(t)
	if got := liveHomeVolume(context.Background(), mock, k, &ui); got != "home-vol" {
		t.Errorf("liveHomeVolume() = %q, want %q", got, "home-vol")
	}
}

func TestLiveHomeVolumeNoVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	k := state.Key{Slug: "slug", Agent: "opencode"}
	ui := termio.NewTestMock(t)
	if got := liveHomeVolume(context.Background(), &msb.MockMsbClient{}, k, &ui); got != "" {
		t.Errorf("liveHomeVolume() = %q, want empty when no VM exists", got)
	}
}

// TestLiveHomeVolumeMountsError covers a mounts read failure on an otherwise
// readable VM: the recovery falls back to an empty result instead of failing.
func TestLiveHomeVolumeMountsError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	k := state.Key{Slug: "slug", Agent: "opencode"}
	mock := &msb.MockMsbClient{
		GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
			return &msb.MockSandboxHandle{
				Cfg:       &msbSdk.SandboxConfig{Image: "img:tag"},
				MountsErr: errors.New("mounts boom"),
			}, nil
		},
	}

	ui := termio.NewTestMock(t)
	if got := liveHomeVolume(context.Background(), mock, k, &ui); got != "" {
		t.Errorf("liveHomeVolume() = %q, want empty on mounts error", got)
	}
	if !strings.Contains(strings.Join(ui.VerboseCalls, " "), "home-volume recovery") {
		t.Errorf("expected a verbose mounts-read log, got %v", ui.VerboseCalls)
	}
}

// TestLiveHomeVolumeLookupErrorLogs covers a transient (non-not-found) VM lookup
// failure: it is verbose-logged rather than silently falling back to a fresh
// volume, which could orphan the surviving one.
func TestLiveHomeVolumeLookupErrorLogs(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	k := state.Key{Slug: "slug", Agent: "opencode"}
	mock := &msb.MockMsbClient{
		GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
			return nil, errors.New("lookup boom")
		},
	}

	ui := termio.NewTestMock(t)
	if got := liveHomeVolume(context.Background(), mock, k, &ui); got != "" {
		t.Errorf("liveHomeVolume() = %q, want empty on lookup error", got)
	}
	if !strings.Contains(strings.Join(ui.VerboseCalls, " "), "home-volume recovery") {
		t.Errorf("expected a verbose lookup-error log, got %v", ui.VerboseCalls)
	}
}

// TestLiveHomeVolumeNilHandle covers the defensive nil-handle branch.
func TestLiveHomeVolumeNilHandle(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	k := state.Key{Slug: "slug", Agent: "opencode"}
	mock := &msb.MockMsbClient{
		GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
			return nil, nil //nolint:nilnil // exercises the defensive nil-handle branch
		},
	}

	ui := termio.NewTestMock(t)
	if got := liveHomeVolume(context.Background(), mock, k, &ui); got != "" {
		t.Errorf("liveHomeVolume() = %q, want empty for a nil handle", got)
	}
}

// TestPrepareHomeVolumeRecoversLostStateFromLiveVM covers issue #219 end to end
// through the volume-setup entry point: with no state file but a project VM
// that still mounts a home volume, the existing volume is reused (no duplicate
// created) and state is rewritten to point at it.
func TestPrepareHomeVolumeRecoversLostStateFromLiveVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const recovered = "agents-sandbox-home-slug-20260101T000000"
	k := state.Key{Slug: "slug", Agent: "opencode"}

	mock := &msb.MockMsbClient{
		GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
			return &msb.MockSandboxHandle{
				Cfg: &msbSdk.SandboxConfig{Image: "img:tag"},
				Mounts: map[string]msbSdk.MountConfig{
					mounts.VMHomeDir: msbSdk.Mount.Named(recovered, msbSdk.MountOptions{}),
				},
			}, nil
		},
		GetVolumeFn: func(_ context.Context, name string) (msb.VolumeHandle, error) {
			if name == recovered {
				return &msb.MockVolumeHandle{Name_: name}, nil
			}
			return nil, fmt.Errorf("volume %s not found", name)
		},
		CreateVolumeFn: func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			t.Fatalf("CreateVolume must not be called during recovery (got %q)", name)
			return nil, errors.New("unexpected CreateVolume")
		},
	}
	msb.WithMsbMock(t, mock)

	origOffer := offerRuntimeHomeMigration
	offerRuntimeHomeMigration = func(_ context.Context, _ msb.Client, _ termio.UI) error { return nil }
	t.Cleanup(func() { offerRuntimeHomeMigration = origOffer })

	ui := termio.NewTestMock(t)
	_, _, homeVol, _, err := prepareHomeVolume(
		context.Background(),
		k,
		sandboximage.ImageInfo{Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		&ui,
	)
	if err != nil {
		t.Fatalf("prepareHomeVolume() error = %v", err)
	}
	if homeVol != recovered {
		t.Errorf("home volume = %q, want recovered %q", homeVol, recovered)
	}
	persisted, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("state not rewritten: %v", err)
	}
	if persisted.HomeVolume != recovered {
		t.Errorf("persisted home volume = %q, want %q", persisted.HomeVolume, recovered)
	}
}

func TestResolveServeHostPortNotServeOnly(t *testing.T) {
	if got := resolveServeHostPort(options.RunOptions{Agent: "opencode"}, 0); got != 0 {
		t.Errorf("resolveServeHostPort(not serve-only) = %d, want 0", got)
	}
}

func TestResolveServeHostPortPassthrough(t *testing.T) {
	if got := resolveServeHostPort(options.RunOptions{ServeOnly: true}, 4096); got != 4096 {
		t.Errorf("resolveServeHostPort(planned 4096) = %d, want 4096", got)
	}
}

func TestResolveServeHostPortProbesWhenZero(t *testing.T) {
	got := resolveServeHostPort(options.RunOptions{ServeOnly: true}, 0)
	if got == 0 || got < options.ServeOnlyBasePort {
		t.Errorf("resolveServeHostPort(planned 0) = %d, want a probed port >= %d", got, options.ServeOnlyBasePort)
	}
}

func symlinkedDir(t *testing.T) (realDir, linkDir string) {
	t.Helper()
	realDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	linkDir = filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	return realDir, linkDir
}

func TestResolveWorkspaceDirResolvesSymlinkInCwd(t *testing.T) {
	realDir, linkDir := symlinkedDir(t)
	t.Chdir(linkDir)

	got, err := resolveWorkspaceDir()
	if err != nil {
		t.Fatalf("resolveWorkspaceDir() error = %v", err)
	}
	if got != realDir {
		t.Errorf("resolveWorkspaceDir() = %q, want %q", got, realDir)
	}
}

func TestResolveWorkspacePathResolvesSymlink(t *testing.T) {
	realDir, linkDir := symlinkedDir(t)

	got, err := resolveWorkspacePath(linkDir)
	if err != nil {
		t.Fatalf("resolveWorkspacePath() error = %v", err)
	}
	if got != realDir {
		t.Errorf("resolveWorkspacePath() = %q, want %q", got, realDir)
	}
}

func TestResolveWorkspacePathError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	if _, err := resolveWorkspacePath(missing); err == nil {
		t.Fatal("expected error when the path does not exist")
	}
}

// TestResolveWorkspaceDirGetwdError covers the branch where the current
// working directory no longer exists (e.g. it was deleted while running), so
// os.Getwd itself fails.
func TestResolveWorkspaceDirGetwdError(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Remove(dir); err != nil {
		t.Skipf("cannot remove cwd: %v", err)
	}

	if _, err := resolveWorkspaceDir(); err == nil {
		t.Fatal("expected error when the current directory no longer exists")
	}
}

// TestPrepareSandboxResolveWorkspaceDirError covers the propagation of a
// workspace-directory resolution failure through PrepareSandbox.
func TestPrepareSandboxResolveWorkspaceDirError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Remove(dir); err != nil {
		t.Skipf("cannot remove cwd: %v", err)
	}

	ui := termio.NewTestMock(t)
	if _, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, false, &ui); err == nil {
		t.Fatal("expected error when the workspace directory cannot be resolved")
	}
}

// TestPrepareHomeVolumeRecoversPersistedAdoptionVolume covers the lost-state
// migration path (#144): adoption recorded the destroyed legacy VM's home volume
// in the checkout state and the run never received it in memory, so home
// resolution must recover it from the state rather than mint a fresh one.
func TestPrepareHomeVolumeRecoversPersistedAdoptionVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const recovered = "agents-sandbox-home-widget-aaa-opencode-9"
	k := state.Key{Slug: adoptCheckoutSlug, Agent: adoptAgentName}
	if err := seedHomeState(k, state.HomeState{RecoveredHomeVolume: recovered}); err != nil {
		t.Fatalf("seed adoption-recovered state: %v", err)
	}

	mock := &msb.MockMsbClient{
		GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "legacy VM already destroyed"}
		},
		GetVolumeFn: func(_ context.Context, name string) (msb.VolumeHandle, error) {
			if name == recovered {
				return &msb.MockVolumeHandle{Name_: name}, nil
			}
			return nil, fmt.Errorf("volume %s not found", name)
		},
		CreateVolumeFn: func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			t.Fatalf("CreateVolume must not be called during recovery (got %q)", name)
			return nil, errors.New("unexpected CreateVolume")
		},
	}
	msb.WithMsbMock(t, mock)

	origOffer := offerRuntimeHomeMigration
	offerRuntimeHomeMigration = func(_ context.Context, _ msb.Client, _ termio.UI) error { return nil }
	t.Cleanup(func() { offerRuntimeHomeMigration = origOffer })

	ui := termio.NewTestMock(t)
	_, _, homeVol, _, err := prepareHomeVolume(
		context.Background(),
		k,
		sandboximage.ImageInfo{Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		&ui,
	)
	if err != nil {
		t.Fatalf("prepareHomeVolume() error = %v", err)
	}
	if homeVol != recovered {
		t.Errorf("home volume = %q, want the adoption-recovered %q", homeVol, recovered)
	}
}
