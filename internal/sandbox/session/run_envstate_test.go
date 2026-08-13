package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/git"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/options"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/reprovision"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/state"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/volume"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/testutil"
)

func TestCurrentEnvState_NotFoundReturnsZero(t *testing.T) {
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-sandbox")
	slug := "nonexistent"

	got := currentEnvState(slug, &termio.Mock{})

	if got.Hash != "" {
		t.Errorf("expected empty hash, got %q", got.Hash)
	}
	if got.Names != nil {
		t.Errorf("expected nil names, got %v", got.Names)
	}
}

func TestCurrentEnvState_ReadsPersisted(t *testing.T) {
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-sandbox")
	slug := "testproj-abc1"

	state.WriteState(slug, state.HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:xyz",
		EnvState: state.EnvState{
			Hash:  "sha256:testenvhash",
			Names: []string{"BAR", "FOO"},
		},
	})

	got := currentEnvState(slug, nil)

	if got.Hash != "sha256:testenvhash" {
		t.Errorf("EnvState.Hash = %q, want %q", got.Hash, "sha256:testenvhash")
	}
	if len(got.Names) != 2 || got.Names[0] != "BAR" || got.Names[1] != "FOO" {
		t.Errorf("EnvState.Names = %v, want [BAR, FOO]", got.Names)
	}
}

func TestCurrentEnvState_IgnoresReadError(t *testing.T) {
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-sandbox")
	slug := "badproj"

	// Corrupted YAML that returns a parser error (not ErrStateNotFound):
	sdir := filepath.Join(state.StateDir, slug)
	os.MkdirAll(sdir, 0o700)
	if err := os.WriteFile(filepath.Join(sdir, "state.yaml"), []byte("!!broken: yaml: [invalid"), 0o600); err != nil {
		t.Fatal(err)
	}

	ui := &termio.Mock{}
	got := currentEnvState(slug, ui)

	if got.Hash != "" {
		t.Errorf("expected empty hash on read error, got %q", got.Hash)
	}
	if len(ui.WarnCalls) != 1 {
		t.Errorf("expected 1 warn call on read error, got %d", len(ui.WarnCalls))
	}
}

func TestCurrentSecretState_NotFoundReturnsZero(t *testing.T) {
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-sandbox")
	slug := "nonexistent"

	got := currentSecretState(slug, &termio.Mock{})

	if got.Hash != "" {
		t.Errorf("expected empty hash, got %q", got.Hash)
	}
	if got.Names != nil {
		t.Errorf("expected nil names, got %v", got.Names)
	}
}

func TestCurrentSecretState_ReadsPersisted(t *testing.T) {
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-sandbox")
	slug := "testproj-abc2"

	state.WriteState(slug, state.HomeState{
		SecretState: state.SecretState{
			Hash:  "sha256:testsecrethash",
			Names: []string{"DB_PASSWORD", "API_KEY"},
		},
	})

	got := currentSecretState(slug, nil)

	if got.Hash != "sha256:testsecrethash" {
		t.Errorf("SecretState.Hash = %q, want %q", got.Hash, "sha256:testsecrethash")
	}
	if len(got.Names) != 2 {
		t.Errorf("SecretState.Names length = %d, want 2", len(got.Names))
	}
}

