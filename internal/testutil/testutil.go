package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"
)

// NewTestio returns an empty stdio.Mock for tests.
func NewTestio(tb testing.TB) stdio.Mock {
	tb.Helper()
	return stdio.Mock{}
}

// WriteFile writes content to a file under dir.
func WriteFile(tb testing.TB, dir, name, content string) {
	tb.Helper()
	WritePath(tb, filepath.Join(dir, name), content)
}

// WritePath writes content to an absolute path.
func WritePath(tb testing.TB, path, content string) {
	tb.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		tb.Fatalf("write %s: %v", path, err)
	}
}

// WriteYAML marshals v as YAML and writes it to a file under dir.
func WriteYAML(tb testing.TB, dir, name string, v map[string]any) {
	tb.Helper()
	data, err := yaml.Marshal(v)
	if err != nil {
		tb.Fatalf("marshal yaml: %v", err)
	}
	WriteFile(tb, dir, name, string(data))
}
