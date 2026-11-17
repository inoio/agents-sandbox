package readback

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
)

var readbackKey = state.Key{Slug: "myproject", Agent: "opencode"}

// stubInspector is a VolumeInspector that records the names it was asked about
// and answers with paths or errors from its maps.
type stubInspector struct {
	paths  map[string]string
	errs   map[string]error
	probed []string
}

func (s *stubInspector) InspectVolume(_ context.Context, name string) (volume.VolumeDetail, error) {
	s.probed = append(s.probed, name)
	if err := s.errs[name]; err != nil {
		return volume.VolumeDetail{}, err
	}
	return volume.VolumeDetail{Name: name, Path: s.paths[name]}, nil
}

// withInspector installs stub as the package volume seam for the test and
// returns it.
func withInspector(t *testing.T, stub *stubInspector) *stubInspector {
	t.Helper()
	previous := volumeInspector
	volumeInspector = stub
	t.Cleanup(func() { volumeInspector = previous })
	return stub
}

func seedHomeVolume(t *testing.T, k state.Key, volumeName string) {
	t.Helper()
	require.NoError(t, state.WithHomeState(k, func(cur *state.HomeState, _ bool) (bool, error) {
		cur.HomeVolume = volumeName
		return true, nil
	}))
}

func TestHomeVolumePathReturnsTheInspectedPath(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	seedHomeVolume(t, readbackKey, "home-vol")
	stub := withInspector(t, &stubInspector{paths: map[string]string{"home-vol": "/host/vol"}})

	got, err := HomeVolumePath(context.Background(), readbackKey)

	require.NoError(t, err)
	require.Equal(t, "/host/vol", got)
	require.Equal(t, []string{"home-vol"}, stub.probed)
}

func TestHomeVolumePathErrorsWhenStateRecordsNoVolume(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	// A state document that exists but records no home volume (an
	// identity-adoption marker): the key is known, the volume is not.
	require.NoError(t, state.WithHomeState(readbackKey, func(cur *state.HomeState, _ bool) (bool, error) {
		cur.ImageDigest = "sha256:onlydigest"
		return true, nil
	}))
	stub := withInspector(t, &stubInspector{})

	_, err := HomeVolumePath(context.Background(), readbackKey)

	require.Error(t, err)
	require.Contains(t, err.Error(), "home volume")
	require.Contains(t, err.Error(), readbackKey.Slug)
	require.Empty(t, stub.probed, "no volume must be inspected when state records none")
}

func TestHomeVolumePathErrorsWhenStateIsMissing(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	withInspector(t, &stubInspector{})

	// no state file at all
	_, err := HomeVolumePath(context.Background(), state.Key{Slug: "absent", Agent: "opencode"})

	require.Error(t, err)
	require.ErrorIs(t, err, state.ErrStateNotFound)
}

func TestHomeVolumePathWrapsAnInspectFailure(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	seedHomeVolume(t, readbackKey, "gone-vol")
	inspectErr := context.DeadlineExceeded
	withInspector(t, &stubInspector{errs: map[string]error{"gone-vol": inspectErr}})

	_, err := HomeVolumePath(context.Background(), readbackKey)

	require.ErrorIs(t, err, inspectErr)
	require.Contains(t, err.Error(), "gone-vol")
}

func TestHomeVolumePathWrapsAnUnparsableState(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	require.NoError(t, os.MkdirAll(state.KeyDir(readbackKey), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(state.KeyDir(readbackKey), "state.yaml"),
		[]byte(":\tnot: yaml\n"),
		0o600,
	))
	withInspector(t, &stubInspector{})

	_, err := HomeVolumePath(context.Background(), readbackKey)

	require.ErrorIs(t, err, state.ErrStateUnparsable)
	require.Contains(t, err.Error(), readbackKey.Slug)
}

// TestHomeVolumePathThroughProductionInspector drives the default seam through
// a mocked microsandbox client, so the wrapper the production path uses is
// exercised rather than only the injected double.
func TestHomeVolumePathThroughProductionInspector(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	seedHomeVolume(t, readbackKey, "home-vol")
	msbtest.WithMsbMock(t, &msbtest.MockMsbClient{
		Volumes: []msb.VolumeHandle{
			&msbtest.MockVolumeHandle{Name_: "home-vol", Path_: "/host/vol"},
		},
	})

	got, err := HomeVolumePath(context.Background(), readbackKey)

	require.NoError(t, err)
	require.Equal(t, "/host/vol", got)
}
