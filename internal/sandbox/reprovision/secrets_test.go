package reprovision

import (
	"path/filepath"
	"reflect"
	"testing"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/testutil"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

func TestParseSecretSpecLegacyCreatesEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.secret")
	testutil.WritePath(t, path, "FOO=bar@example.org\n")
	testUI := termio.NewTestMock(t)

	specs := ParseSecretSpecLegacy(path, &testUI)
	want := map[string]SecretSpec{"FOO": {Value: "bar", Hosts: []string{"example.org"}}}
	if !reflect.DeepEqual(specs, want) {
		t.Errorf("ParseSecretSpecLegacy = %#v, want %#v", specs, want)
	}
}

func TestParseSecretSpecLegacySplitsOnLastAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.secret")
	testutil.WritePath(t, path, "FOO=bar@baz@example.org\n")
	testUI := termio.NewTestMock(t)

	specs := ParseSecretSpecLegacy(path, &testUI)
	got, ok := specs["FOO"]
	if !ok {
		t.Fatal("expected FOO entry")
	}
	// Legacy format splits on last @: value is everything before, host is after.
	if got.Value != "bar@baz" || !reflect.DeepEqual(got.Hosts, []string{"example.org"}) {
		t.Errorf("got value=%q hosts=%v", got.Value, got.Hosts)
	}
}

func TestParseSecretSpecLegacyMissingFile(t *testing.T) {
	testUI := termio.NewTestMock(t)
	specs := ParseSecretSpecLegacy(filepath.Join(t.TempDir(), "nope"), &testUI)
	if len(specs) != 0 {
		t.Errorf("expected empty map for missing file, got %#v", specs)
	}
}

func TestParseSecretSpecLegacyBadLineWarns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.secret")
	testutil.WritePath(t, path, "KEY=nohost\n")
	mock := termio.NewTestMock(t)
	specs := ParseSecretSpecLegacy(path, &mock)
	if len(specs) != 0 {
		t.Errorf("expected 0 entries for a line without @ separator, got %#v", specs)
	}
	if len(mock.WarnCalls) == 0 {
		t.Error("expected a warning for missing @ separator")
	}
}

func TestBuildSecretsFromSpecsNoHostsDropsEntry(t *testing.T) {
	testUI := termio.NewTestMock(t)
	in := map[string]SecretSpec{"FOO": {Value: "x"}}
	secrets := BuildSecretsFromSpecs(in, &testUI)
	if len(secrets) != 0 {
		t.Fatalf("expected entry with no hosts to be dropped, got %d secrets", len(secrets))
	}
	if len(testUI.WarnCalls) == 0 {
		t.Error("expected a warning for no hosts defined")
	}
}

