package volume

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/atomicwrite"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestEnsureNewHome_NameCollisionRetries pins the same-second case of #431:
// HomeVolumeName has second granularity, so two racing sessions can generate
// the same name. The loser waits for the next name instead of failing.
func TestEnsureNewHome_NameCollisionRetries(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	waits := withFakeHomeVolumeClock(t)
	k := state.Key{Slug: "collideproj", Agent: "opencode"}
	takenName := HomeVolumeName(k)
	var attemptedNames []string
	client := &msb.MockMsbClient{
		CreatedSandbox: &msb.MockSandbox{Name_: "prefill"},
		CreateVolumeFn: func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
			attemptedNames = append(attemptedNames, name)
			if name == takenName {
				return nil, errVolumeExists
			}
			return msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	ui := &termio.Mock{}

	volName, _, err := NewManager(docker.Get()).EnsureNewHome(
		context.Background(), client, k, raceTestImage, false, state.Fingerprints{}, "", ui,
	)

	require.NoError(t, err)
	require.NotEqual(t, takenName, volName)
	require.Equal(t, []string{takenName, volName}, attemptedNames)
	require.Equal(t, 1, *waits)
}

// TestEnsureNewHome_SkipVM_CreatesNothing pins #429: a skip-VM run cannot
// prefill, so it must neither create the volume nor record it in the state;
// otherwise a later real run reuses a never-prefilled home.
func TestEnsureNewHome_SkipVM_CreatesNothing(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := &msb.MockMsbClient{
		CreateVolumeFn: func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
			t.Errorf("skip-VM run created volume %q", name)
			return &msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	k := state.Key{Slug: "testproj", Agent: "opencode"}
	ui := &termio.Mock{}

	volName, hs, err := NewManager(docker.Get()).EnsureNewHome(
		context.Background(),
		mock,
		k,
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:abc"},
		true, // skipVM
		state.Fingerprints{},
		"",
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if volName != HomeVolumeName(k) || hs.HomeVolume != volName {
		t.Errorf("volName = %q, hs.HomeVolume = %q, want %q", volName, hs.HomeVolume, HomeVolumeName(k))
	}
	if _, err := state.ReadState(k); !errors.Is(err, state.ErrStateNotFound) {
		t.Errorf("ReadState err = %v, want ErrStateNotFound (no state written under skip-VM)", err)
	}
	if len(ui.InfoCalls) != 1 ||
		!strings.Contains(ui.InfoCalls[0], "would create and prefill home volume "+strconv.Quote(volName)) {
		t.Errorf("InfoCalls = %v, want the deferred create-and-prefill notice for %q", ui.InfoCalls, volName)
	}
}

func TestEnsureNewHome_CreateVolumeFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := &msb.MockMsbClient{}
	mock.CreateVolumeFn = func(_ context.Context, _ string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
		return nil, errors.New("create failed")
	}
	vm := NewManager(docker.Get())
	_, _, err := vm.EnsureNewHome(
		context.Background(),
		mock,
		state.Key{Slug: "testproj", Agent: "opencode"},
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:abc"},
		false,
		state.Fingerprints{},
		"",
		&termio.Mock{},
	)
	if err == nil || !strings.Contains(err.Error(), "create volume") || errors.Unwrap(err) == nil {
		t.Fatalf("expected create volume error, got: %v", err)
	}
}

func TestRecordHomeImage_ReadStateError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "corrupt-rh"
	statePath := filepath.Join(configpaths.Get().UserStateDir(), slug, "opencode", "state.yaml")
	if err := os.MkdirAll(statePath, 0o700); err != nil {
		t.Fatal(err)
	}

	vm := NewManager(docker.Get())
	err := vm.RecordHomeImage(state.Key{Slug: slug, Agent: "opencode"}, "sha256:new", &termio.Mock{})
	if err == nil {
		t.Fatal("expected error for unreadable state, got nil")
	}
}

func TestRecordHomeImage_WriteStateFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "writefail-rh"
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "vol", ImageDigest: "sha256:old"},
	)
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory does not block writes")
	}
	sdir := filepath.Join(configpaths.Get().UserStateDir(), slug, "opencode")
	// A read-only state dir makes the atomic temp-file creation fail, so the
	// state write fails while reads (needing only execute) still succeed.
	if err := os.Chmod(sdir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sdir, 0o700) })

	ui := &termio.Mock{}
	vm := NewManager(docker.Get())
	err := vm.RecordHomeImage(state.Key{Slug: slug, Agent: "opencode"}, "sha256:new", ui)
	if err == nil {
		t.Fatal("expected write-state error, got nil")
	}
	var warned bool
	for _, c := range ui.WarnCalls {
		if strings.Contains(c, "failed to update state file") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("expected update-state warning, got: %v", ui.WarnCalls)
	}
}

// TestEnsureNewHome_ConcurrentCommitAdoptsWinner pins #431: when another
// session commits a fresh home volume while this one is creating its own, the
// later commit must not clobber the earlier one. It adopts the committed volume
// and removes its own, so no volume is left unrecorded.
func TestEnsureNewHome_ConcurrentCommitAdoptsWinner(t *testing.T) {
	tests := []struct {
		name          string
		priorRecorded string
	}{
		{name: "state absent", priorRecorded: ""},
		{name: "recorded volume gone", priorRecorded: "gone-vol"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			docker.WithNoopDockerMock(t)
			k := state.Key{Slug: "raceproj", Agent: "opencode"}
			if tt.priorRecorded != "" {
				require.NoError(t, seedHomeState(k, state.NewHomeState(tt.priorRecorded, "sha256:old")))
			}
			var createdVolume string
			client := &msb.MockMsbClient{
				CreatedSandbox: &msb.MockSandbox{Name_: "prefill"},
				CreateVolumeFn: func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
					createdVolume = name
					commitConcurrentHome(t, k, "winner-vol")
					return msb.MockVolumeHandle{Name_: name}, nil
				},
			}
			ui := &termio.Mock{}

			volName, hs, err := NewManager(docker.Get()).EnsureNewHome(
				context.Background(), client, k, raceTestImage, false, state.Fingerprints{}, tt.priorRecorded, ui,
			)

			require.NoError(t, err)
			require.Equal(t, "winner-vol", volName)
			require.Equal(t, "winner-vol", hs.HomeVolume)
			require.Equal(t, []string{createdVolume}, client.RemovedVolumes)
			persisted, err := state.ReadState(k)
			require.NoError(t, err)
			require.Equal(t, "winner-vol", persisted.HomeVolume)
			require.Equal(t, "sha256:winner", persisted.ImageDigest)
			require.Contains(t, ui.InfoCalls, `home volume "winner-vol" was created concurrently, using it`)
		})
	}
}

// TestEnsureNewHome_ReplacesStaleRecordedVolume pins that a state whose
// recorded volume is gone, unchanged since the snapshot, is still replaced.
func TestEnsureNewHome_ReplacesStaleRecordedVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	k := state.Key{Slug: "staleproj", Agent: "opencode"}
	require.NoError(t, seedHomeState(k, state.NewHomeState("gone-vol", "sha256:old")))
	client := &msb.MockMsbClient{
		CreatedSandbox: &msb.MockSandbox{Name_: "prefill"},
		CreateVolumeFn: func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
			return msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	ui := &termio.Mock{}

	volName, _, err := NewManager(docker.Get()).EnsureNewHome(
		context.Background(), client, k, raceTestImage, false, state.Fingerprints{}, "gone-vol", ui,
	)

	require.NoError(t, err)
	require.NotEqual(t, "gone-vol", volName)
	require.Empty(t, client.RemovedVolumes)
	persisted, err := state.ReadState(k)
	require.NoError(t, err)
	require.Equal(t, volName, persisted.HomeVolume)
}

