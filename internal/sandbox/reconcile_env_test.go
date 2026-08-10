package sandbox

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

func assertNamesSorted(t *testing.T, names []string) {
	t.Helper()
	if len(names) == 0 {
		t.Fatal("names should not be empty")
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Errorf("names not sorted at index %d: %q >= %q", i, names[i-1], names[i])
		}
	}
}

func assertModifyEnv(t *testing.T, mo *msbSdk.ModifyOptions, wantEnv map[string]string, wantRemove []string) {
	t.Helper()
	for name, want := range wantEnv {
		if got := mo.Env[name]; got != want {
			t.Errorf("expected Env[%s]=%q, got %q", name, want, got)
		}
	}
	for _, name := range wantRemove {
		if !slices.Contains(mo.EnvRemove, name) {
			t.Errorf("expected EnvRemove to contain %q, got %v", name, mo.EnvRemove)
		}
	}
}

// --- Tests for Task 2: content-hash + change-detection helpers ---

func TestEnvContentHashOrderIndependence(t *testing.T) {
	a := map[string]string{"A": "1", "B": "2", "C": "3"}
	b := map[string]string{"C": "3", "A": "1", "B": "2"}
	ha := envContentHash(a)
	hb := envContentHash(b)
	if ha != hb {
		t.Errorf("order-independence: got %x vs %x", ha, hb)
	}
}

func TestEnvContentHashNonEmpty(t *testing.T) {
	h := envContentHash(map[string]string{"A": "1"})
	if h == "" {
		t.Error("expected non-empty hash")
	}
	if len(h) != 64 {
		t.Errorf("expected 64-char hex SHA-256, got len=%d", len(h))
	}
}

func TestEnvContentHashDiffersForDifferentValues(t *testing.T) {
	h1 := envContentHash(map[string]string{"A": "1"})
	h2 := envContentHash(map[string]string{"A": "2"})
	if h1 == h2 {
		t.Error("different values should produce different hashes")
	}
}

func TestEnvContentHashEmptyIsStable(t *testing.T) {
	h1 := envContentHash(nil)
	h2 := envContentHash(map[string]string{})
	if h1 != h2 {
		t.Error("nil and empty map should produce the same hash")
	}
}

func TestSecretsContentHashOrderIndependence(t *testing.T) {
	a := []msbSdk.SecretEntry{
		{EnvVar: "A", Value: "1"},
		{EnvVar: "B", Value: "2"},
	}
	b := []msbSdk.SecretEntry{
		{EnvVar: "B", Value: "2"},
		{EnvVar: "A", Value: "1"},
	}
	ha := secretsContentHash(a)
	hb := secretsContentHash(b)
	if ha != hb {
		t.Errorf("order-independence: got %x vs %x", ha, hb)
	}
}

func TestSecretsContentHashValueChange(t *testing.T) {
	a := []msbSdk.SecretEntry{{EnvVar: "X", Value: "old"}}
	b := []msbSdk.SecretEntry{{EnvVar: "X", Value: "new"}}
	ha := secretsContentHash(a)
	hb := secretsContentHash(b)
	if ha == hb {
		t.Error("value change should alter secretsContentHash")
	}
}

func TestSecretsContentHashEmptyStable(t *testing.T) {
	h1 := secretsContentHash(nil)
	h2 := secretsContentHash([]msbSdk.SecretEntry{})
	if h1 != h2 {
		t.Error("nil and empty should produce the same hash")
	}
}

func TestEnvChangedFalseWhenIdentical(t *testing.T) {
	desired := map[string]string{"A": "1", "B": "2"}
	state := buildEnvState(desired)
	if envChanged(state, desired) {
		t.Error("envChanged should be false when identical")
	}
}

func TestEnvChangedTrueOnAdd(t *testing.T) {
	state := buildEnvState(map[string]string{"A": "1"})
	if !envChanged(state, map[string]string{"A": "1", "B": "2"}) {
		t.Error("envChanged should be true when a key is added")
	}
}

func TestEnvChangedTrueOnChange(t *testing.T) {
	state := buildEnvState(map[string]string{"A": "1"})
	if !envChanged(state, map[string]string{"A": "2"}) {
		t.Error("envChanged should be true when a value changes")
	}
}