func TestBuildSecretsFromSpecsHostListAndHost(t *testing.T) {
	testUI := termio.NewTestMock(t)
	in := map[string]SecretSpec{"K": {Value: "v", Host: "a.example", Hosts: []string{"b.example"}}}
	secrets := BuildSecretsFromSpecs(in, &testUI)
	if len(secrets) != 1 {
		t.Fatalf("expected 1 secret")
	}
	want := []string{"b.example", "a.example"}
	got := secrets[0].AllowHosts
	if len(got) != len(want) {
		t.Fatalf("AllowHosts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("AllowHosts[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestBuildSecretsFromSpecsEmptyValueAndHostIsPassedThrough(t *testing.T) {
	testUI := termio.NewTestMock(t)
	in := map[string]SecretSpec{"K": {Value: "", Hosts: []string{"h"}}}
	secrets := BuildSecretsFromSpecs(in, &testUI)
	if len(secrets) != 1 {
		t.Fatalf("expected 1 secret for empty value with host, got %d", len(secrets))
	}
	if secrets[0].Value != "" {
		t.Errorf("Value = %q, want empty string", secrets[0].Value)
	}
}

func TestParseSecretSpecYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.secret.yaml")
	testutil.WritePath(t, path, "FOO:\n  value: \"a@b@c\"\n  host: gw.example\n")
	testUI := termio.NewTestMock(t)

	specs := ParseSecretSpecYAML(path, &testUI)
	want := map[string]SecretSpec{"FOO": {Value: "a@b@c", Host: "gw.example"}}
	if !reflect.DeepEqual(specs, want) {
		t.Errorf("got %#v, want %#v", specs, want)
	}
}

func TestParseSecretSpecYAMLJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.secret.yaml")
	testutil.WritePath(t, path, `{"FOO": {"value": "a@b", "hosts": ["h1","h2"]}}`+"\n")
	testUI := termio.NewTestMock(t)

	specs := ParseSecretSpecYAML(path, &testUI)
	want := map[string]SecretSpec{"FOO": {Value: "a@b", Hosts: []string{"h1", "h2"}}}
	if !reflect.DeepEqual(specs, want) {
		t.Errorf("got %#v, want %#v", specs, want)
	}
}

func TestParseSecretSpecYAMLMultilineValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.secret.yaml")
	testutil.WritePath(t, path, "PEM:\n  value: |\n    line1\n    line2\n")
	testUI := termio.NewTestMock(t)

	specs := ParseSecretSpecYAML(path, &testUI)
	got := specs["PEM"].Value
	want := "line1\nline2\n"
	if got != want {
		t.Errorf("multiline value = %q, want %q", got, want)
	}
}

func TestParseSecretSpecYAMLMissingFile(t *testing.T) {
	testUI := termio.NewTestMock(t)
	specs := ParseSecretSpecYAML(filepath.Join(t.TempDir(), "nope"), &testUI)
	if len(specs) != 0 {
		t.Errorf("expected empty map for missing file, got %#v", specs)
	}
}

func TestParseSecretSpecYAMLMalformedWarns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.secret.yaml")
	testutil.WritePath(t, path, "{ not valid yaml\n")
	testUI := termio.NewTestMock(t)
	specs := ParseSecretSpecYAML(path, &testUI)
	if len(specs) != 0 {
		t.Errorf("expected empty map for malformed yaml, got %#v", specs)
	}
}

func TestParseSecretSpecYAMLAllowAnyHostDangerous(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.secret.yaml")
	testutil.WritePath(t, path, "DANGER:\n  value: x\n  allow_any_host_dangerous: true\n")
	testUI := termio.NewTestMock(t)

	specs := ParseSecretSpecYAML(path, &testUI)
	want := map[string]SecretSpec{"DANGER": {Value: "x", AllowAnyHostDangerous: true}}
	if !reflect.DeepEqual(specs, want) {
		t.Errorf("got %#v, want %#v", specs, want)
	}
}

func TestBuildSecretsFromSpecsAllowAnyHostDangerous(t *testing.T) {
	testUI := termio.NewTestMock(t)
	in := map[string]SecretSpec{"K": {Value: "v", AllowAnyHostDangerous: true}}
	secrets := BuildSecretsFromSpecs(in, &testUI)
	if len(secrets) != 1 {
		t.Fatalf("expected 1 secret for allow_any_host_dangerous, got %d", len(secrets))
	}
	if secrets[0].Value != "v" {
		t.Errorf("Value = %q, want v", secrets[0].Value)
	}
	// No hosts when allow_any_host_dangerous: nil/empty AllowHosts
	if len(secrets[0].AllowHosts) != 0 {
		t.Errorf("AllowHosts = %v, want empty for dangerous any-host", secrets[0].AllowHosts)
	}
}

