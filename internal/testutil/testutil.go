//deadcode:ignore reason="test support: referenced only by _test.go files"
package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"
)

// WriteFile writes content to a file under dir.
func WriteFile(tb testing.TB, dir, name, content string) {
	tb.Helper() // gomutants:disable STATEMENT_REMOVE reason="t.Helper only adjusts reported stack frames"
	WritePath(tb, filepath.Join(dir, name), content)
}

// WritePath writes content to an absolute path.
func WritePath(tb testing.TB, path, content string) {
	tb.Helper() // gomutants:disable STATEMENT_REMOVE reason="t.Helper only adjusts reported stack frames"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		tb.Fatalf("write %s: %v", path, err)
	}
}

// WriteYAML marshals v as YAML and writes it to a file under dir.
func WriteYAML(tb testing.TB, dir, name string, v map[string]any) {
	tb.Helper() // gomutants:disable STATEMENT_REMOVE reason="t.Helper only adjusts reported stack frames"
	data, err := yaml.Marshal(v)
	if err != nil {
		tb.Fatalf("marshal yaml: %v", err)
	}
	WriteFile(tb, dir, name, string(data))
}

// UnsetEnv removes name from the process environment for the test's duration
// and restores its previous value, or its absence, on cleanup. os.Setenv and
// os.Unsetenv cannot fail on the supported platforms (Linux, macOS), so their
// errors are ignored.
//
// The restore uses os.Setenv, not tb.Setenv: tb.Setenv registers a cleanup that
// resets the variable to its pre-call value, and from inside another cleanup that
// reset runs afterward and undoes the restore.
func UnsetEnv(tb testing.TB, name string) {
	tb.Helper() // gomutants:disable STATEMENT_REMOVE reason="t.Helper only adjusts reported stack frames"
	value, present := os.LookupEnv(name)
	_ = os.Unsetenv(name)
	tb.Cleanup(func() {
		if present {
			//nolint:usetesting // tb.Setenv from a cleanup registers its own cleanup that undoes the restore
			_ = os.Setenv(name, value)
			return
		}
		_ = os.Unsetenv(name)
	})
}
