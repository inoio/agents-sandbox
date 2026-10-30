package configpaths

import (
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestFindConfigFilePrefersDocumentedOrder pins the extension order itself:
// with several candidates present the earliest documented extension wins, and
// dropping the earlier ones hands over to the next in line.
func TestFindConfigFilePrefersDocumentedOrder(t *testing.T) {
	order := []string{".yaml", ".yml", ".json", ".jsonc", ".json5"}
	for i, want := range order {
		t.Run(want, func(t *testing.T) {
			dir := t.TempDir()
			for _, ext := range order[i:] {
				testutil.WriteFile(t, dir, "config"+ext, "a: 1\n")
			}

			path, ext, ok := FindConfigFile(dir)
			if !ok || ext != want || path != filepath.Join(dir, "config"+want) {
				t.Errorf(
					"FindConfigFile() = %q, %q, %v; want %q, %q, true",
					path, ext, ok, filepath.Join(dir, "config"+want), want,
				)
			}
		})
	}
}

// TestFindConfigFileEmptyDirIgnoresCwdConfig pins that an empty dir is rejected
// before any relative lookup: even with a config.yaml in the process CWD, an
// empty dir must not resolve one.
func TestFindConfigFileEmptyDirIgnoresCwdConfig(t *testing.T) {
	cwd := t.TempDir()
	testutil.WriteFile(t, cwd, "config.yaml", "a: 1\n")
	t.Chdir(cwd)

	if path, ext, ok := FindConfigFile(""); ok {
		t.Errorf(`FindConfigFile("") = %q, %q, true; want no file`, path, ext)
	}
}

func TestFindConfigFileMissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	if path, ext, ok := FindConfigFile(dir); ok {
		t.Errorf("FindConfigFile(%q) = %q, %q, true; want no file", dir, path, ext)
	}
}
