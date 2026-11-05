package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// recordingImageOperation returns an image operation that records the key it
// was called with and returns the given result.
func recordingImageOperation(
	info image.ImageInfo,
	err error,
) (projectImageOperation, *[]state.Key) {
	var calls []state.Key
	operation := func(_ context.Context, key state.Key) (image.ImageInfo, error) {
		calls = append(calls, key)
		return info, err
	}
	return operation, &calls
}

func setupProjectImageFixture(t *testing.T) (configpaths.ConfigPaths, state.Key) {
	t.Helper()
	initTestProjectDir(t)
	configpaths.WithMockConfigPaths(t)
	checkoutKey := state.Key{Slug: git.MustCheckoutSlug(), Agent: defaultAgentName}
	require.NoError(t, seedHomeState(checkoutKey, state.HomeState{HomeVolume: "home-vol"}))
	return configpaths.Get(), checkoutKey
}

func TestAdoptAndPrepareImageRunsTheOperationForTheAdoptedKeyAndRecordsItsVersion(t *testing.T) {
	paths, checkoutKey := setupProjectImageFixture(t)
	operation, calls := recordingImageOperation(image.ImageInfo{AgentVersion: "4.5.6"}, nil)

	key, info, err := adoptAndPrepareImage(
		context.Background(), paths, &msb.MockMsbClient{}, defaultAgentName, false, &termio.Mock{}, operation,
	)

	require.NoError(t, err)
	require.Equal(t, checkoutKey, key)
	require.Equal(t, "4.5.6", info.AgentVersion)
	require.Equal(t, []state.Key{checkoutKey}, *calls)
	recorded, err := state.ReadState(checkoutKey)
	require.NoError(t, err)
	require.Equal(t, "4.5.6", recorded.AgentVersion)
}

func TestAdoptAndPrepareImageDryRunSkipsTheOperation(t *testing.T) {
	paths, checkoutKey := setupProjectImageFixture(t)
	operation, calls := recordingImageOperation(image.ImageInfo{AgentVersion: "4.5.6"}, nil)

	key, info, err := adoptAndPrepareImage(
		context.Background(), paths, &msb.MockMsbClient{}, defaultAgentName, true, &termio.Mock{}, operation,
	)

	require.NoError(t, err)
	require.Equal(t, checkoutKey, key)
	require.Equal(t, image.ImageInfo{}, info)
	require.Empty(t, *calls)
	recorded, err := state.ReadState(checkoutKey)
	require.NoError(t, err)
	require.Empty(t, recorded.AgentVersion)
}

func TestAdoptAndPrepareImageReturnsTheOperationErrorWithoutRecording(t *testing.T) {
	paths, checkoutKey := setupProjectImageFixture(t)
	operationErr := errors.New("image failed")
	operation, _ := recordingImageOperation(image.ImageInfo{AgentVersion: "4.5.6"}, operationErr)

	_, _, err := adoptAndPrepareImage(
		context.Background(), paths, &msb.MockMsbClient{}, defaultAgentName, false, &termio.Mock{}, operation,
	)

	require.ErrorIs(t, err, operationErr)
	recorded, err := state.ReadState(checkoutKey)
	require.NoError(t, err)
	require.Empty(t, recorded.AgentVersion)
}

func TestAdoptAndPrepareImageWarnsWhenRecordingTheVersionFails(t *testing.T) {
	initTestProjectDir(t)
	configpaths.WithMockConfigPaths(t)
	checkoutKey := state.Key{Slug: git.MustCheckoutSlug(), Agent: defaultAgentName}
	// A directory at the state file's path makes the version write fail.
	require.NoError(t, os.MkdirAll(filepath.Join(state.KeyDir(checkoutKey), "state.yaml"), 0o700))
	operation, _ := recordingImageOperation(image.ImageInfo{AgentVersion: "4.5.6"}, nil)
	ui := &termio.Mock{}

	_, info, err := adoptAndPrepareImage(
		context.Background(), configpaths.Get(), &msb.MockMsbClient{}, defaultAgentName, false, ui, operation,
	)

	require.NoError(t, err)
	require.Equal(t, "4.5.6", info.AgentVersion)
	assertWarnContains(t, ui, "could not persist the agent version")
}

func TestAdoptAndPrepareImageFailsClosedOnAnAdoptionError(t *testing.T) {
	clone := newOriginClone(t)
	identity := mustIdentity(t, clone)
	t.Chdir(clone)
	configpaths.WithMockConfigPaths(t)
	writeUnparsableLegacyState(t, state.Key{Slug: identity.RepoSlug, Agent: defaultAgentName})
	operation, calls := recordingImageOperation(image.ImageInfo{}, nil)

	_, _, err := adoptAndPrepareImage(
		context.Background(), configpaths.Get(), &msb.MockMsbClient{}, defaultAgentName, false, &termio.Mock{},
		operation,
	)

	require.ErrorContains(t, err, "adopt legacy identity")
	require.Error(t, errors.Unwrap(err))
	require.Empty(t, *calls)
}

func TestAdoptAndPrepareImageFailsOnAnUnresolvableIdentity(t *testing.T) {
	t.Chdir(unresolvableIdentityDir(t))
	configpaths.WithMockConfigPaths(t)
	operation, calls := recordingImageOperation(image.ImageInfo{}, nil)

	_, _, err := adoptAndPrepareImage(
		context.Background(), configpaths.Get(), &msb.MockMsbClient{}, defaultAgentName, false, &termio.Mock{},
		operation,
	)

	require.Error(t, err)
	require.Empty(t, *calls)
}
