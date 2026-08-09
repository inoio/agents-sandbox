package sandbox

import (
	"context"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

func TestEnvMapsEqual(t *testing.T) {
	if !envMapsEqual(nil, map[string]string{}) {
		t.Error("nil vs empty should be equal")
	}
	if envMapsEqual(map[string]string{"A": "1"}, map[string]string{"A": "2"}) {
		t.Error("different values should not be equal")
	}
	if envMapsEqual(map[string]string{"A": "1"}, map[string]string{"A": "1", "B": "2"}) {
		t.Error("extra key should not be equal")
	}
}

func TestReconcileEnvAndSecretsAppliesModify(t *testing.T) {
	handle := &MockSandboxHandle{
		Cfg:  &msbSdk.SandboxConfig{Env: map[string]string{"OLD": "1"}},
		Plan: &msbSdk.SandboxModificationPlan{Applied: true},
	}
	// Build the desired secret exactly as BuildSecrets does (Secret.Env).
	desired := []msbSdk.SecretEntry{
		msbSdk.Secret.Env("SECRET", "v", msbSdk.SecretEnvOptions{AllowHosts: []string{"host"}}),
	}
	if _, err := reconcileEnvAndSecrets(
		context.Background(), handle,
		map[string]string{"NEW": "2"},
		desired,
	); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if len(handle.ModifiedOptions) != 1 {
		t.Fatalf("expected 1 Modify call, got %d", len(handle.ModifiedOptions))
	}
	mo := handle.ModifiedOptions[0]
	if mo.Env["NEW"] != "2" {
		t.Errorf("expected Env to include NEW=2, got %+v", mo.Env)
	}
	//nolint:staticcheck // nil check before len is defensive; brief-mandated test
	if mo.EnvRemove == nil || len(mo.EnvRemove) == 0 {
		t.Errorf("expected EnvRemove for stale OLD, got %+v", mo.EnvRemove)
	}
	spec, ok := mo.Secrets["SECRET"]
	if !ok {
		t.Fatalf("expected Secrets to include SECRET, got %+v", mo.Secrets)
	}
	if spec.Value != "v" || len(spec.AllowedHosts) != 1 || spec.AllowedHosts[0] != "host" {
		t.Errorf("expected secret spec Value=v AllowedHosts=[host], got %+v", spec)
	}
}

func TestReconcileEnvAndSecretsNoopWhenSame(t *testing.T) {
	handle := &MockSandboxHandle{
		Cfg: &msbSdk.SandboxConfig{Env: map[string]string{"A": "1"}},
	}
	// Note: deciding "same env" needs a stable comparator; if buildEnvMap
	// adds defaults, this test uses exact equality.
	if _, err := reconcileEnvAndSecrets(
		context.Background(), handle, map[string]string{"A": "1"}, nil,
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(handle.ModifiedOptions) != 0 {
		t.Errorf("expected no Modify call, got %d", len(handle.ModifiedOptions))
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
	if len(state.Names) == 0 {
		t.Fatal("Names should not be empty")
	}
	for i := 1; i < len(state.Names); i++ {
		if state.Names[i-1] >= state.Names[i] {
			t.Errorf("Names not sorted at index %d: %q >= %q", i, state.Names[i-1], state.Names[i])
		}
	}
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
	if len(state.Names) == 0 {
		t.Fatal("Names should not be empty")
	}
	for i := 1; i < len(state.Names); i++ {
		if state.Names[i-1] >= state.Names[i] {
			t.Errorf("Names not sorted at index %d: %q >= %q", i, state.Names[i-1], state.Names[i])
		}
	}
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