// TestEnsureNewHome_ReplacesConcurrentEmptyHomeVolume pins that a concurrent
// document naming no home volume (an identity-adoption marker) is not a
// committed winner, so the fresh volume is still recorded.
func TestEnsureNewHome_ReplacesConcurrentEmptyHomeVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	k := state.Key{Slug: "markerproj", Agent: "opencode"}
	client := &msb.MockMsbClient{
		CreatedSandbox: &msb.MockSandbox{Name_: "prefill"},
		CreateVolumeFn: func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
			require.NoError(t, seedHomeState(k, state.HomeState{RecoveredHomeVolume: "legacy-vol"}))
			return msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	ui := &termio.Mock{}

	volName, _, err := NewManager(docker.Get()).EnsureNewHome(
		context.Background(), client, k, raceTestImage, false, state.Fingerprints{}, "gone-vol", ui,
	)

	require.NoError(t, err)
	require.Empty(t, client.RemovedVolumes)
	persisted, err := state.ReadState(k)
	require.NoError(t, err)
	require.Equal(t, volName, persisted.HomeVolume)
}

// TestResolveHomeVolume_WinnerCommitsBeforeCreation pins a winner that
// commits after resolution found the recorded volume gone but before the fresh
// volume is created: the compare-and-set must use the state resolution
// observed, not a later re-read that already names the winner.
func TestResolveHomeVolume_WinnerCommitsBeforeCreation(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	k := state.Key{Slug: "earlyproj", Agent: "opencode"}
	require.NoError(t, seedHomeState(k, state.NewHomeState("gone-vol", "sha256:old")))
	var createdVolume string
	client := &msb.MockMsbClient{
		CreatedSandbox: &msb.MockSandbox{Name_: "prefill"},
		GetVolumeFn: func(_ context.Context, name string) (msb.VolumeHandle, error) {
			if name == "gone-vol" {
				commitConcurrentHome(t, k, "winner-vol")
			}
			return nil, &msbSdk.Error{Kind: msbSdk.ErrVolumeNotFound, Message: "no such volume"}
		},
		CreateVolumeFn: func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
			createdVolume = name
			return msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	ui := &termio.Mock{}

	volName, _, err := NewManager(docker.Get()).ResolveHomeVolume(
		context.Background(), client, k, raceTestImage, false, state.Fingerprints{}, "", ui,
	)

	require.NoError(t, err)
	require.Equal(t, "winner-vol", volName)
	require.Equal(t, []string{createdVolume}, client.RemovedVolumes)
	persisted, err := state.ReadState(k)
	require.NoError(t, err)
	require.Equal(t, "winner-vol", persisted.HomeVolume)
}

func TestEnsureNewHome_NameCollisionGivesUp(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	waits := withFakeHomeVolumeClock(t)
	k := state.Key{Slug: "collideproj", Agent: "opencode"}
	attempts := 0
	client := &msb.MockMsbClient{
		CreatedSandbox: &msb.MockSandbox{Name_: "prefill"},
		CreateVolumeFn: func(_ context.Context, _ string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
			attempts++
			return nil, errVolumeExists
		},
	}
	ui := &termio.Mock{}

	_, _, err := NewManager(docker.Get()).EnsureNewHome(
		context.Background(), client, k, raceTestImage, false, state.Fingerprints{}, "", ui,
	)

	require.Error(t, err)
	require.True(t, msb.IsVolumeAlreadyExists(err))
	require.Equal(t, 3, attempts)
	require.Equal(t, 2, *waits)
}

func TestEnsureNewHome_CreateErrorDoesNotRetry(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	waits := withFakeHomeVolumeClock(t)
	createErr := errors.New("disk full")
	client := &msb.MockMsbClient{
		CreatedSandbox: &msb.MockSandbox{Name_: "prefill"},
		CreateVolumeFn: func(_ context.Context, _ string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
			return nil, createErr
		},
	}
	ui := &termio.Mock{}

	_, _, err := NewManager(docker.Get()).EnsureNewHome(
		context.Background(), client, state.Key{Slug: "failproj", Agent: "opencode"},
		raceTestImage, false, state.Fingerprints{}, "", ui,
	)

	require.ErrorIs(t, err, createErr)
	require.Zero(t, *waits)
}

// TestEnsureNewHomeSeedsFingerprints pins that a fresh home volume's state
// carries the desired config fingerprints, so it is never fingerprint-less
// beside the VM created from it (issue #183).
func TestEnsureNewHomeSeedsFingerprints(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	docker.WithNoopDockerMock(t)

	client := &msb.MockMsbClient{
		CreatedSandbox: &msb.MockSandbox{Name_: "prefill"},
		CreateVolumeFn: func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
			return msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	ui := &termio.Mock{}
	k := state.Key{Slug: "seedproj", Agent: "opencode"}
	desired := state.Fingerprints{
		Network: state.BuildNetworkState(network.Policy{Profiles: []network.Profile{network.ProfileNone}}),
		Mount:   state.BuildMountState(mounts.Mounts{"/host/data": {Source: "/host/data"}}),
	}

	_, hs, err := NewManager(docker.Get()).EnsureNewHome(
		context.Background(),
		client,
		k,
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:digest"},
		false, // a real run: only it records a fresh home (#429)
		desired,
		"",
		ui,
	)
	if err != nil {
		t.Fatalf("EnsureNewHome: %v", err)
	}
	if !reflect.DeepEqual(hs.NetworkState, desired.Network) || !reflect.DeepEqual(hs.MountState, desired.Mount) {
		t.Errorf("returned state not seeded: got %+v, want %+v", hs, desired)
	}

	onDisk, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if !reflect.DeepEqual(onDisk.NetworkState, desired.Network) ||
		!reflect.DeepEqual(onDisk.MountState, desired.Mount) {
		t.Errorf("persisted state not seeded: got %+v, want %+v", onDisk, desired)
	}
}

// TestResolveHomeVolumeRepairsCorruptState pins that a malformed state file is
// replaced by recovery: ResolveHomeVolume routes it to the recovered volume,
// and the rewrite must not fail on the same broken document it is replacing.
func TestResolveHomeVolumeRepairsCorruptState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	client := &msb.MockMsbClient{
		GetVolumeFn: func(_ context.Context, name string) (msb.VolumeHandle, error) {
			return msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	ui := &termio.Mock{}
	k := state.Key{Slug: "corruptproj", Agent: "opencode"}
	if err := os.MkdirAll(state.KeyDir(k), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state.KeyDir(k), "state.yaml"), []byte("{ corrupt: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	volName, hs, err := NewManager(docker.Get()).ResolveHomeVolume(
		context.Background(),
		client,
		k,
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:digest"},
		true,
		state.Fingerprints{},
		"home-recovered",
		ui,
	)
	if err != nil {
		t.Fatalf("ResolveHomeVolume: %v", err)
	}
	if volName != "home-recovered" || hs.HomeVolume != "home-recovered" {
		t.Fatalf("recovered volume = %q / state home = %q, want home-recovered", volName, hs.HomeVolume)
	}

	onDisk, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState after recovery = %v, want the corrupt state repaired", err)
	}
	if onDisk.HomeVolume != "home-recovered" {
		t.Errorf("persisted HomeVolume = %q, want home-recovered", onDisk.HomeVolume)
	}
}

// TestResolveHomeVolumeRepairCorruptSeedsFingerprintsAndClearsUpgradeFields pins
// the lost/corrupt recovery contract: the repaired state is seeded with the
// desired config fingerprints (never fingerprint-less beside a live VM) and its
// upgrade fields are left empty, self-healed on boot by recordImageProvenance;
// offered_versions loss is the accepted cost.
func TestResolveHomeVolumeRepairCorruptSeedsFingerprintsAndClearsUpgradeFields(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	client := &msb.MockMsbClient{
		GetVolumeFn: func(_ context.Context, name string) (msb.VolumeHandle, error) {
			return msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	ui := &termio.Mock{}
	k := state.Key{Slug: "corruptseed", Agent: "opencode"}
	if err := os.MkdirAll(state.KeyDir(k), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state.KeyDir(k), "state.yaml"), []byte("{ corrupt: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	desired := state.Fingerprints{
		Network: state.BuildNetworkState(network.Policy{Profiles: []network.Profile{network.ProfileNone}}),
		Mount:   state.BuildMountState(mounts.Mounts{"/host/data": {Source: "/host/data"}}),
	}
	_, hs, err := NewManager(docker.Get()).ResolveHomeVolume(
		context.Background(),
		client,
		k,
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:digest"},
		true,
		desired,
		"home-recovered",
		ui,
	)
	if err != nil {
		t.Fatalf("ResolveHomeVolume: %v", err)
	}
	if !reflect.DeepEqual(hs.NetworkState, desired.Network) || !reflect.DeepEqual(hs.MountState, desired.Mount) {
		t.Errorf("repaired state not seeded: got %+v, want %+v", hs, desired)
	}
	if hs.AgentVersion != "" || hs.AgentSource != "" || hs.DockerSource != "" || len(hs.OfferedVersions) != 0 {
		t.Errorf("upgrade fields must be left empty on a corrupt-state repair: %+v", hs)
	}

	onDisk, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState after recovery = %v, want the corrupt state repaired", err)
	}
	if !reflect.DeepEqual(onDisk.NetworkState, desired.Network) {
		t.Errorf("persisted state not seeded: got %+v, want %+v", onDisk, desired)
	}
}

// TestResolveHomeVolumeExistingStateKeepsFingerprints pins that the existing
// path is not reseeded: a stored fingerprint (possibly stale) must survive so
// the reconfig decision can detect a real change.
func TestResolveHomeVolumeExistingStateKeepsFingerprints(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	client := &msb.MockMsbClient{
		GetVolumeFn: func(_ context.Context, name string) (msb.VolumeHandle, error) {
			return msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	ui := &termio.Mock{}
	k := state.Key{Slug: "existingproj", Agent: "opencode"}
	stored := state.BuildNetworkState(
		network.Policy{Profiles: []network.Profile{network.ProfilePublic}, EgressAllow: []string{"example.com"}},
	)
	if err := seedHomeState(k, state.HomeState{
		HomeVolume:   "home-vol",
		ImageDigest:  "sha256:abc",
		NetworkState: stored,
	}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	desired := state.Fingerprints{
		Network: state.BuildNetworkState(network.Policy{Profiles: []network.Profile{network.ProfileNone}}),
	}
	_, hs, err := NewManager(docker.Get()).ResolveHomeVolume(
		context.Background(),
		client,
		k,
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:abc"},
		true,
		desired,
		"",
		ui,
	)
	if err != nil {
		t.Fatalf("ResolveHomeVolume: %v", err)
	}
	if !reflect.DeepEqual(hs.NetworkState, stored) {
		t.Errorf("existing fingerprint was overwritten: got %+v, want %+v", hs.NetworkState, stored)
	}
}

// TestResolveHomeVolumeSeedsFingerprints is the regression test for #183: a
// fresh home volume's state must carry the desired config fingerprints, so a
// second session reading the state beside the freshly created VM never mistakes
// the empty applied fingerprint for a "network policy changed" (or env/secret/
// mount) change. Seeding happens as part of the fresh-home write, closing both
// the concurrent window and the crash/deleted-state route.
func TestResolveHomeVolumeSeedsFingerprints(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	docker.WithNoopDockerMock(t)

	client := &msb.MockMsbClient{
		CreatedSandbox: &msb.MockSandbox{Name_: "prefill"},
		CreateVolumeFn: func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
			return msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	ui := termio.NewTestMock(t)
	k := state.Key{Slug: "seedproj", Agent: "opencode"}
	policy := network.Policy{Profiles: []network.Profile{network.ProfileNone}}
	extraMounts := mounts.Mounts{
		"/host/data": {Source: "/host/data"},
	}

	fingerprints := state.Fingerprints{
		Network: state.BuildNetworkState(policy),
		Mount:   state.BuildMountState(extraMounts),
	}
	_, homeState, err := NewManager(docker.Get()).ResolveHomeVolume(
		context.Background(),
		client,
		k,
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:digest"},
		false, // a real run: only it creates and records a fresh home (#429)
		fingerprints,
		"",
		&ui,
	)
	if err != nil {
		t.Fatalf("ResolveHomeVolume: %v", err)
	}

	if state.NetworkChanged(homeState.NetworkState, policy) {
		t.Error("#183: fresh home state reports a spurious network change")
	}
	if state.MountsChanged(homeState.MountState, extraMounts) {
		t.Error("#183: fresh home state reports a spurious mount change")
	}

	onDisk, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if state.NetworkChanged(onDisk.NetworkState, policy) {
		t.Error("#183: persisted fresh state is fingerprint-less; a concurrent session sees a spurious network change")
	}
	if state.MountsChanged(onDisk.MountState, extraMounts) {
		t.Error("#183: persisted fresh state is fingerprint-less; a concurrent session sees a spurious mount change")
	}
}

// TestResolveHomeVolume_ReuseWarnsNothing pins that a recorded volume that still
// resolves is reused silently: the real path neither reports a state-read
// failure nor falls through to the recovery warning.
func TestResolveHomeVolume_ReuseWarnsNothing(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	key := state.Key{Slug: "quietreuse", Agent: "opencode"}
	if err := seedHomeState(key, state.HomeState{HomeVolume: "vol-keep"}); err != nil {
		t.Fatal(err)
	}

	ui := &termio.Mock{}
	_, hs, err := NewManager(docker.Get()).ResolveHomeVolume(
		context.Background(), lookupMock([]string{"vol-keep"}, false), key,
		image.ImageInfo{Digest: "sha256:def"}, false, state.Fingerprints{}, "", ui,
	)
	if err != nil {
		t.Fatalf("ResolveHomeVolume: %v", err)
	}
	if hs.HomeVolume != "vol-keep" {
		t.Errorf("state volume = %q, want vol-keep", hs.HomeVolume)
	}
	if len(ui.WarnCalls) != 0 {
		t.Errorf("reuse emitted warnings: %v", ui.WarnCalls)
	}
}

// TestResolveHomeVolume_AbsentStateWarnsNothing pins that a missing state file
// (ErrStateNotFound) is not reported as a read failure; only a genuinely
// unreadable document is.
func TestResolveHomeVolume_AbsentStateWarnsNothing(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	key := state.Key{Slug: "absentquiet", Agent: "opencode"}

	ui := &termio.Mock{}
	_, _, err := NewManager(docker.Get()).ResolveHomeVolume(
		context.Background(), lookupMock([]string{"live-quiet"}, false), key,
		image.ImageInfo{Digest: "sha256:def"}, false, state.Fingerprints{}, "live-quiet", ui,
	)
	if err != nil {
		t.Fatalf("ResolveHomeVolume: %v", err)
	}
	if strings.Contains(strings.Join(ui.WarnCalls, "\n"), "could not read state file") {
		t.Errorf("absent state reported as a read failure: %v", ui.WarnCalls)
	}
}

// TestResolveHomeVolume_RecordedLookupErrorWrapsCause pins that a transient
// error probing the recorded volume fails closed with the cause still reachable
// and the recorded-volume context on the error.
func TestResolveHomeVolume_RecordedLookupErrorWrapsCause(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cause := errors.New("daemon unavailable")
	key := state.Key{Slug: "wrapcause", Agent: "opencode"}
	if err := seedHomeState(key, state.HomeState{HomeVolume: "vol-wrap"}); err != nil {
		t.Fatal(err)
	}

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, _ string) (msb.VolumeHandle, error) {
		var vh msb.VolumeHandle
		return vh, fmt.Errorf("lookup: %w", cause)
	}

	_, _, err := NewManager(docker.Get()).ResolveHomeVolume(
		context.Background(), mock, key,
		image.ImageInfo{Digest: "sha256:def"}, false, state.Fingerprints{}, "", &termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected a fail-closed error for the transient recorded-volume lookup")
	}
	if !errors.Is(err, cause) {
		t.Errorf("error %v does not wrap the lookup cause", err)
	}
	if !strings.Contains(err.Error(), "checking home volume") {
		t.Errorf("error %q lacks the recorded-volume context", err)
	}
}

// TestResolveHomeVolume_RecoveryCandidateErrorSurfaces pins that a transient
// error confirming the recovery candidate — after the recorded volume is
// already known gone — is surfaced, not swallowed.
func TestResolveHomeVolume_RecoveryCandidateErrorSurfaces(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cause := errors.New("daemon unavailable")
	key := state.Key{Slug: "candidateerr", Agent: "opencode"}
	if err := seedHomeState(key, state.HomeState{HomeVolume: "vol-gone"}); err != nil {
		t.Fatal(err)
	}

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		if name == "vol-gone" {
			return nil, volumeNotFoundError(name)
		}
		var vh msb.VolumeHandle
		return vh, fmt.Errorf("lookup: %w", cause)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
		t.Fatalf("CreateVolume must not be called for a transient candidate lookup (got %q)", name)
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	_, _, err := NewManager(docker.Get()).ResolveHomeVolume(
		context.Background(), mock, key,
		image.ImageInfo{Digest: "sha256:def"}, false, state.Fingerprints{}, "live-x", &termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected a fail-closed error for the transient candidate lookup")
	}
	if !errors.Is(err, cause) {
		t.Errorf("error %v does not wrap the candidate lookup cause", err)
	}
}

// TestResolveHomeVolume_ClearsAdoptionClaimWhenMarkerVolumeGone pins #144's
// claim retirement on the create path: when an adoption marker's volume is gone,
// a fresh volume is created and the spent claim is still cleared.
func TestResolveHomeVolume_ClearsAdoptionClaimWhenMarkerVolumeGone(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	const marker = "agents-sandbox-home-widget-bbb-opencode-9"
	key := state.Key{Slug: "adoptedwidget2", Agent: "opencode"}
	if err := seedHomeState(key, state.HomeState{RecoveredHomeVolume: marker}); err != nil {
		t.Fatal(err)
	}
	seedAdoptionClaim(t, "widget-bbb", key.Slug, marker)

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		var vh msb.VolumeHandle
		return vh, volumeNotFoundError(name)
	}

	if _, _, err := NewManager(docker.Get()).ResolveHomeVolume(
		context.Background(), mock, key,
		image.ImageInfo{Digest: "sha256:new"}, false, state.Fingerprints{}, "", &termio.Mock{},
	); err != nil {
		t.Fatalf("ResolveHomeVolume: %v", err)
	}

	root, err := state.ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	if _, ok := root.AdoptedHomeVolume("widget-bbb", "opencode"); ok {
		t.Errorf("adoption claim survived the create fallback: %+v", root.AdoptedHomeVolumes)
	}
}

func TestResolveHomeVolume_VolumeNotFound_Warns(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	slug := "myproj"
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{HomeVolume: "agents-sandbox-home-myproj-old", ImageDigest: "sha256:abc"},
	)

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		var vh msb.VolumeHandle
		return vh, volumeNotFoundError(name)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	mockUI := &termio.Mock{}
	vm := NewManager(docker.Get())
	_, _, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		state.Key{Slug: slug, Agent: "opencode"},
		image.ImageInfo{DockerTag: "latest", Ref: "latest", Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		"",
		mockUI,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mockUI.WarnCalls) == 0 {
		t.Error("expected warning about volume not found")
	}
}

// TestResolveHomeVolume_MissingRecordedVolumeStartsFresh pins the clean-break
// stale-state contract: after the private-home change, a project's recorded home
// volume no longer resolves. ResolveHomeVolume must detect the missing volume,
// create a fresh one, rewrite state to the new name, and return no error —
// rather than failing the session.
func TestResolveHomeVolume_MissingRecordedVolumeStartsFresh(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	key := state.Key{Slug: "stalehome", Agent: "opencode"}
	const recordedVolume = "agents-sandbox-home-stalehome-legacy"
	seedHomeState(key, state.HomeState{
		HomeVolume:  recordedVolume,
		ImageDigest: "sha256:legacy",
	})

	var createdName string
	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		var vh msb.VolumeHandle
		return vh, volumeNotFoundError(name)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
		createdName = name
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	ui := &termio.Mock{}
	vm := NewManager(docker.Get())
	volName, st, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		key,
		image.ImageInfo{DockerTag: "latest", Ref: "latest", Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		"",
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if createdName == "" {
		t.Fatal("expected a fresh volume to be created")
	}
	if volName != createdName {
		t.Errorf("returned volume = %q, want created volume %q", volName, createdName)
	}
	if volName == recordedVolume {
		t.Errorf("returned volume = %q, must not reuse the stale recorded name", recordedVolume)
	}

	persisted, err := state.ReadState(key)
	if err != nil {
		t.Fatalf("state not rewritten: %v", err)
	}
	if persisted.HomeVolume != volName {
		t.Errorf("persisted home volume = %q, want %q", persisted.HomeVolume, volName)
	}
	if st.ImageDigest != "sha256:def" {
		t.Errorf("digest = %q, want %q", st.ImageDigest, "sha256:def")
	}
}

func TestRecordHomeImage_UpdatesDigestInState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	seedHomeState(state.Key{Slug: "myproj", Agent: "opencode"}, state.HomeState{
		HomeVolume:  "agents-sandbox-home-myproj-20260806T143022",
		ImageDigest: "sha256:old",
	})

	vm := NewManager(docker.Get())
	if err := vm.RecordHomeImage(
		state.Key{Slug: "myproj", Agent: "opencode"},
		"sha256:new",
		&termio.Mock{},
	); err != nil {
		t.Fatalf("RecordHomeImage: %v", err)
	}

	st, err := state.ReadState(state.Key{Slug: "myproj", Agent: "opencode"})
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.ImageDigest != "sha256:new" {
		t.Errorf("ImageDigest = %q, want %q", st.ImageDigest, "sha256:new")
	}
	if st.HomeVolume != "agents-sandbox-home-myproj-20260806T143022" {
		t.Errorf("HomeVolume changed to %q, want unchanged", st.HomeVolume)
	}
}

func TestRecordHomeImage_MissingStateIsNoop(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	vm := NewManager(docker.Get())
	k := state.Key{Slug: "nosuchproj", Agent: "opencode"}
	if err := vm.RecordHomeImage(k, "sha256:new", &termio.Mock{}); err != nil {
		t.Fatalf("RecordHomeImage should not error on missing state, got: %v", err)
	}
	if _, err := state.ReadState(k); !errors.Is(err, state.ErrStateNotFound) {
		t.Errorf("ReadState after RecordHomeImage = %v, want no state created", err)
	}
}

func TestResolveHomeVolume_FoundInState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	seedHomeState(state.Key{Slug: "myproj", Agent: "opencode"}, state.HomeState{
		HomeVolume:  "agents-sandbox-home-myproj-20260806T143022",
		ImageDigest: "sha256:abc",
	})

	vm := NewManager(docker.Get())
	volName, st, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		state.Key{Slug: "myproj", Agent: "opencode"},
		image.ImageInfo{DockerTag: "", Ref: "", Digest: "sha256:abc"},
		false,
		state.Fingerprints{},
		"",
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if volName != "agents-sandbox-home-myproj-20260806T143022" {
		t.Errorf("volume = %q, want %q", volName, "agents-sandbox-home-myproj-20260806T143022")
	}
	if st.ImageDigest != "sha256:abc" {
		t.Errorf("digest = %q, want %q", st.ImageDigest, "sha256:abc")
	}
}

func TestResolveHomeVolume_NoStateFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	mock := &msb.MockMsbClient{}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	vm := NewManager(docker.Get())
	volName, st, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		state.Key{Slug: "testproj", Agent: "opencode"},
		image.ImageInfo{DockerTag: "", Ref: "", Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		"",
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(volName, "testproj-") {
		t.Errorf("volume = %q, expected prefix %q", volName, "testproj-")
	}
	if st.ImageDigest != "sha256:def" {
		t.Errorf("digest = %q, want %q", st.ImageDigest, "sha256:def")
	}
}

// TestResolveHomeVolume_CorruptStateFile pins an unreadable state file (a
// non-not-found read error): the caller warns about it and falls back to a
// fresh home volume. A directory in place of the state file cannot be repaired
// by the fallback's wholesale write either, so the write error now surfaces
// (#428) instead of the run proceeding against a volume with no state record;
// the uncommitted volume is removed.
func TestResolveHomeVolume_CorruptStateFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	// A directory in place of the state file makes os.ReadFile fail with a
	// non-not-found error, unlike a genuinely absent file.
	statePath := filepath.Join(configpaths.Get().UserStateDir(), "corruptproj", "opencode", "state.yaml")
	if err := os.MkdirAll(statePath, 0o700); err != nil {
		t.Fatal(err)
	}

	mock := &msb.MockMsbClient{}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	ui := &termio.Mock{}
	vm := NewManager(docker.Get())
	_, _, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		state.Key{Slug: "corruptproj", Agent: "opencode"},
		image.ImageInfo{DockerTag: "", Ref: "", Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		"",
		ui,
	)
	if err == nil {
		t.Fatal("expected the unrepairable state write to surface, got nil")
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, " "), "could not read state file") {
		t.Errorf("expected a 'could not read state file' warning, got %v", ui.WarnCalls)
	}
	if len(mock.RemovedVolumes) != 1 {
		t.Errorf("expected the uncommitted volume to be removed, got %v", mock.RemovedVolumes)
	}
}

func TestResolveHomeVolume_VolumeNotFoundInSandbox(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		var vh msb.VolumeHandle
		return vh, volumeNotFoundError(name)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	seedHomeState(state.Key{Slug: "orphanproj", Agent: "opencode"}, state.HomeState{
		HomeVolume:  "agents-sandbox-home-orphanproj-20260806T143022",
		ImageDigest: "sha256:abc",
	})

	vm := NewManager(docker.Get())
	volName, st, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		state.Key{Slug: "orphanproj", Agent: "opencode"},
		image.ImageInfo{DockerTag: "latest-docker-image", Ref: "latest-docker-image", Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		"",
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(volName, "orphanproj-") {
		t.Errorf("volume = %q, expected prefix %q", volName, "orphanproj-")
	}
	if st.ImageDigest != "sha256:def" {
		t.Errorf("digest = %q, want %q", st.ImageDigest, "sha256:def")
	}
}

// TestResolveHomeVolume_TransientRecordedErrorFailsClosed pins that a lookup
// error that is not true absence (a daemon, timeout, or permission failure) is
// never mistaken for a missing volume: resolution fails closed rather than
// minting a fresh volume over the still-valid one and rewriting the state.
func TestResolveHomeVolume_TransientRecordedErrorFailsClosed(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	key := state.Key{Slug: "transientproj", Agent: "opencode"}
	seedHomeState(key, state.HomeState{
		HomeVolume:  "agents-sandbox-home-transientproj-20260806T143022",
		ImageDigest: "sha256:abc",
	})

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		var vh msb.VolumeHandle
		return vh, fmt.Errorf("daemon unavailable looking up %s", name)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
		t.Fatalf("CreateVolume must not be called on a transient lookup error (got %q)", name)
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	ui := &termio.Mock{}
	vm := NewManager(docker.Get())
	_, _, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		key,
		image.ImageInfo{Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		"",
		ui,
	)
	if err == nil {
		t.Fatal("ResolveHomeVolume returned no error for a transient lookup failure, want fail-closed")
	}

	persisted, readErr := state.ReadState(key)
	if readErr != nil {
		t.Fatalf("state not readable after fail-closed: %v", readErr)
	}
	if persisted.HomeVolume != "agents-sandbox-home-transientproj-20260806T143022" {
		t.Errorf("state was rewritten on a transient error: HomeVolume = %q", persisted.HomeVolume)
	}
}

// TestResolveHomeVolume_TransientRecoveryErrorFailsClosed pins that a transient
// error while confirming the recovered candidate also fails closed: recovery
// must not fall back to a fresh prefill when the candidate's existence cannot
// be established.
func TestResolveHomeVolume_TransientRecoveryErrorFailsClosed(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const recovered = "agents-sandbox-home-transientrecover-20260101T000000"
	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		var vh msb.VolumeHandle
		return vh, fmt.Errorf("daemon unavailable looking up %s", name)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
		t.Fatalf("CreateVolume must not be called on a transient lookup error (got %q)", name)
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	ui := &termio.Mock{}
	vm := NewManager(docker.Get())
	_, _, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		state.Key{Slug: "transientrecover", Agent: "opencode"},
		image.ImageInfo{Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		recovered,
		ui,
	)
	if err == nil {
		t.Fatal("ResolveHomeVolume returned no error for a transient recovery lookup, want fail-closed")
	}
}

// when a project's state file is lost but its VM still mounts a home volume,
// the existing volume must be reused instead of a duplicate being created and
// the previous one orphaned.
func TestResolveHomeVolume_RecoversLiveVMVolumeOnMissingState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const recovered = "agents-sandbox-home-recoverproj-20260101T000000"
	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		if name == recovered {
			return msb.MockVolumeHandle{Name_: name}, nil
		}
		var vh msb.VolumeHandle
		return vh, volumeNotFoundError(name)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
		t.Fatalf("CreateVolume must not be called when a live home volume can be recovered (got %q)", name)
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	key := state.Key{Slug: "recoverproj", Agent: "opencode"}
	ui := &termio.Mock{}
	vm := NewManager(docker.Get())
	volName, st, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		key,
		image.ImageInfo{Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		recovered,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if volName != recovered {
		t.Errorf("volume = %q, want recovered %q", volName, recovered)
	}
	if st.HomeVolume != recovered {
		t.Errorf("state volume = %q, want %q", st.HomeVolume, recovered)
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, " "), "recovering home volume") {
		t.Errorf("expected a recovery warning, got %v", ui.WarnCalls)
	}
	persisted, err := state.ReadState(key)
	if err != nil {
		t.Fatalf("state not written: %v", err)
	}
	if persisted.HomeVolume != recovered {
		t.Errorf("persisted volume = %q, want %q", persisted.HomeVolume, recovered)
	}
}

// TestResolveHomeVolume_RecoversPersistedAdoptionVolume pins the durable
// VM-only identity adoption (#144): the checkout state records the home volume a
// destroyed legacy VM mounted, and resolution finishes the recovery into
// HomeVolume with the current digest, seeds fingerprints, and clears the marker.
func TestResolveHomeVolume_RecoversPersistedAdoptionVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const recovered = "agents-sandbox-home-widget-aaa-opencode-9"
	key := state.Key{Slug: "adoptedwidget", Agent: "opencode"}
	seedHomeState(key, state.HomeState{RecoveredHomeVolume: recovered})

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		if name == recovered {
			return msb.MockVolumeHandle{Name_: name}, nil
		}
		var vh msb.VolumeHandle
		return vh, volumeNotFoundError(name)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
		t.Fatalf("CreateVolume must not be called during recovery (got %q)", name)
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	ui := &termio.Mock{}
	vm := NewManager(docker.Get())
	volName, hs, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		key,
		image.ImageInfo{Digest: "sha256:new"},
		false,
		state.Fingerprints{Env: state.EnvState{Hash: "envhash"}},
		"",
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if volName != recovered {
		t.Errorf("volume = %q, want recovered %q", volName, recovered)
	}
	if hs.HomeVolume != recovered || hs.ImageDigest != "sha256:new" || hs.RecoveredHomeVolume != "" {
		t.Errorf("state = %+v, want the marker recovered into HomeVolume with the digest and cleared", hs)
	}
	if hs.EnvState.Hash != "envhash" {
		t.Errorf("fingerprints used to seed = %+v, want envhash", hs.EnvState)
	}
	persisted, err := state.ReadState(key)
	if err != nil {
		t.Fatalf("state not written: %v", err)
	}
	if persisted.HomeVolume != recovered || persisted.RecoveredHomeVolume != "" {
		t.Errorf("persisted state = %+v, want the marker cleared", persisted)
	}
}