func TestEnvChangedTrueOnRemove(t *testing.T) {
	state := buildEnvState(map[string]string{"A": "1", "B": "2"})
	if !envChanged(state, map[string]string{"A": "1"}) {
		t.Error("envChanged should be true when a key is removed")
	}
}

func TestSecretsChangedFalseWhenIdentical(t *testing.T) {
	entries := []msbSdk.SecretEntry{{EnvVar: "X", Value: "v"}}
	state := buildSecretState(entries)
	if secretsChanged(state, entries) {
		t.Error("secretsChanged should be false when identical")
	}
}

func TestSecretsChangedTrueOnValueChange(t *testing.T) {
	state := buildSecretState([]msbSdk.SecretEntry{{EnvVar: "X", Value: "v1"}})
	if !secretsChanged(state, []msbSdk.SecretEntry{{EnvVar: "X", Value: "v2"}}) {
		t.Error("secretsChanged should be true when value changes")
	}
}

func TestSecretsChangedTrueOnAdd(t *testing.T) {
	state := buildSecretState([]msbSdk.SecretEntry{{EnvVar: "X", Value: "v"}})
	if !secretsChanged(state, []msbSdk.SecretEntry{
		{EnvVar: "X", Value: "v"},
		{EnvVar: "Y", Value: "w"},
	}) {
		t.Error("secretsChanged should be true when a secret is added")
	}
}

func TestSecretsChangedTrueOnRemove(t *testing.T) {
	state := buildSecretState([]msbSdk.SecretEntry{
		{EnvVar: "X", Value: "v"},
		{EnvVar: "Y", Value: "w"},
	})
	if !secretsChanged(state, []msbSdk.SecretEntry{{EnvVar: "X", Value: "v"}}) {
		t.Error("secretsChanged should be true when a secret is removed")
	}
}

func TestBuildEnvStateSortedNames(t *testing.T) {
	desired := map[string]string{"Z": "1", "A": "2", "M": "3"}
	state := buildEnvState(desired)
	assertNamesSorted(t, state.Names)
}

func TestBuildEnvStateHashMatchesContentHash(t *testing.T) {
	desired := map[string]string{"Z": "1", "A": "2"}
	state := buildEnvState(desired)
	h := envContentHash(desired)
	if state.Hash != h {
		t.Errorf("buildEnvState hash mismatch: got %s, want %s", state.Hash, h)
	}
}

func TestBuildSecretStateSortedNames(t *testing.T) {
	entries := []msbSdk.SecretEntry{{EnvVar: "Z", Value: "1"}, {EnvVar: "A", Value: "2"}}
	state := buildSecretState(entries)
	assertNamesSorted(t, state.Names)
}

func TestBuildSecretStateHashMatchesContentHash(t *testing.T) {
	entries := []msbSdk.SecretEntry{{EnvVar: "Z", Value: "1"}, {EnvVar: "A", Value: "2"}}
	state := buildSecretState(entries)
	h := secretsContentHash(entries)
	if state.Hash != h {
		t.Errorf("buildSecretState hash mismatch: got %s, want %s", state.Hash, h)
	}
}

func TestBuildEnvStateEmpty(t *testing.T) {
	state := buildEnvState(nil)
	if state.Hash == "" {
		t.Error("empty env should still produce a hash")
	}
	if len(state.Names) != 0 {
		t.Errorf("expected no names, got %v", state.Names)
	}
}

func TestBuildSecretStateEmpty(t *testing.T) {
	state := buildSecretState(nil)
	if state.Hash == "" {
		t.Error("empty secrets should still produce a hash")
	}
	if len(state.Names) != 0 {
		t.Errorf("expected no names, got %v", state.Names)
	}
}

func TestSecretsContentHashOrderStableAcrossBuildSecretState(t *testing.T) {
	a := []msbSdk.SecretEntry{{EnvVar: "Z", Value: "1"}, {EnvVar: "A", Value: "2"}, {EnvVar: "M", Value: "3"}}
	b := []msbSdk.SecretEntry{{EnvVar: "M", Value: "3"}, {EnvVar: "Z", Value: "1"}, {EnvVar: "A", Value: "2"}}
	ha := buildSecretState(a).Hash
	hb := buildSecretState(b).Hash
	if ha != hb {
		t.Errorf("order-independence via buildSecretState: got %x vs %x", ha, hb)
	}
}

