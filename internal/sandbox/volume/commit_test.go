package volume

import (
	"reflect"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

func priorHomeStateWithFingerprints(homeVolume string) state.HomeState {
	return state.HomeState{
		HomeVolume:          homeVolume,
		ImageDigest:         "sha256:old",
		AgentVersion:        "1.2.3",
		OfferedVersions:     []string{"1.2.4"},
		EnvState:            state.EnvState{Hash: "env"},
		SecretState:         state.SecretState{Hash: "secret"},
		NetworkState:        state.NetworkState{Hash: "network"},
		MountState:          state.MountState{Hash: "mount"},
		RecoveredHomeVolume: homeVolume,
	}
}

func readHomeStateForTest(t *testing.T, k state.Key) state.HomeState {
	t.Helper()
	st, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	return *st
}

func TestCommitNewHomeVolumePreservesUnrelatedFields(t *testing.T) {
	for _, clearRecoveredMarker := range []bool{false, true} {
		configpaths.WithMockConfigPaths(t)
		key := state.Key{Slug: "myproj", Agent: "opencode"}
		prior := priorHomeStateWithFingerprints("old-vol")
		seedHomeState(key, prior)

		if err := commitNewHomeVolume(key, "old-vol", "new-vol", "sha256:new", clearRecoveredMarker); err != nil {
			t.Fatalf("commitNewHomeVolume: %v", err)
		}

		want := prior
		want.HomeVolume = "new-vol"
		want.ImageDigest = "sha256:new"
		if clearRecoveredMarker {
			want.RecoveredHomeVolume = ""
		}
		if got := readHomeStateForTest(t, key); !reflect.DeepEqual(got, want) {
			t.Errorf("clearRecoveredMarker=%v: state = %+v, want %+v", clearRecoveredMarker, got, want)
		}
	}
}

func TestCommitNewHomeVolumeAbortsWhenHomeVolumeChanged(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	key := state.Key{Slug: "myproj", Agent: "opencode"}
	prior := priorHomeStateWithFingerprints("other-vol")
	seedHomeState(key, prior)

	err := commitNewHomeVolume(key, "old-vol", "new-vol", "sha256:new", true)
	if err == nil || !strings.Contains(err.Error(), "home volume changed concurrently") {
		t.Fatalf("err = %v, want concurrent-change error", err)
	}
	if got := readHomeStateForTest(t, key); !reflect.DeepEqual(got, prior) {
		t.Errorf("state = %+v, want unchanged %+v", got, prior)
	}
}