// TestResolveHomeVolume_RecoveredVolumeMissingStartsFresh verifies that a live
// VM mount that no longer resolves (e.g. the volume was removed) does not wedge
// resolution: it falls back to a fresh prefill.
func TestResolveHomeVolume_RecoveredVolumeMissingStartsFresh(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	const recovered = "agents-sandbox-home-goneproj-20260101T000000"
	var createdName string
	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		var vh msb.VolumeHandle
		return vh, volumeNotFoundError(name)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
		createdName = name
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	ui := &termio.Mock{}
	vm := NewManager(docker.Get())
	volName, _, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		state.Key{Slug: "goneproj", Agent: "opencode"},
		image.ImageInfo{Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		recovered,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if createdName == "" {
		t.Fatal("expected a fresh volume to be created when the recovered volume is gone")
	}
	if volName != createdName {
		t.Errorf("returned volume = %q, want created volume %q", volName, createdName)
	}
	if volName == recovered {
		t.Errorf("returned volume = %q, must not reuse the missing recovered name", recovered)
	}
}

// TestResolveHomeVolume_RecoversWhenRecordedVolumeGone pins the case where
// state exists but its recorded volume vanished while the VM still mounts a
// different, existing home volume: the VM's volume is adopted.
func TestResolveHomeVolume_RecoversWhenRecordedVolumeGone(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const recorded = "agents-sandbox-home-drifted-20250101T000000"
	const recovered = "agents-sandbox-home-drifted-20260101T000000"
	key := state.Key{Slug: "drifted", Agent: "opencode"}
	seedHomeState(key, state.HomeState{HomeVolume: recorded, ImageDigest: "sha256:old"})

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		if name == recovered {
			return msb.MockVolumeHandle{Name_: name}, nil
		}
		var vh msb.VolumeHandle
		return vh, volumeNotFoundError(name)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
		t.Fatalf("CreateVolume must not be called when the live volume can be recovered (got %q)", name)
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	ui := &termio.Mock{}
	vm := NewManager(docker.Get())
	volName, _, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		key,
		image.ImageInfo{Digest: "sha256:new"},
		false,
		state.Fingerprints{},
		recovered,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if volName != recovered {
		t.Errorf("volume = %q, want recovered %q", volName, recovered)
	}
}

// TestResolveHomeVolume_RecoveryStateFails pins the branch where the recovery
// transaction cannot run (e.g. a directory sits at the state path, so the state
// read fails): the volume is still returned and a warning is emitted.
func TestResolveHomeVolume_RecoveryStateFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const recovered = "agents-sandbox-home-writefail-20260101T000000"
	key := state.Key{Slug: "writefail", Agent: "opencode"}
	statePath := filepath.Join(configpaths.Get().UserStateDir(), "writefail", "opencode", "state.yaml")
	if err := os.MkdirAll(statePath, 0o700); err != nil {
		t.Fatal(err)
	}

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		if name == recovered {
			return msb.MockVolumeHandle{Name_: name}, nil
		}
		var vh msb.VolumeHandle
		return vh, volumeNotFoundError(name)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
		t.Fatalf("CreateVolume must not be called during recovery (got %q)", name)
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	ui := &termio.Mock{}
	vm := NewManager(docker.Get())
	volName, hs, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		key,
		image.ImageInfo{Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		recovered,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if volName != recovered {
		t.Errorf("volume = %q, want recovered %q", volName, recovered)
	}
	if hs.HomeVolume != recovered || hs.ImageDigest != "sha256:def" {
		t.Errorf("recovered state = %+v, want volume %q and digest sha256:def", hs, recovered)
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, " "), "failed to recover state file") {
		t.Errorf("expected a state-recovery warning, got %v", ui.WarnCalls)
	}
}