// --- Tests for Task 3: apply spec helpers ---

func TestApplyEnvSpecEnvSetCorrectly(t *testing.T) {
	applied := buildEnvState(map[string]string{"A": "1", "B": "2"})
	desired := map[string]string{"B": "5", "C": "3"}

	var mo msbSdk.ModifyOptions
	applyEnvSpec(applied, desired, &mo)

	if len(mo.Env) != 2 {
		t.Fatalf("expected 2 env entries, got %d", len(mo.Env))
	}
	assertModifyEnv(t, &mo, map[string]string{"B": "5", "C": "3"}, []string{"A"})
}

func TestApplyEnvSpecNoRemoveWhenAllStay(t *testing.T) {
	applied := buildEnvState(map[string]string{"A": "1", "B": "2"})
	desired := map[string]string{"A": "1", "B": "2"}

	var mo msbSdk.ModifyOptions
	applyEnvSpec(applied, desired, &mo)

	if len(mo.EnvRemove) != 0 {
		t.Errorf("expected no EnvRemove entries, got %v", mo.EnvRemove)
	}
}

func TestApplyEnvSpecEmptyMap(t *testing.T) {
	applied := buildEnvState(map[string]string{"A": "1"})
	desired := map[string]string{}

	var mo msbSdk.ModifyOptions
	applyEnvSpec(applied, desired, &mo)

	// Empty map means "no env vars" — Env set to empty
	if len(mo.Env) != 0 {
		t.Errorf("expected empty Env, got %v", mo.Env)
	}
	// All applied names removed since not in desired
	if !slices.Contains(mo.EnvRemove, "A") {
		t.Errorf("expected EnvRemove=[A], got %v", mo.EnvRemove)
	}
}

func TestApplySecretSpecSecretsSetCorrectly(t *testing.T) {
	applied := buildSecretState([]msbSdk.SecretEntry{{EnvVar: "S"}})
	desired := []msbSdk.SecretEntry{
		msbSdk.Secret.Env("NEW", "v", msbSdk.SecretEnvOptions{AllowHosts: []string{"host"}}),
	}

	var mo msbSdk.ModifyOptions
	applySecretSpec(applied, desired, &mo)

	spec, ok := mo.Secrets["NEW"]
	if !ok {
		t.Fatalf("expected Secrets to include NEW, got %+v", mo.Secrets)
	}
	if spec.Value != "v" {
		t.Errorf("expected secret spec Value=v, got %q", spec.Value)
	}
	if !slices.Contains(mo.SecretsRemove, "S") {
		t.Errorf("expected SecretsRemove=[S], got %v", mo.SecretsRemove)
	}
}

func TestApplySecretSpecNoRemoveWhenAllStay(t *testing.T) {
	applied := buildSecretState([]msbSdk.SecretEntry{{EnvVar: "S"}})
	desired := []msbSdk.SecretEntry{{EnvVar: "S", Value: "val"}}

	var mo msbSdk.ModifyOptions
	applySecretSpec(applied, desired, &mo)

	if len(mo.SecretsRemove) != 0 {
		t.Errorf("expected no SecretsRemove, got %v", mo.SecretsRemove)
	}
}

func TestApplySecretSpecAllRemoved(t *testing.T) {
	applied := buildSecretState([]msbSdk.SecretEntry{{EnvVar: "OLD"}})
	var desired []msbSdk.SecretEntry

	var mo msbSdk.ModifyOptions
	applySecretSpec(applied, desired, &mo)

	if mo.Secrets == nil {
		t.Error("expected Secrets to be non-nil empty map, got nil")
	}
	if len(mo.SecretsRemove) != 1 || mo.SecretsRemove[0] != "OLD" {
		t.Errorf("expected SecretsRemove=[OLD], got %v", mo.SecretsRemove)
	}
}

