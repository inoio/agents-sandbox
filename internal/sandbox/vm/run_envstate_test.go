package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/git"
	"github.com/inoio/opencode-sandbox/internal/sandbox/mounts"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/network"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	"github.com/inoio/opencode-sandbox/internal/sandbox/volume"
	"github.com/inoio/opencode-sandbox/internal/termio"
	"github.com/inoio/opencode-sandbox/internal/testutil"
)

func TestPersistEnvSecrets_RoundTrip(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "roundtripproj"

	envSt := state.EnvState{Hash: "env-hash-123", Names: []string{"FOO"}}
	secSt := state.SecretState{Hash: "sec-hash-456", Names: []string{"PASS"}}

	err := persistEnvSecrets(slug, envSt, secSt)
	if err != nil {
		t.Fatalf("persistEnvSecrets: %v", err)
	}

	got, err := state.ReadState(slug)
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

func TestPersistEnvSecrets_CreatesMissingStateDir(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "newproj-" + makeSlug()

	envSt := state.EnvState{Hash: "h1", Names: []string{"X"}}
	secSt := state.SecretState{Hash: "h2", Names: []string{"Y"}}

	if err := persistEnvSecrets(slug, envSt, secSt); err != nil {
		t.Fatalf("persistEnvSecrets: %v", err)
	}

	got, err := state.ReadState(slug)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.EnvState.Hash != "h1" {
		t.Errorf("EnvState.Hash = %q, want %q", got.EnvState.Hash, "h1")
	}
}

func TestPersistEnvSecrets_MergesExistingState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "mergeproj"

	// Write existing state with HomeVolume and ImageDigest
	state.WriteState(slug, state.HomeState{
		HomeVolume:  "existing-vol",
		ImageDigest: "sha256:existing",
	})

	envSt := state.EnvState{Hash: "new-env-hash", Names: []string{"NEW"}}
	secSt := state.SecretState{Hash: "new-sec-hash", Names: []string{"SECRET"}}

	if err := persistEnvSecrets(slug, envSt, secSt); err != nil {
		t.Fatalf("persistEnvSecrets: %v", err)
	}

	got, err := state.ReadState(slug)
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
	state.WriteState(slug, oldState)

	envSt := state.EnvState{Hash: "newhash", Names: []string{"K"}}
	secSt := state.SecretState{Hash: "sh", Names: []string{"S"}}

	if err := persistEnvSecrets(slug, envSt, secSt); err != nil {
		t.Fatalf("persistEnvSecrets: %v", err)
	}

	got, err := state.ReadState(slug)
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
	policy := network.Policy{Profile: network.ProfileNone, EgressAllow: []string{"api.example.com"}}

	if err := persistNetworkState(slug, policy); err != nil {
		t.Fatalf("persistNetworkState: %v", err)
	}

	got, err := state.ReadState(slug)
	if err != nil {
		t.Fatalf("ReadState after persist: %v", err)
	}
	want := reprovision.BuildNetworkState(policy)
	if got.NetworkState.Hash != want.Hash {
		t.Errorf("NetworkState.Hash = %q, want %q", got.NetworkState.Hash, want.Hash)
	}
}

func TestDecideReconfig_NetworkChangedWithPersistedState(t *testing.T) {
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
	opts := options.RunOptions{Network: network.Policy{Profile: network.ProfileNone}}

	ui := termio.NewTestMock(t)
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	recreate, restart, _, err := decideReconfig(
		context.Background(),
		mock,
		vm,
		opts,
		"img:tag",
		"sha256:samedigest",
		"vol",
		persistedState,
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("decideReconfig: %v", err)
	}
	if !recreate {
		t.Error("expected recreate when network policy differs from persisted state (network cannot be applied live)")
	}
	if restart {
		t.Error("expected no daemon restart for network change (folded into recreate)")
	}
}

func TestDecideReconfig_NetworkUnchangedNoRecreate(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := reconfigMockClient()
	msb.WithMsbMock(t, mock)

	vm := volume.NewManager(&termio.Mock{})

	policy := network.Policy{Profile: network.ProfileNone}
	persistedState := state.HomeState{
		HomeVolume:   "vol",
		ImageDigest:  "sha256:samedigest",
		NetworkState: reprovision.BuildNetworkState(policy),
	}
	opts := options.RunOptions{Network: policy}

	ui := termio.NewTestMock(t)
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	recreate, restart, _, err := decideReconfig(
		context.Background(),
		mock,
		vm,
		opts,
		"img:tag",
		"sha256:samedigest",
		"vol",
		persistedState,
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("decideReconfig: %v", err)
	}
	if recreate {
		t.Error("expected no recreate when network policy matches persisted state")
	}
	if restart {
		t.Error("expected no restart when network policy matches persisted state")
	}
}