// TestResolveHomeVolume_StateWinsOverRecoveredVolume pins the precedence rule:
// when state resolves to an existing volume, the live VM's volume is never
// consulted, even if it differs.
func TestResolveHomeVolume_StateWinsOverRecoveredVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const stateVol = "agents-sandbox-home-precedence-20250101T000000"
	const liveVol = "agents-sandbox-home-precedence-20260101T000000"
	key := state.Key{Slug: "precedence", Agent: "opencode"}
	seedHomeState(key, state.HomeState{HomeVolume: stateVol, ImageDigest: "sha256:abc"})

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		if name != stateVol {
			t.Errorf("GetVolume called with %q; the live volume must not be consulted when state resolves", name)
		}
		return msb.MockVolumeHandle{Name_: name}, nil
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
		t.Fatalf("CreateVolume must not be called (got %q)", name)
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	ui := &termio.Mock{}
	vm := NewManager(docker.Get())
	volName, _, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		key,
		image.ImageInfo{Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		liveVol,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if volName != stateVol {
		t.Errorf("volume = %q, want state volume %q", volName, stateVol)
	}
}

// TestResolveHomeVolume_RecoveryPreservesFingerprints pins that recovering a
// live volume from a still-readable prior state carries the env/secret/network/
// mount fingerprints forward, so the recovered state does not itself trigger a
// spurious rebuild.
func TestResolveHomeVolume_RecoveryPreservesFingerprints(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const recorded = "agents-sandbox-home-fp-20250101T000000"
	const recovered = "agents-sandbox-home-fp-20260101T000000"
	key := state.Key{Slug: "fp", Agent: "opencode"}
	seedHomeState(key, state.HomeState{
		HomeVolume:      recorded,
		ImageDigest:     "sha256:old",
		AgentVersion:    "1.2.3",
		AgentSource:     "tool",
		DockerSource:    "user",
		OfferedVersions: []string{"2.0.0"},
		EnvState:        state.EnvState{Hash: "envhash", Entries: []string{"A"}},
		SecretState:     state.SecretState{Hash: "secrethash", Entries: []string{"B"}},
		NetworkState:    state.NetworkState{Hash: "nethash", Entries: []string{"example.com"}},
		MountState:      state.MountState{Hash: "mountHash", Entries: []string{"/x"}},
	})

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		if name == recovered {
			return msb.MockVolumeHandle{Name_: name}, nil
		}
		var vh msb.VolumeHandle
		return vh, volumeNotFoundError(name)
	}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
		t.Fatalf("CreateVolume must not be called during recovery (got %q)", name)
		return msb.MockVolumeHandle{Name_: name}, nil
	}

	ui := &termio.Mock{}
	vm := NewManager(docker.Get())
	volName, hs, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		key,
		image.ImageInfo{Digest: "sha256:new"},
		false,
		state.Fingerprints{},
		recovered,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if volName != recovered {
		t.Errorf("volume = %q, want recovered %q", volName, recovered)
	}
	if hs.EnvState.Hash != "envhash" || hs.NetworkState.Hash != "nethash" ||
		hs.SecretState.Hash != "secrethash" || hs.MountState.Hash != "mountHash" {
		t.Errorf("fingerprints not preserved: %+v", hs)
	}
	if hs.AgentVersion != "1.2.3" || hs.AgentSource != "tool" || hs.DockerSource != "user" ||
		len(hs.OfferedVersions) != 1 || hs.OfferedVersions[0] != "2.0.0" {
		t.Errorf("upgrade fields not preserved: %+v", hs)
	}
	if hs.ImageDigest != "sha256:new" {
		t.Errorf("digest = %q, want %q", hs.ImageDigest, "sha256:new")
	}
	persisted, err := state.ReadState(key)
	if err != nil {
		t.Fatalf("state not written: %v", err)
	}
	if persisted.NetworkState.Hash != "nethash" {
		t.Errorf("persisted network fingerprint = %q, want %q", persisted.NetworkState.Hash, "nethash")
	}
}

