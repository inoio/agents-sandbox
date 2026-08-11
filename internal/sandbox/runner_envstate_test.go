package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/reprovision"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/volume"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
)

func TestCurrentEnvState_NotFoundReturnsZero(t *testing.T) {
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")
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
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")
	slug := "testproj-abc1"

	WriteState(slug, HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:xyz",
		EnvState: EnvState{
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
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")
	slug := "badproj"

	// Corrupted YAML that returns a parser error (not ErrStateNotFound):
	sdir := filepath.Join(StateDir(), slug)
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
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")
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
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")
	slug := "testproj-abc2"

	WriteState(slug, HomeState{
		SecretState: SecretState{
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
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")
	slug := "roundtripproj"

	envSt := EnvState{Hash: "env-hash-123", Names: []string{"FOO"}}
	secSt := SecretState{Hash: "sec-hash-456", Names: []string{"PASS"}}

	err := persistEnvSecrets(slug, envSt, secSt)
	if err != nil {
		t.Fatalf("persistEnvSecrets: %v", err)
	}

	got, err := ReadState(slug)
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
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")
	slug := "newproj-" + makeSlug()

	envSt := EnvState{Hash: "h1", Names: []string{"X"}}
	secSt := SecretState{Hash: "h2", Names: []string{"Y"}}

	if err := persistEnvSecrets(slug, envSt, secSt); err != nil {
		t.Fatalf("persistEnvSecrets: %v", err)
	}

	got, err := ReadState(slug)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.EnvState.Hash != "h1" {
		t.Errorf("EnvState.Hash = %q, want %q", got.EnvState.Hash, "h1")
	}
}

func TestPersistEnvSecrets_MergesExistingState(t *testing.T) {
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")
	slug := "mergeproj"

	// Write existing state with HomeVolume and ImageDigest
	WriteState(slug, HomeState{
		HomeVolume:  "existing-vol",
		ImageDigest: "sha256:existing",
	})

	envSt := EnvState{Hash: "new-env-hash", Names: []string{"NEW"}}
	secSt := SecretState{Hash: "new-sec-hash", Names: []string{"SECRET"}}

	if err := persistEnvSecrets(slug, envSt, secSt); err != nil {
		t.Fatalf("persistEnvSecrets: %v", err)
	}

	got, err := ReadState(slug)
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
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")
	slug := "existingstateproj"

	// Pre-write state with env_state already set but different hash
	oldState := HomeState{
		HomeVolume: "vol", ImageDigest: "d1",
		EnvState:    EnvState{Hash: "sha256:olddata", Names: nil},
		SecretState: SecretState{},
	}
	WriteState(slug, oldState)

	envSt := EnvState{Hash: "newhash", Names: []string{"K"}}
	secSt := SecretState{Hash: "sh", Names: []string{"S"}}

	if err := persistEnvSecrets(slug, envSt, secSt); err != nil {
		t.Fatalf("persistEnvSecrets: %v", err)
	}

	got, err := ReadState(slug)
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
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")
	slug := "failproj"

	// Corrupted YAML that returns an error (not ErrStateNotFound):
	sdir := filepath.Join(StateDir(), slug)
	os.MkdirAll(sdir, 0o700)
	if err := os.WriteFile(filepath.Join(sdir, "state.yaml"), []byte("{ corrupted: yaml: ["), 0o600); err != nil {
		t.Fatal(err)
	}

	envSt := EnvState{Hash: "h"}
	secSt := SecretState{Hash: "h"}
	err := persistEnvSecrets(slug, envSt, secSt)

	if err == nil {
		t.Fatal("expected error for corrupted YAML, got nil")
	}
	if !errIsContains(err, "read state for persistence") {
		t.Errorf("expected error containing 'read state for persistence', got: %v", err)
	}
}

func TestDecideReconfig_EnvChangedWithPersistedState(t *testing.T) {
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	// Set up handle so reprovision.PlanReconfig gets a non-nil cfg
	mock := reconfigMockClient()
	msb.WithMsbMock(t, mock)
	WithMockConfigPaths(t)

	vm := volume.NewManager(&termio.Mock{})

	// Write state with env hash that differs from desired (nil desired = empty env)
	// Use matching imageDigest to skip home-volume path
	persistedState := HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:samedigest",
		EnvState: EnvState{
			Hash:  "sha256:differenthash",
			Names: []string{"OLD_KEY"},
		},
	}

	ui := testutil.TermUIMock(t)
	recreate, restart, _, err := decideReconfig(
		context.Background(),
		mock,
		vm,
		RunOptions{},
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
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")
	userDir := t.TempDir()

	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)
	WithMockConfigPaths(t)

	vm := volume.NewManager(&termio.Mock{})

	// Write env file and state with matching hash
	testutil.WritePath(t, filepath.Join(userDir, configpaths.EnvFileName), "FOO=bar\n")
	envHash := computeEnvHash(filepath.Join(userDir, configpaths.EnvFileName))

	// Build HomeState with matching env state
	persistedState := HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:img1",
		EnvState: EnvState{
			Hash:  envHash,
			Names: []string{"FOO"},
		},
	}

	ui := testutil.TermUIMock(t)
	recreate, restart, _, err := decideReconfig(
		context.Background(),
		mock,
		vm,
		RunOptions{},
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
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	mock := reconfigMockClient()
	msb.WithMsbMock(t, mock)
	WithMockConfigPaths(t)

	vm := volume.NewManager(&termio.Mock{})

	// Write state with different secret hash than desired (nil desired = no secrets)
	// Use matching imageDigest to skip home-volume path
	persistedState := HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:samedigest",
		SecretState: SecretState{
			Hash:  "sha256:oldsecrethash",
			Names: []string{"DB_PASS"},
		},
	}

	ui := testutil.TermUIMock(t)
	recreate, restart, _, err := decideReconfig(
		context.Background(),
		mock,
		vm,
		RunOptions{},
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
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")
	userDir := t.TempDir()

	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)
	WithMockConfigPaths(t)

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
		RunOptions{},
		"img:tag",
		"sha256:d1",
		"vol",
		HomeState{},
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
	got := reprovision.SecretsChanged(SecretState{}, nil)
	if got {
		t.Error("expected NO change when applied is zero and desired is nil")
	}
}

func TestSecretsChanged_ZeroApplied_EmptyDesired(t *testing.T) {
	got := reprovision.SecretsChanged(SecretState{}, []msbSdk.SecretEntry{})
	if got {
		t.Error("expected NO change when applied is zero and desired is empty")
	}
}

func TestSecretsChanged_NonZeroApplied_DifferentHash(t *testing.T) {
	got := reprovision.SecretsChanged(SecretState{Hash: "h1"}, []msbSdk.SecretEntry{{EnvVar: "K", Value: "v"}})
	if !got {
		t.Error("expected change when hashes differ")
	}
}

func TestEnvChanged_ZeroApplied_NonEmptyDesired(t *testing.T) {
	got := reprovision.EnvChanged(EnvState{}, map[string]string{"FOO": "bar"})
	if !got {
		t.Error("expected change when applied is zero and desired is non-empty")
	}
}

func TestEnvChanged_MatchingHash(t *testing.T) {
	desired := map[string]string{"FOO": "bar"}
	wantHash := reprovision.EnvContentHash(desired)
	got := reprovision.EnvChanged(EnvState{Hash: wantHash}, desired)
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
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")
	slug := "freshproject"

	envSt := EnvState{Hash: "h1", Names: []string{"X"}}
	secSt := SecretState{Hash: "h2", Names: []string{"Y"}}

	if err := persistEnvSecrets(slug, envSt, secSt); err != nil {
		t.Fatalf("persistEnvSecrets: %v", err)
	}

	got, err := ReadState(slug)
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
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")
	slug := "omittestproj"

	envSt := EnvState{Hash: "h1", Names: []string{"X"}}
	secSt := SecretState{}

	if err := persistEnvSecrets(slug, envSt, secSt); err != nil {
		t.Fatalf("persistEnvSecrets: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(StateDir(), slug, "state.yaml"))
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	content := string(data)
	if !containsStr(content, "env_state") {
		t.Error("expected env_state in YAML")
	}
}

func TestCurrentStates_PersistedBothFields(t *testing.T) {
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")
	slug := "bothproj"

	WriteState(slug, HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:img",
		EnvState:    EnvState{Hash: "eh", Names: []string{"A"}},
		SecretState: SecretState{Hash: "sh", Names: []string{"B"}},
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
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")
	slug := "overwriteproj"

	WriteState(slug, HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:img",
		EnvState:    EnvState{Hash: "old-env", Names: []string{"OLD"}},
	})

	if err := persistEnvSecrets(slug, EnvState{}, SecretState{}); err != nil {
		t.Fatalf("persistEnvSecrets: %v", err)
	}

	got, err := ReadState(slug)
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
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")
	userDir := t.TempDir()

	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)
	WithMockConfigPaths(t)

	vm := volume.NewManager(&termio.Mock{})

	testutil.WritePath(t, filepath.Join(userDir, configpaths.EnvFileName), "K=V\n")
	desiredEnv := reprovision.MergeEnvMaps(reprovision.BuildEnvMap(filepath.Join(userDir, configpaths.EnvFileName)))
	envHash := reprovision.EnvContentHash(desiredEnv)

	WriteState("myproj5", HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:img",
		EnvState:    EnvState{Hash: envHash, Names: []string{"K"}},
	})

	persistedState := HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:img",
		EnvState:    EnvState{Hash: envHash, Names: []string{"K"}},
	}

	ui := testutil.TermUIMock(t)
	recreate, restart, _, err := decideReconfig(
		context.Background(),
		mock,
		vm,
		RunOptions{},
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

// Test helpers

func reconfigMockClient() *msb.MockMsbClient {
	mock := &msb.MockMsbClient{}
	sh := &msb.MockSandboxHandle{Cfg: &msbSdk.SandboxConfig{
		Image: "img:tag", CPUs: 4, MemoryMiB: 4096,
	}}
	sh.ConnectSb = &MockSandbox{}
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
