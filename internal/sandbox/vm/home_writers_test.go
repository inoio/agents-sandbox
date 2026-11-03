package vm

import (
	"context"
	"reflect"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestHomeWritersPreserveUnrelatedFields is a property guard over the boot
// provenance HomeState writer: each writer owns a small set of fields and must leave every
// other field of the existing document untouched. Each case seeds a fully
// populated state, runs one writer, and compares the result against the seed
// with only the writer's owned fields advanced.
func TestHomeWritersPreserveUnrelatedFields(t *testing.T) {
	seed := func() state.HomeState {
		return state.HomeState{
			HomeVolume:      "vol",
			ImageDigest:     "d",
			AgentVersion:    "1.0.0",
			AgentSource:     state.AgentSourceTool,
			DockerSource:    state.AgentSourceTool,
			OfferedVersions: []string{"1.1.0"},
			EnvState:        state.EnvState{Hash: "e"},
			SecretState:     state.SecretState{Hash: "s"},
			NetworkState:    state.NetworkState{Hash: "n"},
			MountState:      state.MountState{Hash: "m"},
		}
	}

	cases := []struct {
		name string
		run  func(t *testing.T, k state.Key)
		want func() state.HomeState
	}{
		{
			name: "recordImageProvenance",
			run: func(t *testing.T, k state.Key) {
				t.Helper()
				sb := &msb.MockSandbox{
					FSValue_: msb.NewTestFS(map[string][]byte{
						agentSourcePath:  []byte(state.AgentSourceUser + "\n"),
						dockerSourcePath: []byte(state.AgentSourceUser + "\n"),
					}, nil),
				}
				recordImageProvenance(context.Background(), k, opencodeResolved(t), sb, &termio.Mock{})
			},
			want: func() state.HomeState {
				w := seed()
				w.AgentSource = state.AgentSourceUser
				w.DockerSource = state.AgentSourceUser
				return w
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			k := state.Key{Slug: "proj", Agent: "opencode"}
			if err := seedHomeState(k, seed()); err != nil {
				t.Fatalf("seed home state: %v", err)
			}
			tc.run(t, k)
			got, err := state.ReadState(k)
			if err != nil {
				t.Fatalf("ReadState: %v", err)
			}
			if want := tc.want(); !reflect.DeepEqual(*got, want) {
				t.Errorf("writer changed unrelated fields:\n got %+v\nwant %+v", *got, want)
			}
		})
	}
}
