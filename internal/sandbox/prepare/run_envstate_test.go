package prepare

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestPersistEnvSecrets_RoundTrip(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "roundtripproj"
	k := state.Key{Slug: slug, Agent: "opencode"}
	if err := seedHomeState(k, state.NewHomeState("vol", "digest")); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	envSt := state.EnvState{Hash: "env-hash-123", Names: []string{"FOO"}}
	secSt := state.SecretState{Hash: "sec-hash-456", Names: []string{"PASS"}}

	err := persistFingerprints(k, state.Fingerprints{Env: envSt, Secret: secSt})
	if err != nil {
		t.Fatalf("persistFingerprints: %v", err)
	}

	got, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState after persist: %v", err)
	}
	if got.EnvState.Hash != "env-hash-123" {
		t.Errorf("EnvState.Hash = %q, want %q", got.EnvState.Hash, "env-hash-123")
	}
	if got.SecretState.Hash != "sec-hash-456" {
		t.Errorf("SecretState.Hash = %q, want %q", got.SecretState.Hash, "sec-hash-456")
	}
}

// TestPersistFingerprintsSkipsMissingState pins that a first boot with no prior
// state.yaml records nothing. Creating a fingerprint-only document there would
// persist home_volume: "" (HomeVolume has no omitempty) and poison the next
// run's home resolution with a GetVolume("") failure (#430).
func TestPersistFingerprintsSkipsMissingState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	k := state.Key{Slug: "newproj-" + makeSlug(), Agent: "opencode"}

	envSt := state.EnvState{Hash: "h1", Names: []string{"X"}}
	secSt := state.SecretState{Hash: "h2", Names: []string{"Y"}}

	if err := persistFingerprints(k, state.Fingerprints{Env: envSt, Secret: secSt}); err != nil {
		t.Fatalf("persistFingerprints: %v", err)
	}

	if _, err := state.ReadState(k); !errors.Is(err, state.ErrStateNotFound) {
		t.Fatalf("ReadState error = %v, want ErrStateNotFound (no state created)", err)
	}
	if _, err := os.Stat(filepath.Join(state.KeyDir(k), "state.yaml")); !os.IsNotExist(err) {
		t.Errorf("state.yaml exists after a first boot with no prior state: err = %v", err)
	}
}

func TestPersistEnvSecrets_MergesExistingState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "mergeproj"

	// Write existing state with HomeVolume and ImageDigest
	seedHomeState(state.Key{Slug: slug, Agent: "opencode"}, state.HomeState{
		HomeVolume:  "existing-vol",
		ImageDigest: "sha256:existing",
	})

	envSt := state.EnvState{Hash: "new-env-hash", Names: []string{"NEW"}}
	secSt := state.SecretState{Hash: "new-sec-hash", Names: []string{"SECRET"}}

	if err := persistFingerprints(
		state.Key{Slug: slug, Agent: "opencode"},
		state.Fingerprints{Env: envSt, Secret: secSt},
	); err != nil {
		t.Fatalf("persistFingerprints: %v", err)
	}

	got, err := state.ReadState(state.Key{Slug: slug, Agent: "opencode"})
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.HomeVolume != "existing-vol" {
		t.Errorf("HomeVolume = %q, want %q", got.HomeVolume, "existing-vol")
	}
	if got.ImageDigest != "sha256:existing" {
		t.Errorf("ImageDigest = %q, want %q", got.ImageDigest, "sha256:existing")
	}
	if got.EnvState.Hash != "new-env-hash" {
		t.Errorf("EnvState.Hash = %q, want %q", got.EnvState.Hash, "new-env-hash")
	}
	if got.SecretState.Hash != "new-sec-hash" {
		t.Errorf("SecretState.Hash = %q, want %q", got.SecretState.Hash, "new-sec-hash")
	}
}

func TestPersistEnvSecrets_OverwritesExistingState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "existingstateproj"

	// Pre-write state with env_state already set but different hash
	oldState := state.HomeState{
		HomeVolume: "vol", ImageDigest: "d1",
		EnvState:    state.EnvState{Hash: "sha256:olddata", Names: nil},
		SecretState: state.SecretState{},
	}
	seedHomeState(state.Key{Slug: slug, Agent: "opencode"}, oldState)

	envSt := state.EnvState{Hash: "newhash", Names: []string{"K"}}
	secSt := state.SecretState{Hash: "sh", Names: []string{"S"}}

	if err := persistFingerprints(
		state.Key{Slug: slug, Agent: "opencode"},
		state.Fingerprints{Env: envSt, Secret: secSt},
	); err != nil {
		t.Fatalf("persistFingerprints: %v", err)
	}

	got, err := state.ReadState(state.Key{Slug: slug, Agent: "opencode"})
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.EnvState.Hash != "newhash" {
		t.Errorf("EnvState.Hash = %q, want %q", got.EnvState.Hash, "newhash")
	}
	if got.SecretState.Hash != "sh" {
		t.Errorf("SecretState.Hash = %q, want %q", got.SecretState.Hash, "sh")
	}
}

