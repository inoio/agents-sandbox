package envsecret

import (
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/projectguard"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestBuildEnvMap(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), "env")
	testutil.WritePath(t, envFile, "FOO=bar\n# comment\n\nBAZ=qux\n")
	got := BuildEnvMap(envFile, new(termio.NewTestMock(t)))

	if len(got) != 2 {
		t.Fatalf("expected 2 env vars, got %d: %v", len(got), got)
	}
	if got["FOO"] != "bar" {
		t.Errorf("expected FOO=bar, got %q", got["FOO"])
	}
	if got["BAZ"] != "qux" {
		t.Errorf("expected BAZ=qux, got %q", got["BAZ"])
	}
}

// TestBuildEnvMapSkipsEmptyName verifies that a line without a variable name is
// dropped with a warning naming the file, because the VM refuses to boot with an
// empty env name.
func TestBuildEnvMapSkipsEmptyName(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), "env")
	testutil.WritePath(t, envFile, "=orphan\nFOO=bar\n")
	ui := termio.NewTestMock(t)

	got := BuildEnvMap(envFile, &ui)

	if want := map[string]string{"FOO": "bar"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if len(ui.WarnCalls) != 1 || !strings.Contains(ui.WarnCalls[0], envFile) {
		t.Errorf("expected one warning naming %s, got %q", envFile, ui.WarnCalls)
	}
}

func TestReadSandboxEnvMissing(t *testing.T) {
	env := BuildEnvMap("missing", new(termio.NewTestMock(t)))
	if env != nil {
		t.Errorf("expected nil env map when .agents-sandbox/env missing, got %#v", env)
	}
}

func TestMergeEnvMapsProjectOverridesUser(t *testing.T) {
	userFile := filepath.Join(t.TempDir(), "env")
	testutil.WritePath(t, userFile, "FOO=user\nBAR=user\n")
	projectFile := filepath.Join(t.TempDir(), "env")
	testutil.WritePath(t, projectFile, "FOO=project\n")

	ui := termio.NewTestMock(t)
	got := MergeEnvMaps(BuildEnvMap(userFile, &ui), BuildEnvMap(projectFile, &ui))
	want := map[string]string{"FOO": "project", "BAR": "user"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestParseKeyValueLinesOnLineError verifies that an error returned by the
// callback is propagated.
func TestParseKeyValueLinesOnLineError(t *testing.T) {
	boom := errors.New("boom")
	err := parseKeyValueLines("A=1\nB=2\n", func(key, _ string) error {
		if key == "B" {
			return boom
		}
		return nil
	})
	if !errors.Is(err, boom) {
		t.Errorf("expected boom, got %v", err)
	}
}

// TestParseKeyValueLinesSkipsNoEqualsLine verifies that a line without an '='
// separator is skipped rather than passed to the callback.
func TestParseKeyValueLinesSkipsNoEqualsLine(t *testing.T) {
	var seen []string
	err := parseKeyValueLines("A=1\nNOTSEPARATED\n\n# comment\n  \nC=3\n", func(key, value string) error {
		seen = append(seen, key+"="+value)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"A=1", "C=3"}
	if len(seen) != len(want) {
		t.Fatalf("seen = %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("seen[%d] = %q, want %q", i, seen[i], want[i])
		}
	}
}

// TestParseKeyValueLinesSkipsCommentContainingEquals verifies that a comment
// line is skipped even when it contains an '=' separator (which would
// otherwise split into a key/value pair).
func TestParseKeyValueLinesSkipsCommentContainingEquals(t *testing.T) {
	var seen []string
	err := parseKeyValueLines("# foo=bar\nA=1\n", func(key, value string) error {
		seen = append(seen, key+"="+value)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"A=1"}
	if len(seen) != len(want) || seen[0] != want[0] {
		t.Fatalf("seen = %v, want %v", seen, want)
	}
}

// TestParseKeyValueLinesTrimsKeyAndValue verifies trimming of lines and that
// keys/values are passed verbatim (no further trimming of parts).
func TestParseKeyValueLinesHandlesWhitespace(t *testing.T) {
	var got []string
	err := parseKeyValueLines("  A = 1  \n\tB=x\n", func(key, value string) error {
		got = append(got, key+"="+value)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"A = 1", "B=x"}
	if len(got) != len(want) {
		t.Fatalf("got = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

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

// TestEnvAndSecretReadsGoThroughTheApproval pins that env and secret files are
// read through the approval: changed ones are ignored with a warning and the
// change is recorded.
func TestEnvAndSecretReadsGoThroughTheApproval(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "env")
	legacy := filepath.Join(dir, "env.secret")
	yamlFile := filepath.Join(dir, "env.secret.yaml")
	t.Cleanup(projectguard.Install([]string{dir}, map[string]projectguard.Approval{
		envFile:  projectguard.ApprovalOf([]byte("A=1\n")),
		legacy:   projectguard.ApprovalOf([]byte("T=v@a.example.com\n")),
		yamlFile: projectguard.ApprovalOf([]byte("T:\n  value: v\n  hosts: [a.example.com]\n")),
	}))
	testutil.WritePath(t, envFile, "A=1\nHTTP_PROXY=http://evil\n")
	testutil.WritePath(t, legacy, "T=v@evil.example.com\n")
	testutil.WritePath(t, yamlFile, "T:\n  value: v\n  hosts: [evil.example.com]\n")
	ui := termio.NewTestMock(t)

	if got := BuildEnvMap(envFile, &ui); got != nil {
		t.Errorf("BuildEnvMap = %v, want nothing from a changed env file", got)
	}
	if got := ParseSecretSpecLegacy(legacy, &ui); got != nil {
		t.Errorf("ParseSecretSpecLegacy = %v, want nothing from a changed secret file", got)
	}
	if got := ParseSecretSpecYAML(yamlFile, &ui); got != nil {
		t.Errorf("ParseSecretSpecYAML = %v, want nothing from a changed secret file", got)
	}
	if len(ui.WarnCalls) != 3 {
		t.Errorf("warnings = %q, want one per changed file", ui.WarnCalls)
	}
	if !errors.Is(projectguard.Violation(), projectguard.ErrNotApproved) {
		t.Errorf("Violation() = %v, want the change recorded", projectguard.Violation())
	}
}