func TestPersistEnvSecrets_RoundTrip(t *testing.T) {
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-sandbox")
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
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-sandbox")
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
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-sandbox")
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
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-sandbox")
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

func TestPersistEnvSecrets_ReadFailsReturnsError(t *testing.T) {
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-sandbox")
	slug := "failproj"

	// Corrupted YAML that returns an error (not ErrStateNotFound):
	sdir := filepath.Join(state.StateDir, slug)
	os.MkdirAll(sdir, 0o700)
	if err := os.WriteFile(filepath.Join(sdir, "state.yaml"), []byte("{ corrupted: yaml: ["), 0o600); err != nil {
		t.Fatal(err)
	}

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
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-sandbox")

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

	ui := testutil.TermUIMock(t)
	recreate, restart, _, err := decideReconfig(
		context.Background(),
		mock,
		vm,
		options.RunOptions{},
		"img:tag",
		"sha256:samedigest",
		"vol",
		persistedState,
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
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-sandbox")
	userDir := t.TempDir()

	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)
	configpaths.WithMockConfigPaths(t)

	vm := volume.NewManager(&termio.Mock{})

	// Write env file and state with matching hash
	testutil.WritePath(t, filepath.Join(userDir, configpaths.EnvFileName), "FOO=bar\n")
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

	ui := testutil.TermUIMock(t)
	recreate, restart, _, err := decideReconfig(
		context.Background(),
		mock,
		vm,
		options.RunOptions{},
		"img:tag",
		"sha256:samedigest",
		"vol",
		persistedState,
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
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-sandbox")

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

	ui := testutil.TermUIMock(t)
	recreate, restart, _, err := decideReconfig(
		context.Background(),
		mock,
		vm,
		options.RunOptions{},
		"img:tag",
		"sha256:samedigest",
		"vol",
		persistedState,
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
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-sandbox")
	userDir := t.TempDir()

	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)
	configpaths.WithMockConfigPaths(t)

	vm := volume.NewManager(&termio.Mock{})

	// Write empty env file → desired is empty.
	// State has no env_state → zero value (first run).
	// Zero applied + empty desired → NOT changed (per ruling)
	testutil.WritePath(t, filepath.Join(userDir, configpaths.EnvFileName), "# nothing here\n")

	ui := testutil.TermUIMock(t)
	recreate, restart, _, err := decideReconfig(
		context.Background(),
		mock,
		vm,
		options.RunOptions{},
		"img:tag",
		"sha256:d1",
		"vol",
		state.HomeState{},
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
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-sandbox")
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
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-sandbox")
	slug := "omittestproj"

	envSt := state.EnvState{Hash: "h1", Names: []string{"X"}}
	secSt := state.SecretState{}

	if err := persistEnvSecrets(slug, envSt, secSt); err != nil {
		t.Fatalf("persistEnvSecrets: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(state.StateDir, slug, "state.yaml"))
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	content := string(data)
	if !containsStr(content, "env_state") {
		t.Error("expected env_state in YAML")
	}
}

func TestCurrentStates_PersistedBothFields(t *testing.T) {
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-sandbox")
	slug := "bothproj"

	state.WriteState(slug, state.HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:img",
		EnvState:    state.EnvState{Hash: "eh", Names: []string{"A"}},
		SecretState: state.SecretState{Hash: "sh", Names: []string{"B"}},
	})

	e := currentEnvState(slug, nil)
	if e.Hash != "eh" {
		t.Errorf("EnvState.Hash = %q, want %q", e.Hash, "eh")
	}
	s := currentSecretState(slug, nil)
	if s.Hash != "sh" {
		t.Errorf("SecretState.Hash = %q, want %q", s.Hash, "sh")
	}
}

func TestPersistEnvSecrets_ZeroStateOverwritesOnlyFields(t *testing.T) {
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-sandbox")
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
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-sandbox")
	userDir := t.TempDir()

	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)
	configpaths.WithMockConfigPaths(t)

	vm := volume.NewManager(&termio.Mock{})

	testutil.WritePath(t, filepath.Join(userDir, configpaths.EnvFileName), "K=V\n")
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

	ui := testutil.TermUIMock(t)
	recreate, restart, _, err := decideReconfig(
		context.Background(),
		mock,
		vm,
		options.RunOptions{},
		"img", "sha256:img", "vol",
		persistedState,
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
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-sandbox")

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

	recreate, restart, homeVol, err := decideReconfig(
		context.Background(),
		mock,
		vm,
		options.RunOptions{},
		"img:tag",    // imageRef == cfg.Image -> no image-triggered rebuild
		"sha256:new", // stored digest differs -> image change detected, but rebuild deferred
		"vol",
		persisted,
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
	state.SetStateDirForTest(t, t.TempDir()+"/opencode-sandbox")

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
	slug := git.ProjectSlug(ui)
	state.WriteState(slug, persisted)

	recreate, restart, homeVol, err := decideReconfig(
		context.Background(),
		mock,
		vm,
		options.RunOptions{},
		"img:new",    // imageRef != cfg.Image("img:tag") -> image-triggered rebuild
		"sha256:new", // stored digest differs -> image change detected
		"vol",
		persisted,
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
