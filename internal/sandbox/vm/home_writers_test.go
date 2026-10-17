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

// TestHomeWritersPreserveUnrelatedFields is a property guard over every vm
// HomeState writer: each writer owns a small set of fields and must leave every
// other field of the existing document untouched. Each case seeds a fully
// populated state, runs one writer, and compares the result against the seed
// with only the writer's owned fields advanced.
func TestHomeWritersPreserveUnrelatedFields(t *testing.T) {
	seed := func() state.HomeState {
		return state.HomeState{
			HomeVolume:      "vol",
			ImageDigest:     "d",
			AgentVersion:    "1.0.0",
			AgentSource:     agentSourceTool,
			DockerSource:    agentSourceTool,
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
			name: "persistEnvSecrets",
			run: func(t *testing.T, k state.Key) {
				t.Helper()
				if err := persistEnvSecrets(k, state.EnvState{Hash: "e2"}, state.SecretState{Hash: "s2"}); err != nil {
					t.Fatalf("persistEnvSecrets: %v", err)
				}
			},
			want: func() state.HomeState {
				w := seed()
				w.EnvState = state.EnvState{Hash: "e2"}
				w.SecretState = state.SecretState{Hash: "s2"}
				return w
			},
		},
		{
			name: "persistNetworkState",
			run: func(t *testing.T, k state.Key) {
				t.Helper()
				if err := persistNetworkState(k, state.NetworkState{Hash: "n2"}); err != nil {
					t.Fatalf("persistNetworkState: %v", err)
				}
			},
			want: func() state.HomeState {
				w := seed()
				w.NetworkState = state.NetworkState{Hash: "n2"}
				return w
			},
		},
		{
			name: "persistMountState",
			run: func(t *testing.T, k state.Key) {
				t.Helper()
				if err := persistMountState(k, state.MountState{Hash: "m2"}); err != nil {
					t.Fatalf("persistMountState: %v", err)
				}
			},
			want: func() state.HomeState {
				w := seed()
				w.MountState = state.MountState{Hash: "m2"}
				return w
			},
		},
		{
			name: "persistOfferedVersion",
			run: func(t *testing.T, k state.Key) {
				t.Helper()
				persistOfferedVersion(k, "2.0.0", &termio.Mock{})
			},
			want: func() state.HomeState {
				w := seed()
				w.OfferedVersions = []string{"1.1.0", "2.0.0"}
				return w
			},
		},
		{
			name: "recoverAgentVersion",
			run: func(t *testing.T, k state.Key) {
				t.Helper()
				orig := existingAgentVersion
				existingAgentVersion = func(context.Context, string, string) (string, bool) { return "1.2.3", true }
				t.Cleanup(func() { existingAgentVersion = orig })
				if got := recoverAgentVersion(context.Background(), k, &termio.Mock{}); got != "1.2.3" {
					t.Fatalf("recoverAgentVersion = %q, want 1.2.3", got)
				}
			},
			want: func() state.HomeState {
				w := seed()
				w.AgentVersion = "1.2.3"
				return w
			},
		},
		{
			name: "recordImageProvenance",
			run: func(t *testing.T, k state.Key) {
				t.Helper()
				sb := &msb.MockSandbox{
					FSValue_: msb.NewTestFS(map[string][]byte{
						agentSourcePath:  []byte(agentSourceUser + "\n"),
						dockerSourcePath: []byte(agentSourceUser + "\n"),
					}, nil),
				}
				recordImageProvenance(context.Background(), k, opencodeAgent(t), sb, &termio.Mock{})
			},
			want: func() state.HomeState {
				w := seed()
				w.AgentSource = agentSourceUser
				w.DockerSource = agentSourceUser
				return w
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			k := state.Key{Slug: "proj", Agent: "opencode"}
			if err := seedHomeState(k, seed()); err != nil {
				t.Fatalf("seed seedHomeState: %v", err)
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
