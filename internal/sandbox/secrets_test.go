package sandbox

import (
	"path/filepath"
	"reflect"
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
)

func TestParseSecretSpecLegacyCreatesEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.secret")
	testutil.WritePath(t, path, "FOO=bar@example.org\n")
	testUI := testutil.TermUIMock(t)

	specs := parseSecretSpecLegacy(path, &testUI)
	want := map[string]secretSpec{"FOO": {Value: "bar", Hosts: []string{"example.org"}}}
	if !reflect.DeepEqual(specs, want) {
		t.Errorf("parseSecretSpecLegacy = %#v, want %#v", specs, want)
	}
}

func TestParseSecretSpecLegacySplitsOnFirstAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.secret")
	testutil.WritePath(t, path, "FOO=bar@baz@example.org\n")
	testUI := testutil.TermUIMock(t)

	specs := parseSecretSpecLegacy(path, &testUI)
	got, ok := specs["FOO"]
	if !ok {
		t.Fatal("expected FOO entry")
	}
	// Legacy format keeps first-@ split: value is everything before the first @.
	if got.Value != "bar" || !reflect.DeepEqual(got.Hosts, []string{"baz@example.org"}) {
		t.Errorf("got value=%q hosts=%v", got.Value, got.Hosts)
	}
}

func TestParseSecretSpecLegacyMissingFile(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	specs := parseSecretSpecLegacy(filepath.Join(t.TempDir(), "nope"), &testUI)
	if len(specs) != 0 {
		t.Errorf("expected empty map for missing file, got %#v", specs)
	}
}

func TestParseSecretSpecLegacyBadLineWarns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.secret")
	testutil.WritePath(t, path, "FOO-no-at-sign\n")
	mock := testutil.TermUIMock(t)
	_ = mock
	specs := parseSecretSpecLegacy(path, &mock)
	if len(specs) != 0 {
		t.Errorf("expected 0 entries for a line with no @, got %#v", specs)
	}
}

func TestBuildSecretsFromSpecsDefaultHost(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	in := map[string]secretSpec{"FOO": {Value: "x"}}
	secrets := buildSecretsFromSpecs(in, &testUI)
	if len(secrets) != 1 {
		t.Fatalf("expected 1 secret, got %d", len(secrets))
	}
	if secrets[0].Value != "x" {
		t.Errorf("Value = %q, want x", secrets[0].Value)
	}
	if !reflect.DeepEqual(secrets[0].AllowHosts, []string{"microsandbox"}) {
		t.Errorf("AllowHosts = %v, want [microsandbox]", secrets[0].AllowHosts)
	}
}

func TestBuildSecretsFromSpecsHostListAndHost(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	in := map[string]secretSpec{"K": {Value: "v", Host: "a.example", Hosts: []string{"b.example"}}}
	secrets := buildSecretsFromSpecs(in, &testUI)
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

func TestBuildSecretsFromSpecsEmptyValueDropped(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	in := map[string]secretSpec{"K": {Hosts: []string{"h"}}}
	secrets := buildSecretsFromSpecs(in, &testUI)
	if len(secrets) != 0 {
		t.Errorf("expected entry with empty value and no override to be dropped, got %#v", secrets)
	}
}
