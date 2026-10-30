package volume

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

func TestHomeVolumeNameIsPrefixFree(t *testing.T) {
	got := HomeVolumeName(state.Key{Slug: "myproj-1mjusbm3wikhb0", Agent: "opencode"})
	require.NotContains(t, got, naming.LegacyHomePrefix)
	require.Regexp(t, `^myproj-1mjusbm3wikhb0-opencode-\d{8}T\d{6}$`, got)
}

func TestListVolumesShowsBothSchemes(t *testing.T) {
	mock := &msb.MockMsbClient{Volumes: []msb.VolumeHandle{
		&msb.MockVolumeHandle{Name_: "agents-sandbox-home-myproj-1mjusbm3wikhb0-opencode-20260812T123456"},
		&msb.MockVolumeHandle{Name_: "myproj-1mjusbm3wikhb0-opencode-20260812T123457"},
		&msb.MockVolumeHandle{Name_: "some-other-volume"},
	}}
	msb.WithMsbMock(t, mock)

	got, err := ListVolumes(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "myproj-1mjusbm3wikhb0-opencode-20260812T123457", got[1].Name)
}
