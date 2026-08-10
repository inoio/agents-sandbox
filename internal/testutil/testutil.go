package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

// TermUIMock returns an empty termio.Mock for tests.
func TermUIMock(tb testing.TB) termio.Mock {
	tb.Helper()
	return termio.Mock{}
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

// InitMocks runs each install closure before the package's tests and then executes
// them. Go requires TestMain to be defined in the package under test; this helper
// keeps that definition to a single call. An install closure swaps a package-level
// factory var (e.g. sandbox.GetConfigPaths) to a fail-fast default for tests.
func InitMocks(m *testing.M, installs ...func()) {
	for _, install := range installs {
		install()
	}
	m.Run()
}
