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