func TestMultiChangeEnvRemovedAddedChanged(t *testing.T) {
	// applied: {A=1, B=2}, desired: {B=5, C=3} → A removed, B changed, C added
	appliedEnv := buildEnvState(map[string]string{"A": "1", "B": "2"})
	appliedSecrets := buildSecretState([]msbSdk.SecretEntry{{EnvVar: "S"}})
	desiredEnv := map[string]string{"B": "5", "C": "3"}
	var desiredSecrets []msbSdk.SecretEntry

	mock := &MockSandboxHandle{Plan: &msbSdk.SandboxModificationPlan{Applied: true}}
	changed, envSt, secretSt, err := reconcileEnvAndSecrets(
		context.Background(), mock, desiredEnv, desiredSecrets, appliedEnv, appliedSecrets,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed=true")
	}

	mo := mock.ModifiedOptions[0]

	assertModifyEnv(t, &mo, map[string]string{"B": "5", "C": "3"}, []string{"A"})

	// Secrets: specs set (empty map), SecretsRemove for stale S
	if len(mo.SecretsRemove) != 1 || mo.SecretsRemove[0] != "S" {
		t.Errorf("expected SecretsRemove=[S], got %v", mo.SecretsRemove)
	}
	if len(mo.Secrets) != 0 {
		t.Errorf("expected empty Secrets, got %+v", mo.Secrets)
	}

	// Returned states match build env/secret state
	wantEnv := buildEnvState(desiredEnv)
	wantSecret := buildSecretState(desiredSecrets)
	if envSt.Hash != wantEnv.Hash {
		t.Errorf("returned envState hash mismatch: got %q, want %q", envSt.Hash, wantEnv.Hash)
	}
	if !slices.Equal(envSt.Names, wantEnv.Names) {
		t.Errorf("returned envState names mismatch: got %v, want %v", envSt.Names, wantEnv.Names)
	}
	if secretSt.Hash != wantSecret.Hash {
		t.Errorf("returned secretState hash mismatch: got %q, want %q", secretSt.Hash, wantSecret.Hash)
	}
	if !slices.Equal(secretSt.Names, wantSecret.Names) {
		t.Errorf("returned secretState names mismatch: got %v, want %v", secretSt.Names, wantSecret.Names)
	}
}

func TestReconcileChangedTriggersOneModify(t *testing.T) {
	desiredEnv := map[string]string{"NEW": "val"}
	desiredSecrets := []msbSdk.SecretEntry{
		msbSdk.Secret.Env("S", "v", msbSdk.SecretEnvOptions{AllowHosts: []string{"h"}}),
	}

	mock := &MockSandboxHandle{Plan: &msbSdk.SandboxModificationPlan{Applied: true}}
	changed, envState, secretState, err := reconcileEnvAndSecrets(
		context.Background(), mock, desiredEnv, desiredSecrets,
		EnvState{}, SecretState{}, // zero applied state
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed=true")
	}
	if len(mock.ModifiedOptions) != 1 {
		t.Fatalf("expected 1 Modify call, got %d", len(mock.ModifiedOptions))
	}

	mo := mock.ModifiedOptions[0]
	if mo.Env["NEW"] != "val" {
		t.Errorf("expected Env[NEW]=val, got %+v", mo.Env)
	}
	if len(mo.EnvRemove) != 0 {
		t.Errorf("expected no EnvRemove for zero applied state, got %v", mo.EnvRemove)
	}

	spec, ok := mo.Secrets["S"]
	if !ok {
		t.Fatalf("expected Secrets[S], got %+v", mo.Secrets)
	}
	if spec.Value != "v" {
		t.Errorf("expected secret spec Value=v, got %q", spec.Value)
	}

	wantEnv := buildEnvState(desiredEnv)
	wantSecret := buildSecretState(desiredSecrets)
	if envState.Hash != wantEnv.Hash {
		t.Errorf("returned envState hash: got %q, want %q", envState.Hash, wantEnv.Hash)
	}
	if secretState.Hash != wantSecret.Hash {
		t.Errorf("returned secretState hash: got %q, want %q", secretState.Hash, wantSecret.Hash)
	}
}