func TestNetworkChanged_ZeroApplied_NonEmptyDesired(t *testing.T) {
	got := reprovision.NetworkChanged(state.NetworkState{}, network.Policy{Profile: network.ProfileNone})
	if !got {
		t.Error("expected change when applied is zero and desired is non-empty")
	}
}

func TestNetworkChanged_ZeroApplied_EmptyDesired(t *testing.T) {
	got := reprovision.NetworkChanged(state.NetworkState{}, network.Policy{})
	if got {
		t.Error("expected NO change when applied is zero and desired is empty")
	}
}

func TestNetworkChanged_MatchingHash(t *testing.T) {
	policy := network.Policy{Profile: network.ProfileNone, EgressAllow: []string{"api.example.com"}}
	applied := reprovision.BuildNetworkState(policy)
	if got := reprovision.NetworkChanged(applied, policy); got {
		t.Error("expected NO change when fingerprints match")
	}
}

func TestNetworkChanged_DifferentHash(t *testing.T) {
	policy := network.Policy{Profile: network.ProfileNone, EgressAllow: []string{"api.example.com"}}
	applied := reprovision.BuildNetworkState(network.Policy{Profile: network.ProfileNone})
	if got := reprovision.NetworkChanged(applied, policy); !got {
		t.Error("expected change when fingerprints differ")
	}
}

func TestPersistEnvSecrets_ReadFailsReturnsError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "failproj"

	// Corrupted YAML that returns an error (not ErrStateNotFound):
	sdir := filepath.Join(configpaths.Get().UserStateDir(), slug)
	os.MkdirAll(sdir, 0o700)
	testutil.WriteFile(t, sdir, "state.yaml", "{ corrupted: yaml: [")

	envSt := state.EnvState{Hash: "h"}
	secSt := state.SecretState{Hash: "h"}
	err := persistEnvSecrets(slug, envSt, secSt)

	if err == nil {
		t.Fatal("expected error for corrupted YAML, got nil")
	}
	if !errIsContains(err, "read state for persistence") {
		t.Errorf("expected error containing 'read state for persistence', got: %v", err)
	}
}

func TestDecideReconfig_EnvChangedWithPersistedState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	// Set up handle so reprovision.PlanReconfig gets a non-nil cfg
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
	recreate, restart, _, err := decideReconfig(
		context.Background(),
		mock,
		vm,
		options.RunOptions{},
		"img:tag",
		"sha256:samedigest",
		"vol",
		persistedState,
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("decideReconfig: %v", err)
	}
	if !recreate {
		t.Error("expected recreate when env differs from persisted state (env cannot be applied live)")
	}
	if restart {
		t.Error("expected no daemon restart for env change (folded into recreate)")
	}
}

func TestDecideReconfig_EnvUnchangedWithPersistedState(t *testing.T) {
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
	recreate, restart, _, err := decideReconfig(
		context.Background(),
		mock,
		vm,
		options.RunOptions{},
		"img:tag",
		"sha256:samedigest",
		"vol",
		persistedState,
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("decideReconfig: %v", err)
	}
	if recreate {
		t.Error("expected no recreate")
	}
	if restart {
		t.Error("expected no restart when env matches persisted state")
	}
}

func TestDecideReconfig_SecretsChangedWithPersistedState(t *testing.T) {
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
	recreate, restart, _, err := decideReconfig(
		context.Background(),
		mock,
		vm,
		options.RunOptions{},
		"img:tag",
		"sha256:samedigest",
		"vol",
		persistedState,
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("decideReconfig: %v", err)
	}
	if !recreate {
		t.Error("expected recreate when secrets differ from persisted state (secrets cannot be applied live)")
	}
	if restart {
		t.Error("expected no daemon restart for secrets change (folded into recreate)")
	}
}

func TestDecideReconfig_ZeroPersistedStateNoSpuriousChange(t *testing.T) {
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
	recreate, restart, _, err := decideReconfig(
		context.Background(),
		mock,
		vm,
		options.RunOptions{},
		"img:tag",
		"sha256:samedigest",
		"vol",
		state.HomeState{},
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("decideReconfig: %v", err)
	}
	if recreate {
		t.Error("expected no recreate")
	}
	if restart {
		t.Error("expected no restart when no env is configured (zero+empty)")
	}
}

func TestSecretsChanged_ZeroApplied_NilDesired(t *testing.T) {
	got := reprovision.SecretsChanged(state.SecretState{}, nil)
	if got {
		t.Error("expected NO change when applied is zero and desired is nil")
	}
}

