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
	scopes := configpaths.ConfigScopes(configpaths.Get(), "")

	testutil.WritePath(t, scopes[0].EnvFile(), "A=user\nSHARED=user\n")
	testutil.WritePath(t, scopes[1].EnvFile(), "B=project\nSHARED=project\n")

	testutil.WritePath(t, scopes[0].EnvSecretFile(), "SECRET_A=value-a@user.example\n")
	testutil.WritePath(t, scopes[1].EnvSecretFile(), "SECRET_B=value-b@project.example\n")
	testutil.WritePath(t, scopes[0].EnvSecretYAMLFile(), "SECRET_C:\n  value: value-c\n  host: user.example\n")
	testutil.WritePath(t, scopes[1].EnvSecretYAMLFile(), "SECRET_D:\n  value: value-d\n  hosts: [project.example]\n")

	ui := termio.NewTestMock(t)
	loaded := LoadEnvAndSecrets(&ui, scopes, nil)

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
	scopes := configpaths.ConfigScopes(configpaths.Get(), "")

	testutil.WritePath(t, scopes[1].EnvSecretFile(), "SECRET_A=value-a@a.example\n")

	ui := termio.NewTestMock(t)
	loaded := LoadEnvAndSecrets(&ui, scopes, []string{"model.example", "a.example"})

	if len(loaded.Secrets) != 1 {
		t.Fatalf("expected 1 secret, got %d: %#v", len(loaded.Secrets), loaded.Secrets)
	}
	if !slices.Equal(loaded.Secrets[0].Passthrough, []string{"model.example"}) {
		t.Errorf("Passthrough = %v, want [model.example]", loaded.Secrets[0].Passthrough)
	}
}

func TestLoadEnvAndSecretsEmptyWhenNoFiles(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	scopes := configpaths.ConfigScopes(configpaths.Get(), "")

	ui := termio.NewTestMock(t)
	loaded := LoadEnvAndSecrets(&ui, scopes, nil)

	if len(loaded.Vars) != 0 {
		t.Errorf("expected empty env, got %#v", loaded.Vars)
	}
	if len(loaded.Secrets) != 0 {
		t.Errorf("expected empty secrets, got %#v", loaded.Secrets)
	}
}

// TestLoadEnvAndSecretsPerSlugPrecedence pins that env files merge across all
// scopes in order: generic < per-slug < project, with later scopes winning.
func TestLoadEnvAndSecretsPerSlugPrecedence(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	scopes := configpaths.ConfigScopes(configpaths.Get(), "widget-bbb")

	testutil.WritePath(t, scopes[0].EnvFile(), "SHARED=generic\nA=generic\n")
	testutil.WritePath(t, scopes[1].EnvFile(), "SHARED=slug\nB=slug\n")
	testutil.WritePath(t, scopes[2].EnvFile(), "SHARED=project\nC=project\n")

	ui := termio.NewTestMock(t)
	loaded := LoadEnvAndSecrets(&ui, scopes, nil)

	wantEnv := map[string]string{
		"SHARED": "project",
		"A":      "generic",
		"B":      "slug",
		"C":      "project",
	}
	if !reflect.DeepEqual(loaded.Vars, wantEnv) {
		t.Errorf("env = %#v, want %#v", loaded.Vars, wantEnv)
	}
}

// TestLoadEnvAndSecretsPerSlugSecretFormats pins the format-tiered secret
// precedence across scopes: every scope's legacy specs merge first, then every
// scope's YAML specs, so a per-slug YAML entry beats a project legacy entry for
// the same key while the later project scope still wins within the YAML tier.
func TestLoadEnvAndSecretsPerSlugSecretFormats(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	scopes := configpaths.ConfigScopes(configpaths.Get(), "widget-bbb")

	testutil.WritePath(t, scopes[1].EnvSecretFile(), "TOKEN=legacy@legacy.example\n")
	testutil.WritePath(t, scopes[2].EnvSecretFile(), "TOKEN=project-legacy@project-legacy.example\n")
	testutil.WritePath(
		t,
		scopes[1].EnvSecretYAMLFile(),
		"TOKEN:\n  value: yaml-slug\n  hosts: [slug.example]\n"+
			"OTHER:\n  value: yaml-slug-other\n  hosts: [slug-other.example]\n",
	)
	testutil.WritePath(
		t,
		scopes[2].EnvSecretYAMLFile(),
		"OTHER:\n  value: yaml-project-other\n  hosts: [project-other.example]\n",
	)

	ui := termio.NewTestMock(t)
	loaded := LoadEnvAndSecrets(&ui, scopes, nil)

	byVar := make(map[string]msbSdk.SecretEntry, len(loaded.Secrets))
	for _, secret := range loaded.Secrets {
		byVar[secret.EnvVar] = secret
	}
	if got := byVar["TOKEN"].Value; got != "yaml-slug" {
		t.Errorf("TOKEN value = %q, want the per-slug YAML value yaml-slug", got)
	}
	if got := byVar["TOKEN"].Allow; !slices.Equal(got, []string{"slug.example"}) {
		t.Errorf("TOKEN Allow = %v, want [slug.example]", got)
	}
	if got := byVar["OTHER"].Value; got != "yaml-project-other" {
		t.Errorf("OTHER value = %q, want the project YAML value yaml-project-other", got)
	}
	if got := byVar["OTHER"].Allow; !slices.Equal(got, []string{"project-other.example"}) {
		t.Errorf("OTHER Allow = %v, want [project-other.example]", got)
	}
}
