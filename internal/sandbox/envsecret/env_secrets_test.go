package envsecret

import (
	"reflect"
	"slices"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestLoadEnvAndSecretsMergesUserAndProject(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()

	testutil.WritePath(t, cp.UserEnvFile(), "A=user\nSHARED=user\n")
	testutil.WritePath(t, cp.ProjectEnvFile(), "B=project\nSHARED=project\n")

	testutil.WritePath(t, cp.UserEnvSecretFile(), "SECRET_A=value-a@user.example\n")
	testutil.WritePath(t, cp.ProjectEnvSecretFile(), "SECRET_B=value-b@project.example\n")
	testutil.WritePath(t, cp.UserEnvSecretYAMLFile(), "SECRET_C:\n  value: value-c\n  host: user.example\n")
	testutil.WritePath(t, cp.ProjectEnvSecretYAMLFile(), "SECRET_D:\n  value: value-d\n  hosts: [project.example]\n")

	ui := termio.NewTestMock(t)
	loaded := LoadEnvAndSecrets(&ui, nil)

	wantEnv := map[string]string{"A": "user", "B": "project", "SHARED": "project"}
	if !reflect.DeepEqual(loaded.Vars, wantEnv) {
		t.Errorf("env = %#v, want %#v", loaded.Vars, wantEnv)
	}

	secrets := loaded.Secrets
	if len(secrets) != 4 {
		t.Fatalf("expected 4 secrets, got %d: %#v", len(secrets), secrets)
	}
	byVar := make(map[string]msbSdk.SecretEntry, len(secrets))
	for _, s := range secrets {
		byVar[s.EnvVar] = s
	}
	wantValues := map[string]string{
		"SECRET_A": "value-a",
		"SECRET_B": "value-b",
		"SECRET_C": "value-c",
		"SECRET_D": "value-d",
	}
	for envVar, want := range wantValues {
		if got := byVar[envVar].Value; got != want {
			t.Errorf("%s value = %q, want %q", envVar, got, want)
		}
	}
}

// TestLoadEnvAndSecretsAppliesAllowPlaceholderFor pins that the global
// allow-placeholder-for list reaches the built entries: a listed host becomes a
// placeholder-passthrough host, while a host the secret already allows keeps
// substitution and is excluded from passthrough.
func TestLoadEnvAndSecretsAppliesAllowPlaceholderFor(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()

	testutil.WritePath(t, cp.ProjectEnvSecretFile(), "SECRET_A=value-a@a.example\n")

	ui := termio.NewTestMock(t)
	loaded := LoadEnvAndSecrets(&ui, []string{"model.example", "a.example"})

	if len(loaded.Secrets) != 1 {
		t.Fatalf("expected 1 secret, got %d: %#v", len(loaded.Secrets), loaded.Secrets)
	}
	if !slices.Equal(loaded.Secrets[0].Passthrough, []string{"model.example"}) {
		t.Errorf("Passthrough = %v, want [model.example]", loaded.Secrets[0].Passthrough)
	}
}

func TestLoadEnvAndSecretsEmptyWhenNoFiles(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	ui := termio.NewTestMock(t)
	loaded := LoadEnvAndSecrets(&ui, nil)

	if len(loaded.Vars) != 0 {
		t.Errorf("expected empty env, got %#v", loaded.Vars)
	}
	if len(loaded.Secrets) != 0 {
		t.Errorf("expected empty secrets, got %#v", loaded.Secrets)
	}
}
