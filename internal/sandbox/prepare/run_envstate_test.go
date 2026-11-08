package prepare

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestPersistFingerprintsRecordsEveryFingerprintOnExistingState pins that the
// env, secret, network and mount fingerprints replace the recorded ones, while
// the fields persistFingerprints does not own (home volume, image digest) are
// kept.
func TestPersistFingerprintsRecordsEveryFingerprintOnExistingState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if err := seedHomeState(k, state.HomeState{
		HomeVolume:  "existing-vol",
		ImageDigest: "sha256:existing",
		EnvState:    state.EnvState{Hash: "old-env", Entries: []string{"OLD"}},
	}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
	policy := network.Policy{Profiles: []network.Profile{network.ProfileNone}, EgressAllow: []string{"api.example.com"}}
	configuredMounts := mounts.Mounts{"/home/dev/.m2": {Source: "/host/.m2"}}
	fingerprints := state.Fingerprints{
		Env:     state.EnvState{Hash: "new-env", Entries: []string{"FOO"}},
		Secret:  state.SecretState{Hash: "new-secret", Entries: []string{"PASS"}},
		Network: state.BuildNetworkState(policy),
		Mount:   state.BuildMountState(configuredMounts),
	}

	if err := persistFingerprints(k, fingerprints); err != nil {
		t.Fatalf("persistFingerprints: %v", err)
	}

	got, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.HomeVolume != "existing-vol" || got.ImageDigest != "sha256:existing" {
		t.Errorf("home volume/digest = %q/%q, want the seeded existing-vol/sha256:existing",
			got.HomeVolume, got.ImageDigest)
	}
	if got.EnvState.Hash != "new-env" {
		t.Errorf("EnvState.Hash = %q, want new-env", got.EnvState.Hash)
	}
	if got.SecretState.Hash != "new-secret" {
		t.Errorf("SecretState.Hash = %q, want new-secret", got.SecretState.Hash)
	}
	if got.NetworkState.Hash != fingerprints.Network.Hash {
		t.Errorf("NetworkState.Hash = %q, want %q", got.NetworkState.Hash, fingerprints.Network.Hash)
	}
	if got.MountState.Hash != mounts.Fingerprint(configuredMounts) {
		t.Errorf("MountState.Hash = %q, want %q", got.MountState.Hash, mounts.Fingerprint(configuredMounts))
	}
}

// TestPersistFingerprintsReplacesWithZeroFingerprints pins that an empty
// fingerprint clears the recorded one instead of keeping the old value.
func TestPersistFingerprintsReplacesWithZeroFingerprints(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if err := seedHomeState(k, state.HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:img",
		EnvState:    state.EnvState{Hash: "old-env", Entries: []string{"OLD"}},
	}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	if err := persistFingerprints(k, state.Fingerprints{}); err != nil {
		t.Fatalf("persistFingerprints: %v", err)
	}

	got, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.HomeVolume != "vol" || got.ImageDigest != "sha256:img" {
		t.Errorf("home volume/digest = %q/%q, want the seeded vol/sha256:img", got.HomeVolume, got.ImageDigest)
	}
	if got.EnvState.Hash != "" {
		t.Errorf("EnvState.Hash = %q, want empty", got.EnvState.Hash)
	}
}

// TestPersistFingerprintsSkipsMissingState pins that a first boot with no prior
// state.yaml records nothing. Creating a fingerprint-only document there would
// persist home_volume: "" (HomeVolume has no omitempty) and poison the next
// run's home resolution with a GetVolume("") failure.
func TestPersistFingerprintsSkipsMissingState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "newproj", Agent: "opencode"}

	if err := persistFingerprints(k, state.Fingerprints{
		Env:    state.EnvState{Hash: "h1", Entries: []string{"X"}},
		Secret: state.SecretState{Hash: "h2", Entries: []string{"Y"}},
	}); err != nil {
		t.Fatalf("persistFingerprints: %v", err)
	}

	if _, err := state.ReadState(k); !errors.Is(err, state.ErrStateNotFound) {
		t.Fatalf("ReadState error = %v, want ErrStateNotFound (no state created)", err)
	}
	if _, err := os.Stat(filepath.Join(state.KeyDir(k), "state.yaml")); !os.IsNotExist(err) {
		t.Errorf("state.yaml exists after a first boot with no prior state: err = %v", err)
	}
}

// TestPersistFingerprintsFailsOnCorruptState pins that a state file that exists
// but cannot be parsed is reported, not treated as missing state.
func TestPersistFingerprintsFailsOnCorruptState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "corruptproj", Agent: "opencode"}
	stateDir := filepath.Join(configpaths.Get().UserStateDir(), k.Slug, k.Agent)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, stateDir, "state.yaml", "{ corrupted: yaml: [")

	err := persistFingerprints(k, state.Fingerprints{Env: state.EnvState{Hash: "h"}})
	if err == nil {
		t.Fatal("persistFingerprints succeeded, want an error for a corrupt state file")
	}
	if !strings.Contains(err.Error(), "read state for transaction") {
		t.Errorf("error = %v, want it to report the state read failure", err)
	}
}
