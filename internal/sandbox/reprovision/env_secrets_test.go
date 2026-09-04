package reprovision

import (
	"reflect"
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
	env, secrets := LoadEnvAndSecrets(&ui)

	wantEnv := map[string]string{"A": "user", "B": "project", "SHARED": "project"}
	if !reflect.DeepEqual(env, wantEnv) {
		t.Errorf("env = %#v, want %#v", env, wantEnv)
	}

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

func TestLoadEnvAndSecretsEmptyWhenNoFiles(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	ui := termio.NewTestMock(t)
	env, secrets := LoadEnvAndSecrets(&ui)

	if len(env) != 0 {
		t.Errorf("expected empty env, got %#v", env)
	}
	if len(secrets) != 0 {
		t.Errorf("expected empty secrets, got %#v", secrets)
	}
}