func TestEnsureNewHome_PrefillFails_RemovesVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	mock := &msb.MockMsbClient{}
	mock.CreateSandboxErr = errors.New("prefill sandbox creation failed")
	var createdVolume string
	mock.CreateVolumeFn = func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
		createdVolume = name
		return &msb.MockVolumeHandle{Name_: name}, nil
	}
	vm := NewManager(docker.Get())

	_, _, err := vm.EnsureNewHome(
		context.Background(),
		mock,
		state.Key{Slug: "testproj", Agent: "opencode"},
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:abc"},
		false,
		state.Fingerprints{},
		"",
		&termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "prefill") {
		t.Errorf("unexpected error: %v", err)
	}
	if len(mock.RemovedVolumes) != 1 || mock.RemovedVolumes[0] != createdVolume {
		t.Errorf("expected created volume %q to be removed, got %v", createdVolume, mock.RemovedVolumes)
	}
}

// TestEnsureNewHome_StateWriteFails pins #428: the state write is the record
// of the volume a run is about to use, so a failed write must surface an error
// and remove the uncommitted volume rather than warn and report success. A
// post-rename durability failure is the exception — the installed state already
// names the volume, so it must be kept.
func TestEnsureNewHome_StateWriteFails(t *testing.T) {
	tests := []struct {
		name       string
		writeErr   error
		wantRemove bool
	}{
		{
			name:       "pre-rename failure removes the uncommitted volume",
			writeErr:   errors.New("write state file: rename boom"),
			wantRemove: true,
		},
		{
			name:       "installed write keeps the volume",
			writeErr:   fmt.Errorf("write state file: %w: sync dir boom", atomicwrite.ErrInstalled),
			wantRemove: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			restore := recoverHomeState
			recoverHomeState = func(_ state.Key, _ func(*state.HomeState, bool) (bool, error)) error {
				return tt.writeErr
			}
			t.Cleanup(func() { recoverHomeState = restore })

			docker.WithNoopDockerMock(t)

			var createdVolume string
			mock := &msb.MockMsbClient{
				CreatedSandbox: &msb.MockSandbox{Name_: "prefill"},
				CreateVolumeFn: func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
					createdVolume = name
					return &msb.MockVolumeHandle{Name_: name}, nil
				},
			}
			ui := &termio.Mock{}

			_, _, err := NewManager(docker.Get()).EnsureNewHome(
				context.Background(),
				mock,
				state.Key{Slug: "statewritefail", Agent: "opencode"},
				image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:abc"},
				false, // the prefill succeeds; the state write is the failure under test
				state.Fingerprints{},
				"",
				ui,
			)
			if err == nil {
				t.Fatal("expected the state-write error to surface, got nil")
			}
			if !errors.Is(err, tt.writeErr) {
				t.Errorf("err = %v, want it to carry %v", err, tt.writeErr)
			}
			removed := len(mock.RemovedVolumes) == 1
			if removed != tt.wantRemove {
				t.Errorf("removed = %v (RemovedVolumes %v), want %v", removed, mock.RemovedVolumes, tt.wantRemove)
			}
			if tt.wantRemove && mock.RemovedVolumes[0] != createdVolume {
				t.Errorf("removed volume = %q, want the created volume %q", mock.RemovedVolumes[0], createdVolume)
			}
		})
	}
}