func TestSecretsChanged_ZeroApplied_EmptyDesired(t *testing.T) {
	got := reprovision.SecretsChanged(state.SecretState{}, []msbSdk.SecretEntry{})
	if got {
		t.Error("expected NO change when applied is zero and desired is empty")
	}
}

func TestSecretsChanged_NonZeroApplied_DifferentHash(t *testing.T) {
	got := reprovision.SecretsChanged(state.SecretState{Hash: "h1"}, []msbSdk.SecretEntry{{EnvVar: "K", Value: "v"}})
	if !got {
		t.Error("expected change when hashes differ")
	}
}

func TestEnvChanged_ZeroApplied_NonEmptyDesired(t *testing.T) {
	got := reprovision.EnvChanged(state.EnvState{}, map[string]string{"FOO": "bar"})
	if !got {
		t.Error("expected change when applied is zero and desired is non-empty")
	}
}

func TestEnvChanged_MatchingHash(t *testing.T) {
	desired := map[string]string{"FOO": "bar"}
	wantHash := reprovision.EnvContentHash(desired)
	got := reprovision.EnvChanged(state.EnvState{Hash: wantHash}, desired)
	if got {
		t.Error("expected NO change when hashes match")
	}
}

func TestSecretState_NilEntries_RendersEmpty(t *testing.T) {
	got := reprovision.SecretsContentHash(nil)
	if got == "" {
		t.Error("expected non-empty hash for nil entries")
	}
}

func TestEnvContentHash_OrderIndependent(t *testing.T) {
	a := map[string]string{"A": "1", "B": "2"}
	b := map[string]string{"B": "2", "A": "1"}
	hA := reprovision.EnvContentHash(a)
	hB := reprovision.EnvContentHash(b)
	if hA != hB {
		t.Errorf("hashes differ for same content in different order: %q vs %q", hA, hB)
	}
}

// TestPersistEnvSecrets_NilStateOnNotFound tests persistEnvSecrets when no state file exists.
func TestPersistEnvSecrets_NilStateOnNotFound(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "freshproject"

	envSt := state.EnvState{Hash: "h1", Names: []string{"X"}}
	secSt := state.SecretState{Hash: "h2", Names: []string{"Y"}}

	if err := persistEnvSecrets(slug, envSt, secSt); err != nil {
		t.Fatalf("persistEnvSecrets: %v", err)
	}

	got, err := state.ReadState(slug)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.HomeVolume != "" {
		t.Error("expected empty HomeVolume for newly created state")
	}
	if got.ImageDigest != "" {
		t.Error("expected empty ImageDigest for newly created state")
	}
	if got.EnvState.Hash != "h1" {
		t.Errorf("EnvState.Hash = %q, want %q", got.EnvState.Hash, "h1")
	}
	if got.SecretState.Hash != "h2" {
		t.Errorf("SecretState.Hash = %q, want %q", got.SecretState.Hash, "h2")
	}
}