func TestPersistNetworkState_RoundTrip(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "netproj"
	k := state.Key{Slug: slug, Agent: "opencode"}
	if err := seedHomeState(k, state.NewHomeState("vol", "digest")); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
	policy := network.Policy{Profiles: []network.Profile{network.ProfileNone}, EgressAllow: []string{"api.example.com"}}

	if err := persistFingerprints(
		k,
		state.Fingerprints{Network: state.BuildNetworkState(policy)},
	); err != nil {
		t.Fatalf("persistFingerprints: %v", err)
	}

	got, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState after persist: %v", err)
	}
	want := state.BuildNetworkState(policy)
	if got.NetworkState.Hash != want.Hash {
		t.Errorf("NetworkState.Hash = %q, want %q", got.NetworkState.Hash, want.Hash)
	}
}

func TestPersistEnvSecrets_ReadFailsReturnsError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "failproj"

	// Corrupted YAML that returns an error (not ErrStateNotFound):
	sdir := filepath.Join(configpaths.Get().UserStateDir(), slug, "opencode")
	os.MkdirAll(sdir, 0o700)
	testutil.WriteFile(t, sdir, "state.yaml", "{ corrupted: yaml: [")

	envSt := state.EnvState{Hash: "h"}
	secSt := state.SecretState{Hash: "h"}
	err := persistFingerprints(state.Key{Slug: slug, Agent: "opencode"}, state.Fingerprints{Env: envSt, Secret: secSt})

	if err == nil {
		t.Fatal("expected error for corrupted YAML, got nil")
	}
	if !errIsContains(err, "read state for transaction") {
		t.Errorf("expected error containing 'read state for transaction', got: %v", err)
	}
}

func TestPersistEnvSecrets_HomeStateOmitsZeroState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "omittestproj"
	k := state.Key{Slug: slug, Agent: "opencode"}
	if err := seedHomeState(k, state.NewHomeState("vol", "digest")); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	envSt := state.EnvState{Hash: "h1", Names: []string{"X"}}
	secSt := state.SecretState{}

	if err := persistFingerprints(k, state.Fingerprints{Env: envSt, Secret: secSt}); err != nil {
		t.Fatalf("persistFingerprints: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(configpaths.Get().UserStateDir(), slug, "opencode", "state.yaml"))
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	content := string(data)
	if !containsStr(content, "env_state") {
		t.Error("expected env_state in YAML")
	}
}

func TestPersistEnvSecrets_ZeroStateOverwritesOnlyFields(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "overwriteproj"

	seedHomeState(state.Key{Slug: slug, Agent: "opencode"}, state.HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:img",
		EnvState:    state.EnvState{Hash: "old-env", Names: []string{"OLD"}},
	})

	if err := persistFingerprints(
		state.Key{Slug: slug, Agent: "opencode"},
		state.Fingerprints{Env: state.EnvState{}, Secret: state.SecretState{}},
	); err != nil {
		t.Fatalf("persistFingerprints: %v", err)
	}

	got, err := state.ReadState(state.Key{Slug: slug, Agent: "opencode"})
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.HomeVolume != "vol" {
		t.Errorf("HomeVolume = %q, want %q", got.HomeVolume, "vol")
	}
	if got.ImageDigest != "sha256:img" {
		t.Errorf("ImageDigest = %q, want %q", got.ImageDigest, "sha256:img")
	}
	if got.EnvState.Hash != "" {
		t.Errorf("EnvState.Hash = %q, want empty (zero overwrite)", got.EnvState.Hash)
	}
}

// TestPersistMountState_RecordsOnExistingState pins that mount fingerprints are
// folded into an existing document (the post-home-resolution path); a first boot
// with no state is a no-op covered by TestPersistFingerprintsSkipsMissingState.
func TestPersistMountState_RecordsOnExistingState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "freshmountproject"
	k := state.Key{Slug: slug, Agent: "opencode"}
	if err := seedHomeState(k, state.NewHomeState("vol", "digest")); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
	mnts := mounts.Mounts{
		"/home/dev/.m2": {Source: "/host/.m2"},
	}

	if err := persistFingerprints(
		k,
		state.Fingerprints{Mount: state.BuildMountState(mnts)},
	); err != nil {
		t.Fatalf("persistFingerprints: %v", err)
	}

	got, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.HomeVolume != "vol" {
		t.Errorf("HomeVolume = %q, want the seeded vol", got.HomeVolume)
	}
	if got.MountState.Hash != mounts.Fingerprint(mnts) {
		t.Errorf("MountState.Hash = %q, want %q", got.MountState.Hash, mounts.Fingerprint(mnts))
	}
}