func TestMergeSecretSpecsLaterWins(t *testing.T) {
	got := MergeSecretSpecs(
		map[string]SecretSpec{"K": {Value: "legacy", Hosts: []string{"a"}}},
		map[string]SecretSpec{"K": {Value: "yaml", Host: "b"}, "J": {Value: "only-yaml"}},
	)
	want := map[string]SecretSpec{
		"K": {Value: "yaml", Host: "b"},
		"J": {Value: "only-yaml"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestMergeSecretSpecsEmpty(t *testing.T) {
	if got := MergeSecretSpecs(); got != nil {
		t.Errorf("expected nil for no maps, got %#v", got)
	}
}

func TestBuildSecretsPipelinePrecedence(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "user.env.secret")
	project := filepath.Join(dir, "project.env.secret")
	userYAML := filepath.Join(dir, "user.env.secret.yaml")
	projectYAML := filepath.Join(dir, "project.env.secret.yaml")
	testutil.WritePath(t, user, "KEY=legacy@a.example\nONLY_LEGACY=v@h\n")
	testutil.WritePath(t, project, "KEY=proj@b.example\n")
	testutil.WritePath(t, userYAML, "KEY:\n  value: user-yaml\n  host: u.example\n")
	testutil.WritePath(t, projectYAML, "KEY:\n  value: proj-yaml@h\n  host: p.example\n")

	testUI := termio.NewTestMock(t)
	specs := MergeSecretSpecs(
		ParseSecretSpecLegacy(user, &testUI),
		ParseSecretSpecLegacy(project, &testUI),
		ParseSecretSpecYAML(userYAML, &testUI),
		ParseSecretSpecYAML(projectYAML, &testUI),
	)
	secrets := BuildSecretsFromSpecs(specs, &testUI)

	byVar := map[string]msbSdk.SecretEntry{}
	for _, s := range secrets {
		byVar[s.EnvVar] = s
	}
	if got := byVar["KEY"].Value; got != "proj-yaml@h" {
		t.Errorf("KEY value = %q, want proj-yaml@h (yaml wins, project wins)", got)
	}
	if got := byVar["ONLY_LEGACY"].Value; got != "v" {
		t.Errorf("ONLY_LEGACY value = %q, want v", got)
	}
}

func TestBuildSecretsFromSpecsDangerousFlagWithHosts(t *testing.T) {
	testUI := termio.NewTestMock(t)
	in := map[string]SecretSpec{"K": {Value: "v", AllowAnyHostDangerous: true, Hosts: []string{"h.example"}}}
	secrets := BuildSecretsFromSpecs(in, &testUI)
	if len(secrets) != 1 {
		t.Fatalf("expected 1 secret, got %d", len(secrets))
	}
	if secrets[0].Value != "v" {
		t.Errorf("Value = %q, want v", secrets[0].Value)
	}
	if secrets[0].AllowHosts[0] != "h.example" {
		t.Errorf("AllowHosts = %v, want [h.example]", secrets[0].AllowHosts)
	}
}

func TestMergePrecedence4Layers(t *testing.T) {
	dir := t.TempDir()
	userLegacy := filepath.Join(dir, "user.env.secret")
	projectLegacy := filepath.Join(dir, "project.env.secret")
	userYAML := filepath.Join(dir, "user.env.secret.yaml")
	projectYAML := filepath.Join(dir, "project.env.secret.yaml")

	testutil.WritePath(t, userLegacy, "A=user-legacy@h.example\nB=user-legacy@h.example\n")
	testutil.WritePath(t, projectLegacy, "A=proj-legacy@h.example\n")
	testutil.WritePath(
		t,
		userYAML,
		"C:\n  value: user-yaml\n  allow_any_host_dangerous: true\nD:\n  value: user-yaml\n  host: u.example\n",
	)
	testutil.WritePath(
		t,
		projectYAML,
		"C:\n  value: proj-yaml\n  host: p.example\nD:\n  value: proj-yaml\n  allow_any_host_dangerous: true\nE:\n  value: proj-only\n  host: e.example\n",
	)

	testUI := termio.NewTestMock(t)
	specs := MergeSecretSpecs(
		ParseSecretSpecLegacy(userLegacy, &testUI),
		ParseSecretSpecLegacy(projectLegacy, &testUI),
		ParseSecretSpecYAML(userYAML, &testUI),
		ParseSecretSpecYAML(projectYAML, &testUI),
	)
	secrets := BuildSecretsFromSpecs(specs, &testUI)

	byVar := map[string]msbSdk.SecretEntry{}
	for _, s := range secrets {
		byVar[s.EnvVar] = s
	}

	// A: project-legacy wins over user-legacy
	if got := byVar["A"].Value; got != "proj-legacy" {
		t.Errorf("A value = %q, want proj-legacy", got)
	}
	// B: user-YAML doesn't override B (B not in YAML), but user-YAML C dangerous doesn't affect B
	if got := byVar["B"].Value; got != "user-legacy" {
		t.Errorf("B value = %q, want user-legacy", got)
	}
	// C: project-YAML overrides user-YAML (full replace)
	if got := byVar["C"].Value; got != "proj-yaml" {
		t.Errorf("C value = %q, want proj-yaml", got)
	}
	// D: user-YAML (host: u.example) overridden by project-YAML (dangerous)
	if got := byVar["D"].Value; got != "proj-yaml" {
		t.Errorf("D value = %q, want proj-yaml", got)
	}
	// E: only in project-YAML
	if got := byVar["E"].Value; got != "proj-only" {
		t.Errorf("E value = %q, want proj-only", got)
	}
}

func TestParseSecretSpecLegacyValueOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.secret")
	testutil.WritePath(t, path, "FOO=val@h.example\n")
	testUI := termio.NewTestMock(t)
	specs := ParseSecretSpecLegacy(path, &testUI)
	want := map[string]SecretSpec{"FOO": {Value: "val", Hosts: []string{"h.example"}}}
	if !reflect.DeepEqual(specs, want) {
		t.Errorf("got %#v, want %#v", specs, want)
	}
}

func TestBuildSecretsEmptyHostname(t *testing.T) {
	testUI := termio.NewTestMock(t)
	in := map[string]SecretSpec{"K": {Value: "v", Hosts: []string{""}}}
	secrets := BuildSecretsFromSpecs(in, &testUI)
	if len(secrets) != 1 {
		t.Fatalf("expected 1 secret, got %d", len(secrets))
	}
	if secrets[0].AllowHosts[0] != "" {
		t.Errorf("AllowHosts = %v, want [\"\"]", secrets[0].AllowHosts)
	}
}

func TestBuildSecretsYAMLValueOnlyWithNoHostsDropped(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "env.secret")
	yamlPath := filepath.Join(dir, "env.secret.yaml")

	testutil.WritePath(t, legacyPath, "KEY=legacy@h.example\n")
	testutil.WritePath(t, yamlPath, "KEY:\n  value: yaml-only\n")

	testUI := termio.NewTestMock(t)
	specs := MergeSecretSpecs(
		ParseSecretSpecLegacy(legacyPath, &testUI),
		ParseSecretSpecYAML(yamlPath, &testUI),
	)
	secrets := BuildSecretsFromSpecs(specs, &testUI)
	// YAML entry with value only fully replaces legacy spec; no hosts → dropped
	if len(secrets) != 0 {
		t.Errorf("expected 0 secrets (YAML value-only replaces legacy hosts), got %d", len(secrets))
	}
	if len(testUI.WarnCalls) == 0 {
		t.Error("expected a warning for the dropped entry")
	}
}