func TestPersistEnvSecrets_HomeStateOmitsZeroState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "omittestproj"

	envSt := state.EnvState{Hash: "h1", Names: []string{"X"}}
	secSt := state.SecretState{}

	if err := persistEnvSecrets(slug, envSt, secSt); err != nil {
		t.Fatalf("persistEnvSecrets: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(configpaths.Get().UserStateDir(), slug, "state.yaml"))
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

	state.WriteState(slug, state.HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:img",
		EnvState:    state.EnvState{Hash: "old-env", Names: []string{"OLD"}},
	})

	if err := persistEnvSecrets(slug, state.EnvState{}, state.SecretState{}); err != nil {
		t.Fatalf("persistEnvSecrets: %v", err)
	}

	got, err := state.ReadState(slug)
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

func TestDecideReconfig_PersistedSecretsMatchDesired(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	userDir := t.TempDir()

	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)
	configpaths.WithMockConfigPaths(t)

	vm := volume.NewManager(&termio.Mock{})

	testutil.WriteFile(t, userDir, configpaths.EnvFileName, "K=V\n")
	desiredEnv := reprovision.MergeEnvMaps(reprovision.BuildEnvMap(filepath.Join(userDir, configpaths.EnvFileName)))
	envHash := reprovision.EnvContentHash(desiredEnv)

	state.WriteState("myproj5", state.HomeState{
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
	recreate, restart, _, err := decideReconfig(
		context.Background(),
		mock,
		vm,
		options.RunOptions{},
		"img", "sha256:img", "vol",
		persistedState,
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("decideReconfig: %v", err)
	}
	if recreate {
		t.Error("expected no recreate")
	}
	if restart {
		t.Error("expected no restart when env matches persisted state and no secrets")
	}
}

func TestDecideReconfig_HomePromptDeferredWhenRebuildDeferred(t *testing.T) {
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
	recreate, restart, homeVol, err := decideReconfig(
		context.Background(),
		mock,
		vm,
		options.RunOptions{},
		"img:tag",    // imageRef == cfg.Image -> no image-triggered rebuild
		"sha256:new", // stored digest differs -> image change detected, but rebuild deferred
		"vol",
		persisted,
		cfs,
		ui,
	)
	if err != nil {
		t.Fatalf("decideReconfig: %v", err)
	}
	if prompted {
		t.Error("expected home-volume prompt to be deferred when the rebuild is deferred")
	}
	if homeVol != "vol" {
		t.Errorf("homeVol = %q, want unchanged %q", homeVol, "vol")
	}
	if recreate {
		t.Error("expected no recreate when no config change triggers a rebuild")
	}
	if restart {
		t.Error("expected no restart")
	}
}

func TestDecideReconfig_HomePromptAskedWhenRebuildConfirmed(t *testing.T) {
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
	slug := git.ProjectSlug()
	state.WriteState(slug, persisted)

	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	recreate, restart, homeVol, err := decideReconfig(
		context.Background(),
		mock,
		vm,
		options.RunOptions{},
		"img:new",    // imageRef != cfg.Image("img:tag") -> image-triggered rebuild
		"sha256:new", // stored digest differs -> image change detected
		"vol",
		persisted,
		cfs,
		ui,
	)
	if err != nil {
		t.Fatalf("decideReconfig: %v", err)
	}
	if !prompted {
		t.Error("expected home-volume prompt to fire when the rebuild onto the new image is confirmed")
	}
	if !recreate {
		t.Error("expected recreate when switching to the new image")
	}
	if restart {
		t.Error("expected no restart (env/secret/config unchanged)")
	}
	if homeVol != "vol" {
		t.Errorf("homeVol = %q, want %q (keep chosen)", homeVol, "vol")
	}

	st, rerr := state.ReadState(slug)
	if rerr != nil {
		t.Fatalf("ReadState: %v", rerr)
	}
	if st.ImageDigest != "sha256:new" {
		t.Errorf("state ImageDigest = %q, want %q (keep records the new digest)", st.ImageDigest, "sha256:new")
	}
}

func TestDecideReconfig_OpenCodeConfigChanged_StoppedVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	// Desired opencode config: a snippet producing {"model":"y"} (new config).
	testutil.WriteFile(t,
		configpaths.Get().UserOpencodeConfigDir(),
		"a.json",
		`{"model":"y"}`,
	)

	// Existing VM whose Connect fails (stopped/suspended VM): decideReconfig
	// cannot read live config, so it must neither recreate nor spuriously flag a
	// daemon restart. Provisioning + fresh daemon pickup happen downstream in
	// setUpSandbox once the VM is started.
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
		HomeVolume:  "vol",
		ImageDigest: "sha256:same",
	}

	ui := termio.NewTestMock(t)
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	recreate, restart, _, err := decideReconfig(
		context.Background(),
		mock,
		vm,
		options.RunOptions{},
		"img:tag",
		"sha256:same",
		"vol",
		persisted,
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("decideReconfig: %v", err)
	}
	if recreate {
		t.Error("expected no recreate for an opencode-config-only change on a stopped VM")
	}
	if restart {
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
	env := reprovision.BuildEnvMap(envFile)
	return reprovision.EnvContentHash(env)
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

// TestPersistMountState_NilStateOnNotFound tests persistMountState when no
// state file exists yet, e.g. the very first run of a project.
func TestPersistMountState_NilStateOnNotFound(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "freshmountproject"
	mnts := mounts.Mounts{
		"/home/dev/.m2": {Source: "/host/.m2"},
	}

	if err := persistMountState(slug, mnts); err != nil {
		t.Fatalf("persistMountState: %v", err)
	}

	got, err := state.ReadState(slug)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.HomeVolume != "" {
		t.Error("expected empty HomeVolume for newly created state")
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
	sdir := filepath.Join(configpaths.Get().UserStateDir(), slug)
	if err := os.MkdirAll(sdir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, sdir, "state.yaml", "{ corrupted: yaml: [")

	err := persistMountState(slug, nil)
	if err == nil {
		t.Fatal("expected an error for a corrupt state file")
	}
	if !strings.Contains(err.Error(), "read state for mount persistence") {
		t.Errorf("error = %v, want it to mention mount persistence", err)
	}
}