func TestEnsureNewHome_PrefillCancelled_RemovesVolumeWithLiveContext(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	ctx, cancel := context.WithCancel(context.Background())

	mock := &msb.MockMsbClient{}
	mock.CreateVolumeFn = func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
		return &msb.MockVolumeHandle{Name_: name}, nil
	}
	mock.CreateSandboxFn = func(_ context.Context, _ string, _ msb.SandboxSpec) (msb.Sandbox, error) {
		cancel()
		return nil, errors.New("prefill sandbox creation failed")
	}
	var cleanupCtxErr error
	mock.RemoveVolumeFn = func(cleanupCtx context.Context, _ string) error {
		cleanupCtxErr = cleanupCtx.Err()
		return nil
	}
	vm := NewManager(docker.Get())

	_, _, err := vm.EnsureNewHome(
		ctx,
		mock,
		state.Key{Slug: "testproj", Agent: "opencode"},
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:abc"},
		false,
		state.Fingerprints{},
		"",
		&termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if ctx.Err() == nil {
		t.Fatal("expected the caller context to be cancelled during prefill")
	}
	if len(mock.RemovedVolumes) != 1 {
		t.Fatalf("expected the created volume to be removed exactly once, got %v", mock.RemovedVolumes)
	}
	if cleanupCtxErr != nil {
		t.Errorf("cleanup received a cancelled context (%v); want a fresh live context", cleanupCtxErr)
	}
}