func TestMergeSecretSpecsYAMLOnlyKeyWins(t *testing.T) {
	dir := t.TempDir()
	userYAML := filepath.Join(dir, "user.env.secret.yaml")
	projectYAML := filepath.Join(dir, "project.env.secret.yaml")

	testutil.WritePath(t, userYAML, "KEY:\n  value: user-yaml\n  host: u.example\n")
	testutil.WritePath(
		t,
		projectYAML,
		"KEY:\n  value: proj-yaml\n  host: p.example\nEXTRA:\n  value: extras\n  host: e.example\n",
	)

	testUI := termio.NewTestMock(t)
	specs := MergeSecretSpecs(
		ParseSecretSpecYAML(userYAML, &testUI),
		ParseSecretSpecYAML(projectYAML, &testUI),
	)
	secrets := BuildSecretsFromSpecs(specs, &testUI)

	byVar := map[string]msbSdk.SecretEntry{}
	for _, s := range secrets {
		byVar[s.EnvVar] = s
	}

	if got := byVar["KEY"].Value; got != "proj-yaml" {
		t.Errorf("KEY value = %q, want proj-yaml", got)
	}
	if got := byVar["EXTRA"].Value; got != "extras" {
		t.Errorf("EXTRA value = %q, want extras", got)
	}
}

func TestParseSecretSpecLegacyValueWithAtAtEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.secret")
	testutil.WritePath(t, path, "FOO=value@@h.example\n")
	testUI := termio.NewTestMock(t)
	specs := ParseSecretSpecLegacy(path, &testUI)
	want := map[string]SecretSpec{"FOO": {Value: "value@", Hosts: []string{"h.example"}}}
	if !reflect.DeepEqual(specs, want) {
		t.Errorf("got %#v, want %#v", specs, want)
	}
}

func TestBuildSecretsFromSpecsOnlySingleHost(t *testing.T) {
	testUI := termio.NewTestMock(t)
	in := map[string]SecretSpec{"K": {Value: "v", Host: "h.example"}}
	secrets := BuildSecretsFromSpecs(in, &testUI)
	if len(secrets) != 1 {
		t.Fatalf("expected 1 secret, got %d", len(secrets))
	}
	if secrets[0].AllowHosts[0] != "h.example" {
		t.Errorf("AllowHosts = %v, want [h.example]", secrets[0].AllowHosts)
	}
}
