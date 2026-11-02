package prepare

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestPersistNetworkStateReadError covers the non-not-found ReadState error
// branch of persistFingerprints (a corrupt state file).
func TestPersistFingerprintsReadError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "corruptnetproj"
	sdir := filepath.Join(configpaths.Get().UserStateDir(), slug, "opencode")
	if err := os.MkdirAll(sdir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, sdir, "state.yaml", "{ corrupted: yaml: [")

	err := persistFingerprints(
		state.Key{Slug: slug, Agent: "opencode"},
		state.Fingerprints{
			Network: state.BuildNetworkState(network.Policy{Profiles: []network.Profile{network.ProfileNone}}),
		},
	)
	if err == nil {
		t.Fatal("expected error for corrupted state YAML")
	}
}

// TestCurrentAgentVersionReadError covers the read-error branch of
// currentAgentVersion: an unreadable state file yields "".
func TestCurrentAgentVersionReadError(t *testing.T) {
	withFailingStateDir(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if got := currentAgentVersion(k); got != "" {
		t.Errorf("currentAgentVersion() = %q, want empty when the state cannot be read", got)
	}
}

// TestPersistConfigHashesWarnsOnError covers the warning branches of
// persistConfigHashes when the env/secret/network fingerprints cannot be
// persisted (a corrupt state file makes the write fail).
func TestPersistConfigHashesWarnsOnError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)

	slug := "corrupthashproj"
	sdir := filepath.Join(configpaths.Get().UserStateDir(), slug, "opencode")
	if err := os.MkdirAll(sdir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, sdir, "state.yaml", "{ corrupted: yaml: [")

	persistConfigHashes(
		state.Key{Slug: slug, Agent: "opencode"},
		state.Fingerprints{
			Network: state.BuildNetworkState(network.Policy{Profiles: []network.Profile{network.ProfileNone}}),
		},
		&ui,
	)

	if len(ui.WarnCalls) != 1 || !contains(ui.WarnCalls[0], "persisting config fingerprints") {
		t.Errorf("expected one warning about persisting config fingerprints, got %v", ui.WarnCalls)
	}
}