func TestEnsureNewHome_SkipVM_NoSandbox(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := &msb.MockMsbClient{}
	vm := NewManager(docker.Get())

	volName, _, err := vm.EnsureNewHome(
		context.Background(),
		mock,
		state.Key{Slug: "testproj", Agent: "opencode"},
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:abc"},
		true, // skipVM
		state.Fingerprints{},
		"",
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(volName, "testproj-") {
		t.Errorf("expected home volume prefix, got %q", volName)
	}
	if len(mock.CreatedSandboxes) != 0 {
		t.Errorf("expected no sandboxes in dry-run-vm, got %d", len(mock.CreatedSandboxes))
	}
}

// TestResolveHomeVolume_SkipVMThenRealRun_Prefills is the #429 scenario: a
// --dry-run-vm run on a new project followed by a real run must prefill the
// home volume on the real run and only then record it.
func TestResolveHomeVolume_SkipVMThenRealRun_Prefills(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	var createdVolumes []string
	mock := &msb.MockMsbClient{
		CreatedSandbox: &msb.MockSandbox{Name_: "prefill"},
		CreateVolumeFn: func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
			createdVolumes = append(createdVolumes, name)
			return &msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	k := state.Key{Slug: "testproj", Agent: "opencode"}
	info := image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:abc"}
	ui := &termio.Mock{}
	vm := NewManager(docker.Get())

	if _, _, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		k,
		info,
		true,
		state.Fingerprints{},
		"",
		ui,
	); err != nil {
		t.Fatalf("skip-VM run: %v", err)
	}
	volName, _, err := vm.ResolveHomeVolume(context.Background(), mock, k, info, false, state.Fingerprints{}, "", ui)
	if err != nil {
		t.Fatalf("real run: %v", err)
	}

	if len(createdVolumes) != 1 || createdVolumes[0] != volName {
		t.Errorf("created volumes = %v, want exactly %q created by the real run", createdVolumes, volName)
	}
	if len(mock.CreatedSandboxes) != 1 {
		t.Errorf("prefill sandboxes = %d, want 1 (the real run must prefill)", len(mock.CreatedSandboxes))
	}
	onDisk, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if onDisk.HomeVolume != volName {
		t.Errorf("state HomeVolume = %q, want %q", onDisk.HomeVolume, volName)
	}
}

// TestResolveHomeVolume_ClearsAdoptionClaim pins pruning the ownership record:
// once recovery folds the preserved volume into the project's own state, the
// claim that guarded it is spent and must be retired.
func TestResolveHomeVolume_ClearsAdoptionClaim(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const recovered = "agents-sandbox-home-widget-aaa-opencode-9"
	key := state.Key{Slug: "adoptedwidget", Agent: "opencode"}
	seedHomeState(key, state.HomeState{RecoveredHomeVolume: recovered})
	seedAdoptionClaim(t, "widget-aaa", key.Slug, recovered)

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		if name == recovered {
			return msb.MockVolumeHandle{Name_: name}, nil
		}
		var vh msb.VolumeHandle
		return vh, volumeNotFoundError(name)
	}

	ui := &termio.Mock{}
	vm := NewManager(docker.Get())
	if _, _, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		key,
		image.ImageInfo{Digest: "sha256:new"},
		false,
		state.Fingerprints{},
		"",
		ui,
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	root, err := state.ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	if _, ok := root.AdoptedHomeVolume("widget-aaa", "opencode"); ok {
		t.Errorf("adoption claim survived recovery: %+v", root.AdoptedHomeVolumes)
	}
}

