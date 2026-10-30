package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"
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

func TestResolveReconfigDecision_NetworkChangedWithPersistedState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := reconfigMockClient()
	msb.WithMsbMock(t, mock)

	vm := volume.NewManager(&termio.Mock{})

	// Zero network state (no policy recorded yet) + a non-empty desired policy
	// => recreate to apply the policy.
	persistedState := state.HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:samedigest",
	}
	opts := options.RunOptions{Network: network.Policy{Profiles: []network.Profile{network.ProfileNone}}}

	ui := termio.NewTestMock(t)
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	decision, err := resolveReconfigDecision(
		context.Background(),
		mock,
		vm,
		state.Key{Slug: "testproj", Agent: "opencode"},
		opts,
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:samedigest"},
		"vol",
		persistedState,
		cfs,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if !decision.Recreate {
		t.Error("expected recreate when network policy differs from persisted state (network cannot be applied live)")
	}
	if decision.Restart {
		t.Error("expected no daemon restart for network change (folded into recreate)")
	}
}

func TestResolveReconfigDecision_NetworkUnchangedNoRecreate(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := reconfigMockClient()
	msb.WithMsbMock(t, mock)

	vm := volume.NewManager(&termio.Mock{})

	policy := network.Policy{Profiles: []network.Profile{network.ProfileNone}}
	persistedState := state.HomeState{
		HomeVolume:   "vol",
		ImageDigest:  "sha256:samedigest",
		NetworkState: state.BuildNetworkState(policy),
	}
	opts := options.RunOptions{Network: policy}

	ui := termio.NewTestMock(t)
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	decision, err := resolveReconfigDecision(
		context.Background(),
		mock,
		vm,
		state.Key{Slug: "testproj", Agent: "opencode"},
		opts,
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:samedigest"},
		"vol",
		persistedState,
		cfs,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if decision.Recreate {
		t.Error("expected no recreate when network policy matches persisted state")
	}
	if decision.Restart {
		t.Error("expected no restart when network policy matches persisted state")
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

func TestResolveReconfigDecision_EnvChangedWithPersistedState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	// Set up handle so reconfig.PlanReconfig gets a non-nil cfg
	mock := reconfigMockClient()
	msb.WithMsbMock(t, mock)
	configpaths.WithMockConfigPaths(t)

	vm := volume.NewManager(&termio.Mock{})

	// Write state with env hash that differs from desired (nil desired = empty env)
	// Use matching imageDigest to skip home-volume path
	persistedState := state.HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:samedigest",
		EnvState: state.EnvState{
			Hash:  "sha256:differenthash",
			Names: []string{"OLD_KEY"},
		},
	}

	ui := termio.NewTestMock(t)
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	decision, err := resolveReconfigDecision(
		context.Background(),
		mock,
		vm,
		state.Key{Slug: "testproj", Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:samedigest"},
		"vol",
		persistedState,
		cfs,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if !decision.Recreate {
		t.Error("expected recreate when env differs from persisted state (env cannot be applied live)")
	}
	if decision.Restart {
		t.Error("expected no daemon restart for env change (folded into recreate)")
	}
}

func TestResolveReconfigDecision_EnvUnchangedWithPersistedState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	userDir := t.TempDir()

	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)
	configpaths.WithMockConfigPaths(t)

	vm := volume.NewManager(&termio.Mock{})

	// Write env file and state with matching hash
	testutil.WriteFile(t, userDir, configpaths.EnvFileName, "FOO=bar\n")
	envHash := computeEnvHash(filepath.Join(userDir, configpaths.EnvFileName))

	// Build HomeState with matching env state
	persistedState := state.HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:img1",
		EnvState: state.EnvState{
			Hash:  envHash,
			Names: []string{"FOO"},
		},
	}

	ui := termio.NewTestMock(t)
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	decision, err := resolveReconfigDecision(
		context.Background(),
		mock,
		vm,
		state.Key{Slug: "testproj", Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:samedigest"},
		"vol",
		persistedState,
		cfs,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if decision.Recreate {
		t.Error("expected no recreate")
	}
	if decision.Restart {
		t.Error("expected no restart when env matches persisted state")
	}
}

func TestResolveReconfigDecision_SecretsChangedWithPersistedState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := reconfigMockClient()
	msb.WithMsbMock(t, mock)
	configpaths.WithMockConfigPaths(t)

	vm := volume.NewManager(&termio.Mock{})

	// Write state with different secret hash than desired (nil desired = no secrets)
	// Use matching imageDigest to skip home-volume path
	persistedState := state.HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:samedigest",
		SecretState: state.SecretState{
			Hash:  "sha256:oldsecrethash",
			Names: []string{"DB_PASS"},
		},
	}

	ui := termio.NewTestMock(t)
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	decision, err := resolveReconfigDecision(
		context.Background(),
		mock,
		vm,
		state.Key{Slug: "testproj", Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:samedigest"},
		"vol",
		persistedState,
		cfs,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if !decision.Recreate {
		t.Error("expected recreate when secrets differ from persisted state (secrets cannot be applied live)")
	}
	if decision.Restart {
		t.Error("expected no daemon restart for secrets change (folded into recreate)")
	}
}

func TestResolveReconfigDecision_ZeroPersistedStateNoSpuriousChange(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	userDir := t.TempDir()

	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)
	configpaths.WithMockConfigPaths(t)

	vm := volume.NewManager(&termio.Mock{})

	// Write empty env file → desired is empty.
	// State has no env_state → zero value (first run).
	// Zero applied + empty desired → NOT changed (per ruling)
	testutil.WriteFile(t, userDir, configpaths.EnvFileName, "# nothing here\n")

	ui := termio.NewTestMock(t)
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	decision, err := resolveReconfigDecision(
		context.Background(),
		mock,
		vm,
		state.Key{Slug: "testproj", Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:samedigest"},
		"vol",
		state.HomeState{},
		cfs,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if decision.Recreate {
		t.Error("expected no recreate")
	}
	if decision.Restart {
		t.Error("expected no restart when no env is configured (zero+empty)")
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

func TestResolveReconfigDecision_PersistedSecretsMatchDesired(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	userDir := t.TempDir()

	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)
	configpaths.WithMockConfigPaths(t)

	vm := volume.NewManager(&termio.Mock{})

	testutil.WriteFile(t, userDir, configpaths.EnvFileName, "K=V\n")
	desiredEnv := envsecret.MergeEnvMaps(envsecret.BuildEnvMap(filepath.Join(userDir, configpaths.EnvFileName)))
	envHash := state.EnvContentHash(desiredEnv)

	seedHomeState(state.Key{Slug: "myproj5", Agent: "opencode"}, state.HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:img",
		EnvState:    state.EnvState{Hash: envHash, Names: []string{"K"}},
	})

	persistedState := state.HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:img",
		EnvState:    state.EnvState{Hash: envHash, Names: []string{"K"}},
	}

	ui := termio.NewTestMock(t)
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	decision, err := resolveReconfigDecision(
		context.Background(),
		mock,
		vm,
		state.Key{Slug: "testproj", Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img", Ref: "img", Digest: "sha256:img"},
		"vol",
		persistedState,
		cfs,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if decision.Recreate {
		t.Error("expected no recreate")
	}
	if decision.Restart {
		t.Error("expected no restart when env matches persisted state and no secrets")
	}
}

func TestResolveReconfigDecision_HomePromptDeferredWhenRebuildDeferred(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := reconfigMockClient()
	msb.WithMsbMock(t, mock)
	configpaths.WithMockConfigPaths(t)

	key := state.Key{Slug: "testproj", Agent: "opencode"}
	release, err := state.AcquireClientLease(key)
	if err != nil {
		t.Fatalf("AcquireClientLease: %v", err)
	}
	defer release()

	reconfigPrompted := false
	homePrompted := false
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(prompt string, _ []termio.Choice, _ string) (string, error) {
			switch {
			case strings.Contains(prompt, "Applying requires rebuilding"):
				reconfigPrompted = true
				return "k", nil
			case strings.Contains(prompt, "Docker image changed"):
				homePrompted = true
			}
			return "", nil
		},
	}
	vm := volume.NewManager(ui)

	persisted := state.HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:old",
	}

	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	imageInfo := image.ImageInfo{
		DockerTag: "img:tag",
		Ref:       "img:tag",
		// imageRef == cfg.Image; the digest still identifies the image
		Digest: "sha256:new",
	} // stored digest differs -> image-triggered rebuild
	decision, err := resolveReconfigDecision(
		context.Background(),
		mock,
		vm,
		key,
		options.RunOptions{Agent: "opencode"},
		imageInfo,
		"vol",
		persisted,
		cfs,
		envsecret.EnvSecrets{},
		ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	homeVol, err := applyReconfigHomeAction(
		context.Background(), mock, vm, key, "vol", imageInfo,
		false, decision, ui,
	)
	if err != nil {
		t.Fatalf("applyReconfigHomeAction: %v", err)
	}
	if !reconfigPrompted {
		t.Error("expected VM-recreate prompt when another client is active")
	}
	if homePrompted {
		t.Error("expected home-volume prompt to be deferred with the VM rebuild")
	}
	if homeVol != "vol" {
		t.Errorf("homeVol = %q, want unchanged %q", homeVol, "vol")
	}
	if decision.Recreate {
		t.Error("expected VM rebuild to be deferred when another client keeps the current VM")
	}
	if decision.Restart {
		t.Error("expected no restart")
	}
	if joined := strings.Join(ui.VerboseCalls, "\n"); !strings.Contains(joined, "image digest changed") {
		t.Errorf("verbose calls = %v, want the image-digest change reason", ui.VerboseCalls)
	}
}

func TestResolveReconfigDecision_HomePromptAskedWhenRebuildConfirmed(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := reconfigMockClient()
	msb.WithMsbMock(t, mock)
	configpaths.WithMockConfigPaths(t)

	prompted := false
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(prompt string, _ []termio.Choice, _ string) (string, error) {
			if strings.Contains(prompt, "Docker image changed") {
				prompted = true
				return "k", nil // keep volume
			}
			return "", nil
		},
	}
	vm := volume.NewManager(ui)

	persisted := state.HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:old",
	}
	slug := git.MustCheckoutSlug()
	seedHomeState(state.Key{Slug: slug, Agent: "opencode"}, persisted)

	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	imageInfo := image.ImageInfo{
		DockerTag: "img:new",
		Ref:       "img:new",
		// imageRef != cfg.Image("img:tag") -> image-triggered rebuild
		Digest: "sha256:new",
	} // stored digest differs -> image change detected
	decision, err := resolveReconfigDecision(
		context.Background(),
		mock,
		vm,
		state.Key{Slug: slug, Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		imageInfo,
		"vol",
		persisted,
		cfs,
		envsecret.EnvSecrets{},
		ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	homeVol, err := applyReconfigHomeAction(
		context.Background(), mock, vm, state.Key{Slug: slug, Agent: "opencode"}, "vol", imageInfo,
		false, decision, ui,
	)
	if err != nil {
		t.Fatalf("applyReconfigHomeAction: %v", err)
	}
	if !prompted {
		t.Error("expected home-volume prompt to fire when the rebuild onto the new image is confirmed")
	}
	if !decision.Recreate {
		t.Error("expected recreate when switching to the new image")
	}
	if decision.Restart {
		t.Error("expected no restart (env/secret/config unchanged)")
	}
	if homeVol != "vol" {
		t.Errorf("homeVol = %q, want %q (keep chosen)", homeVol, "vol")
	}

	st, rerr := state.ReadState(state.Key{Slug: slug, Agent: "opencode"})
	if rerr != nil {
		t.Fatalf("ReadState: %v", rerr)
	}
	if st.ImageDigest != "sha256:new" {
		t.Errorf("state ImageDigest = %q, want %q (keep records the new digest)", st.ImageDigest, "sha256:new")
	}
}

func TestResolveReconfigDecision_OpenCodeConfigChanged_StoppedVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	// Desired opencode config: a snippet producing {"model":"y"} (new config).
	testutil.WriteFile(t,
		configpaths.MustDir(configpaths.Get().UserAgentConfigDir(opencodeAgent(t))),
		"a.json",
		`{"model":"y"}`,
	)

	// Existing VM whose Connect fails (stopped/suspended VM):
	// resolveReconfigDecision cannot read live config, so it must neither
	// recreate nor spuriously flag a daemon restart. Provisioning + fresh daemon
	// pickup happen downstream in setUpSandbox once the VM is started.
	sh := &msb.MockSandboxHandle{
		Cfg: &msbSdk.SandboxConfig{Image: "img:tag", CPUs: 4, MemoryMiB: 4096},
	}
	sh.ConnectErr = errors.New("vm not running")
	mock := &msb.MockMsbClient{}
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return sh, nil
	}
	msb.WithMsbMock(t, mock)

	vm := volume.NewManager(&termio.Mock{})
	persisted := state.HomeState{
		HomeVolume:   "vol",
		ImageDigest:  "sha256:same",
		NetworkState: state.BuildNetworkState(network.Policy{}),
	}

	ui := termio.NewTestMock(t)
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	decision, err := resolveReconfigDecision(
		context.Background(),
		mock,
		vm,
		state.Key{Slug: "testproj", Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:same"},
		"vol",
		persisted,
		cfs,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if decision.Recreate {
		t.Error("expected no recreate for an opencode-config-only change on a stopped VM")
	}
	if decision.Restart {
		t.Error("expected no daemon-restart flag from a stopped VM (fresh daemon picks up config on start)")
	}
}

// Test helpers

func reconfigMockClient() *msb.MockMsbClient {
	mock := &msb.MockMsbClient{}
	sh := &msb.MockSandboxHandle{Cfg: &msbSdk.SandboxConfig{
		Image: "img:tag", CPUs: 4, MemoryMiB: 4096,
	}}
	sh.ConnectSb = &msb.MockSandbox{}
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return sh, nil
	}
	return mock
}

func computeEnvHash(envFile string) string {
	env := envsecret.BuildEnvMap(envFile)
	return state.EnvContentHash(env)
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
