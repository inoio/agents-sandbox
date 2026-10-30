package volume

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

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

// TestEnsureNewHome_ConcurrentCommitAdoptsWinner covers #431: when another
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
				CreateVolumeFn: func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
					createdVolume = name
					commitConcurrentHome(t, k, "winner-vol")
					return msb.MockVolumeHandle{Name_: name}, nil
				},
			}
			ui := &termio.Mock{}

			volName, hs, err := NewManager(ui).EnsureNewHome(
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
		CreateVolumeFn: func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			return msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	ui := &termio.Mock{}

	volName, _, err := NewManager(ui).EnsureNewHome(
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
		CreateVolumeFn: func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			require.NoError(t, seedHomeState(k, state.HomeState{RecoveredHomeVolume: "legacy-vol"}))
			return msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	ui := &termio.Mock{}

	volName, _, err := NewManager(ui).EnsureNewHome(
		context.Background(), client, k, raceTestImage, false, state.Fingerprints{}, "gone-vol", ui,
	)

	require.NoError(t, err)
	require.Empty(t, client.RemovedVolumes)
	persisted, err := state.ReadState(k)
	require.NoError(t, err)
	require.Equal(t, volName, persisted.HomeVolume)
}

// TestResolveHomeVolume_WinnerCommitsBeforeCreation covers a winner that
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
		CreateVolumeFn: func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			createdVolume = name
			return msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	ui := &termio.Mock{}

	volName, _, err := NewManager(ui).ResolveHomeVolume(
		context.Background(), client, k, raceTestImage, false, state.Fingerprints{}, "", ui,
	)

	require.NoError(t, err)
	require.Equal(t, "winner-vol", volName)
	require.Equal(t, []string{createdVolume}, client.RemovedVolumes)
	persisted, err := state.ReadState(k)
	require.NoError(t, err)
	require.Equal(t, "winner-vol", persisted.HomeVolume)
}

// TestEnsureNewHome_NameCollisionRetries covers the same-second case of #431:
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
		CreateVolumeFn: func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			attemptedNames = append(attemptedNames, name)
			if name == takenName {
				return nil, errVolumeExists
			}
			return msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	ui := &termio.Mock{}

	volName, _, err := NewManager(ui).EnsureNewHome(
		context.Background(), client, k, raceTestImage, false, state.Fingerprints{}, "", ui,
	)

	require.NoError(t, err)
	require.NotEqual(t, takenName, volName)
	require.Equal(t, []string{takenName, volName}, attemptedNames)
	require.Equal(t, 1, *waits)
}

func TestEnsureNewHome_NameCollisionGivesUp(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	waits := withFakeHomeVolumeClock(t)
	k := state.Key{Slug: "collideproj", Agent: "opencode"}
	attempts := 0
	client := &msb.MockMsbClient{
		CreatedSandbox: &msb.MockSandbox{Name_: "prefill"},
		CreateVolumeFn: func(_ context.Context, _ string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			attempts++
			return nil, errVolumeExists
		},
	}
	ui := &termio.Mock{}

	_, _, err := NewManager(ui).EnsureNewHome(
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
		CreateVolumeFn: func(_ context.Context, _ string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			return nil, createErr
		},
	}
	ui := &termio.Mock{}

	_, _, err := NewManager(ui).EnsureNewHome(
		context.Background(), client, state.Key{Slug: "failproj", Agent: "opencode"},
		raceTestImage, false, state.Fingerprints{}, "", ui,
	)

	require.ErrorIs(t, err, createErr)
	require.Zero(t, *waits)
}