func TestReconcileEnvNormalizedEmptyRemovesStale(t *testing.T) {
	// Tests the nil→empty normalization boundary: applied env has names but
	// desired is nil. Reconcile should detect change and remove all stale keys.
	appliedEnv := buildEnvState(map[string]string{"A": "1", "B": "2", "C": "3"})

	mock := &MockSandboxHandle{Plan: &msbSdk.SandboxModificationPlan{Applied: true}}
	changed, envSt, _, err := reconcileEnvAndSecrets(
		context.Background(), mock, nil, nil, appliedEnv, SecretState{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed=true (stale keys should be removed)")
	}
	if len(mock.ModifiedOptions) != 1 {
		t.Fatalf("expected 1 Modify call, got %d", len(mock.ModifiedOptions))
	}

	mo := mock.ModifiedOptions[0]
	// Env should be set to empty map
	if mo.Env == nil {
		t.Fatal("expected mo.Env to be set to empty map, got nil")
	}
	if len(mo.Env) != 0 {
		t.Errorf("expected Env to be empty, got %v", mo.Env)
	}
	// All applied names should be in EnvRemove
	for _, name := range appliedEnv.Names {
		if !slices.Contains(mo.EnvRemove, name) {
			t.Errorf("expected EnvRemove to contain %q, got %v", name, mo.EnvRemove)
		}
	}
	// Returned envState should be buildEnvState(nil) (empty)
	wantEnv := buildEnvState(nil)
	if envSt.Hash != wantEnv.Hash {
		t.Errorf("returned envState hash: got %q, want %q", envSt.Hash, wantEnv.Hash)
	}
	if len(envSt.Names) != 0 {
		t.Errorf("expected zero names, got %v", envSt.Names)
	}
	// SecretState is zero (unchanged)
}

func TestReconcileEmptyMapDesiredRemovesStale(t *testing.T) {
	// Same scenario as above but with explicit empty map instead of nil.
	appliedEnv := buildEnvState(map[string]string{"A": "1", "B": "2"})

	mock := &MockSandboxHandle{Plan: &msbSdk.SandboxModificationPlan{Applied: true}}
	changed, envSt, _, err := reconcileEnvAndSecrets(
		context.Background(), mock, map[string]string{}, nil, appliedEnv, SecretState{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed=true")
	}
	if len(mock.ModifiedOptions) != 1 {
		t.Fatalf("expected 1 Modify, got %d", len(mock.ModifiedOptions))
	}

	mo := mock.ModifiedOptions[0]
	if mo.Env == nil {
		t.Fatal("expected mo.Env to be set, got nil")
	}
	for _, name := range appliedEnv.Names {
		if !slices.Contains(mo.EnvRemove, name) {
			t.Errorf("expected EnvRemove to contain %q, got %v", name, mo.EnvRemove)
		}
	}
	wantEnv := buildEnvState(map[string]string{})
	if envSt.Hash != wantEnv.Hash {
		t.Errorf("hash mismatch: got %q, want %q", envSt.Hash, wantEnv.Hash)
	}
}

func TestReconcileNoopWhenSame(t *testing.T) {
	desiredEnv := map[string]string{"A": "1", "B": "2"}
	desiredSecrets := []msbSdk.SecretEntry{{EnvVar: "S", Value: "v"}}
	appliedEnv := buildEnvState(desiredEnv)
	appliedSecrets := buildSecretState(desiredSecrets)

	mock := &MockSandboxHandle{}
	changed, envSt, secretSt, err := reconcileEnvAndSecrets(
		context.Background(), mock, desiredEnv, desiredSecrets,
		appliedEnv, appliedSecrets,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected changed=false")
	}
	if len(mock.ModifiedOptions) != 0 {
		t.Errorf("expected no Modify call, got %d", len(mock.ModifiedOptions))
	}
	if envSt.Hash != appliedEnv.Hash {
		t.Errorf("returned envState != applied envState: got %q, want %q", envSt.Hash, appliedEnv.Hash)
	}
	if secretSt.Hash != appliedSecrets.Hash {
		t.Errorf("returned secretState != applied secretState: got %q, want %q", secretSt.Hash, appliedSecrets.Hash)
	}
}

func TestReconcileNilHandleNoop(t *testing.T) {
	changed, envSt, secretSt, err := reconcileEnvAndSecrets(
		context.Background(), nil,
		map[string]string{"X": "1"},
		[]msbSdk.SecretEntry{{EnvVar: "S", Value: "v"}},
		EnvState{}, SecretState{},
	)
	if changed {
		t.Error("expected changed=false with nil handle")
	}
	if envSt.Hash != "" {
		t.Error("expected zero envState with nil handle")
	}
	if secretSt.Hash != "" {
		t.Error("expected zero secretState with nil handle")
	}
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestReconcileEnvOnly(t *testing.T) {
	desiredEnv := map[string]string{"NEW": "val"}
	appliedEnv := buildEnvState(map[string]string{"OLD": "old"})

	mock := &MockSandboxHandle{Plan: &msbSdk.SandboxModificationPlan{Applied: true}}
	changed, envState, secretState, err := reconcileEnvAndSecrets(
		context.Background(), mock, desiredEnv, nil, appliedEnv, SecretState{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed=true")
	}
	if len(mock.ModifiedOptions) != 1 {
		t.Fatalf("expected 1 Modify call, got %d", len(mock.ModifiedOptions))
	}

	mo := mock.ModifiedOptions[0]
	if mo.Env["NEW"] != "val" {
		t.Errorf("expected Env[NEW]=val, got %+v", mo.Env)
	}
	if !slices.Contains(mo.EnvRemove, "OLD") {
		t.Errorf("expected EnvRemove=[OLD], got %v", mo.EnvRemove)
	}
	if len(mo.Secrets) != 0 {
		t.Errorf("expected nil/empty Secrets, got %+v", mo.Secrets)
	}
	wantEnv := buildEnvState(desiredEnv)
	if envState.Hash != wantEnv.Hash {
		t.Errorf("hash mismatch: got %q, want %q", envState.Hash, wantEnv.Hash)
	}
	if secretState.Hash != "" {
		t.Error("expected zero secretState")
	}
}

func TestReconcileSecretsOnly(t *testing.T) {
	desiredSecrets := []msbSdk.SecretEntry{{EnvVar: "NEW", Value: "v"}}
	appliedSecrets := buildSecretState([]msbSdk.SecretEntry{{EnvVar: "OLD", Value: "x"}})

	mock := &MockSandboxHandle{Plan: &msbSdk.SandboxModificationPlan{Applied: true}}
	changed, envState, secretState, err := reconcileEnvAndSecrets(
		context.Background(), mock, nil, desiredSecrets, EnvState{}, appliedSecrets,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed=true")
	}
	if len(mock.ModifiedOptions) != 1 {
		t.Fatalf("expected 1 Modify call, got %d", len(mock.ModifiedOptions))
	}

	mo := mock.ModifiedOptions[0]
	if mo.Secrets == nil || len(mo.Secrets) != 1 {
		t.Fatalf("expected Secrets[NEW], got %+v", mo.Secrets)
	}
	spec := mo.Secrets["NEW"]
	if spec.Value != "v" {
		t.Errorf("expected Value=v, got %q", spec.Value)
	}
	if !slices.Contains(mo.SecretsRemove, "OLD") {
		t.Errorf("expected SecretsRemove=[OLD], got %v", mo.SecretsRemove)
	}
	// mo.Env is nil because envChanged=false (applyEnvSpec not called)
	if mo.Env != nil {
		t.Errorf("expected nil Env (no change requested), got %v", mo.Env)
	}
	// secretState is rebuilt, envState is unchanged (zero-value applied)
	wantSecret := buildSecretState(desiredSecrets)
	if secretState.Hash != wantSecret.Hash {
		t.Errorf("secretState hash: got %q, want %q", secretState.Hash, wantSecret.Hash)
	}
	// envState preserved as applied (zero value, unchanged)
	if envState.Hash != "" {
		t.Errorf("envState should be unchanged (zero): got %q", envState.Hash)
	}
}

func TestReconcileErrorsWrapped(t *testing.T) {
	mock := &MockSandboxHandle{
		ModifyErr: errors.New("sdk error"),
	}
	_, _, _, err := reconcileEnvAndSecrets(
		context.Background(), mock,
		map[string]string{"X": "1"},
		[]msbSdk.SecretEntry{},
		EnvState{}, SecretState{},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "modify env/secrets") {
		t.Errorf("expected wrapped error, got %v", err)
	}
}
