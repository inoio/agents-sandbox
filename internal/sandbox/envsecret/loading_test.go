package envsecret

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestBuildEnvMap(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), "env")
	testutil.WritePath(t, envFile, "FOO=bar\n# comment\n\nBAZ=qux\n")
	got := BuildEnvMap(envFile)

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

func TestReadSandboxEnvMissing(t *testing.T) {
	env := BuildEnvMap("missing")
	if len(env) != 0 {
		t.Errorf("expected 0 env vars when .agents-sandbox/env missing, got %d", len(env))
	}
}

func TestMergeEnvMapsProjectOverridesUser(t *testing.T) {
	userFile := filepath.Join(t.TempDir(), "env")
	testutil.WritePath(t, userFile, "FOO=user\nBAR=user\n")
	projectFile := filepath.Join(t.TempDir(), "env")
	testutil.WritePath(t, projectFile, "FOO=project\n")

	got := MergeEnvMaps(BuildEnvMap(userFile), BuildEnvMap(projectFile))
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

// TestParseSecretSpecLegacyEmptyKey verifies that a line with an empty (after
// trimming) key is skipped.
func TestParseSecretSpecLegacyEmptyKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.secret")
	testutil.WritePath(t, path, "=val@h.example\nFOO=bar@baz.example\n")
	testUI := termio.NewTestMock(t)
	specs := ParseSecretSpecLegacy(path, &testUI)
	if _, ok := specs["FOO"]; !ok {
		t.Error("expected FOO to be parsed")
	}
	if len(specs) != 1 {
		t.Errorf("expected only 1 spec (empty key skipped), got %d", len(specs))
	}
}