// TestResolveHomeVolume_AdoptionClaimClearErrorWarns pins the best-effort
// clear: a spent claim that cannot be retired (an unreadable root state document)
// only warns, because the recovery itself already succeeded.
func TestResolveHomeVolume_AdoptionClaimClearErrorWarns(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const recovered = "agents-sandbox-home-widget-aaa-opencode-9"
	key := state.Key{Slug: "adoptedwidget", Agent: "opencode"}
	seedHomeState(key, state.HomeState{RecoveredHomeVolume: recovered})
	seedAdoptionClaim(t, "widget-aaa", key.Slug, recovered)

	// Corrupt the root state document after seeding the claim, so clearing the
	// claim's transaction fails to read it.
	rootStatePath := filepath.Join(configpaths.Get().UserStateDir(), "state.yaml")
	if err := os.WriteFile(rootStatePath, []byte("{ corrupt: yaml: ["), 0o600); err != nil {
		t.Fatal(err)
	}

	mock := &msb.MockMsbClient{}
	mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		if name == recovered {
			return msb.MockVolumeHandle{Name_: name}, nil
		}
		var vh msb.VolumeHandle
		return vh, volumeNotFoundError(name)
	}

	ui := &termio.Mock{}
	vm := NewManager(docker.Get())
	if _, _, err := vm.ResolveHomeVolume(
		context.Background(),
		mock,
		key,
		image.ImageInfo{Digest: "sha256:new"},
		false,
		state.Fingerprints{},
		"",
		ui,
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(strings.Join(ui.WarnCalls, "\n"), "failed to clear the adoption claim") {
		t.Errorf("expected a warning about the unretired adoption claim, got %v", ui.WarnCalls)
	}
}

// TestConfirmedRecoveredVolume pins the three outcomes that drive recovery:
// an empty candidate and a genuinely absent volume report "not confirmed" with
// no error (fall back to a fresh prefill), a present volume is confirmed, and
// any other lookup error is returned so the caller fails closed.
func TestConfirmedRecoveredVolume(t *testing.T) {
	const present = "agents-sandbox-home-confirm-20260101T000000"

	tests := []struct {
		name      string
		candidate string
		getErr    error
		wantName  string
		wantOK    bool
		wantErr   bool
	}{
		{"empty candidate", "", nil, "", false, false},
		{"present", present, nil, present, true, false},
		{"absent", present, volumeNotFoundError(present), "", false, false},
		{"transient", present, errors.New("daemon unavailable"), "", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &msb.MockMsbClient{}
			mock.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
				if tt.getErr != nil {
					var vh msb.VolumeHandle
					return vh, tt.getErr
				}
				return msb.MockVolumeHandle{Name_: name}, nil
			}

			name, ok, err := confirmedRecoveredVolume(context.Background(), mock, tt.candidate)
			if name != tt.wantName || ok != tt.wantOK {
				t.Errorf("confirmedRecoveredVolume() = (%q, %v), want (%q, %v)", name, ok, tt.wantName, tt.wantOK)
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("confirmedRecoveredVolume() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

var raceTestImage = image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:abc"}

// commitConcurrentHome records volume as the project's home, the way a racing
// session's EnsureNewHome commits its fresh volume.
func commitConcurrentHome(t *testing.T, k state.Key, volume string) {
	t.Helper()
	require.NoError(t, seedHomeState(k, state.NewHomeState(volume, "sha256:winner")))
}

// withFakeHomeVolumeClock freezes the clock HomeVolumeName timestamps from and
// replaces the sleep between colliding names with a clock advance, so
// collision tests do not sleep. It returns the number of waits.
func withFakeHomeVolumeClock(t *testing.T) *int {
	t.Helper()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	waits := 0
	restoreClock, restoreSleep := homeVolumeClock, sleepForNextHomeVolumeName
	homeVolumeClock = func() time.Time { return now }
	sleepForNextHomeVolumeName = func(duration time.Duration) {
		waits++
		now = now.Add(duration)
	}
	t.Cleanup(func() { homeVolumeClock, sleepForNextHomeVolumeName = restoreClock, restoreSleep })
	return &waits
}

// errVolumeExists is the SDK error CreateVolume returns for a taken name.
var errVolumeExists = &msbSdk.Error{Kind: msbSdk.ErrVolumeAlreadyExists, Message: "exists"}
