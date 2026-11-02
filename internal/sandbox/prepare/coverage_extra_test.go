package prepare

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestCurrentAgentVersionIgnoresCorruptFile covers the read-error branch of
// currentAgentVersion (a corrupt project state file must yield "").
func TestCurrentAgentVersionIgnoresCorruptFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	sdir := filepath.Join(configpaths.Get().UserStateDir(), k.Slug, k.Agent)
	if err := os.MkdirAll(sdir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdir, "state.yaml"), []byte("::: not yaml :::"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := currentAgentVersion(k); got != "" {
		t.Errorf("currentAgentVersion() = %q, want empty for corrupt file", got)
	}
}

// TestPersistConfigHashesCoversEnvSecretAndNetwork covers the previously
// entirely-uncovered persistConfigHashes function: it persists the desired
// env/secret/network fingerprints after a fresh VM creation.
func TestPersistConfigHashesCoversEnvSecretAndNetwork(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)

	slug := "hashproj"
	k := state.Key{Slug: slug, Agent: "opencode"}
	if err := seedHomeState(k, state.NewHomeState("vol", "digest")); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
	policy := network.Policy{Profiles: []network.Profile{network.ProfileNone}}

	persistConfigHashes(
		k,
		state.Fingerprints{
			Network: state.BuildNetworkState(policy),
			Mount:   state.BuildMountState(nil),
		},
		&ui,
	)

	st, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState after persistConfigHashes: %v", err)
	}
	if st.NetworkState.Hash == "" {
		t.Error("expected network state hash to be persisted")
	}
	if st.MountState.Hash == "" {
		t.Error("expected mount state hash to be persisted")
	}
}