// TestPersistMountStateCorruptState covers the read-error branch, where the
// state file exists but cannot be parsed.
func TestPersistMountStateCorruptState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "corruptmountproject"
	sdir := filepath.Join(configpaths.Get().UserStateDir(), slug, "opencode")
	if err := os.MkdirAll(sdir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, sdir, "state.yaml", "{ corrupted: yaml: [")

	err := persistFingerprints(
		state.Key{Slug: slug, Agent: "opencode"},
		state.Fingerprints{Mount: state.BuildMountState(nil)},
	)
	if err == nil {
		t.Fatal("expected an error for a corrupt state file")
	}
	if !strings.Contains(err.Error(), "read state for transaction") {
		t.Errorf("error = %v, want it to mention a state read failure", err)
	}
}

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

func TestNetworkChanged_ZeroApplied_NonEmptyDesired(t *testing.T) {
	got := state.NetworkChanged(state.NetworkState{}, network.Policy{Profiles: []network.Profile{network.ProfileNone}})
	if !got {
		t.Error("expected change when applied is zero and desired is non-empty")
	}
}

func TestNetworkChanged_ZeroApplied_EmptyDesired(t *testing.T) {
	got := state.NetworkChanged(state.NetworkState{}, network.Policy{})
	if !got {
		t.Error("expected change when applied is zero so the secure default can be applied")
	}
}

func TestNetworkChanged_MatchingHash(t *testing.T) {
	policy := network.Policy{Profiles: []network.Profile{network.ProfileNone}, EgressAllow: []string{"api.example.com"}}
	applied := state.BuildNetworkState(policy)
	if got := state.NetworkChanged(applied, policy); got {
		t.Error("expected NO change when fingerprints match")
	}
}

func TestNetworkChanged_DifferentHash(t *testing.T) {
	policy := network.Policy{Profiles: []network.Profile{network.ProfileNone}, EgressAllow: []string{"api.example.com"}}
	applied := state.BuildNetworkState(network.Policy{Profiles: []network.Profile{network.ProfileNone}})
	if got := state.NetworkChanged(applied, policy); !got {
		t.Error("expected change when fingerprints differ")
	}
}

func TestSecretsChanged_ZeroApplied_NilDesired(t *testing.T) {
	got := state.SecretsChanged(state.SecretState{}, nil)
	if got {
		t.Error("expected NO change when applied is zero and desired is nil")
	}
}

func TestSecretsChanged_ZeroApplied_EmptyDesired(t *testing.T) {
	got := state.SecretsChanged(state.SecretState{}, []msbSdk.SecretEntry{})
	if got {
		t.Error("expected NO change when applied is zero and desired is empty")
	}
}

func TestSecretsChanged_NonZeroApplied_DifferentHash(t *testing.T) {
	got := state.SecretsChanged(state.SecretState{Hash: "h1"}, []msbSdk.SecretEntry{{EnvVar: "K", Value: "v"}})
	if !got {
		t.Error("expected change when hashes differ")
	}
}

func TestEnvChanged_ZeroApplied_NonEmptyDesired(t *testing.T) {
	got := state.EnvChanged(state.EnvState{}, map[string]string{"FOO": "bar"})
	if !got {
		t.Error("expected change when applied is zero and desired is non-empty")
	}
}

func TestEnvChanged_MatchingHash(t *testing.T) {
	desired := map[string]string{"FOO": "bar"}
	wantHash := state.EnvContentHash(desired)
	got := state.EnvChanged(state.EnvState{Hash: wantHash}, desired)
	if got {
		t.Error("expected NO change when hashes match")
	}
}

func TestSecretState_NilEntries_RendersEmpty(t *testing.T) {
	got := state.SecretsContentHash(nil)
	if got == "" {
		t.Error("expected non-empty hash for nil entries")
	}
}

func TestEnvContentHash_OrderIndependent(t *testing.T) {
	a := map[string]string{"A": "1", "B": "2"}
	b := map[string]string{"B": "2", "A": "1"}
	hA := state.EnvContentHash(a)
	hB := state.EnvContentHash(b)
	if hA != hB {
		t.Errorf("hashes differ for same content in different order: %q vs %q", hA, hB)
	}
}

func makeSlug() string {
	return "aBcDeFgHiJkLmNoP"
}

func errIsContains(err error, sub string) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && contains(s, sub)
}
