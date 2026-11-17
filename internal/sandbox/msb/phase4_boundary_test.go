package msb

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// sdkImportPath is the microsandbox SDK import no non-`msb` production package
// may use.
const sdkImportPath = "github.com/superradcompany/microsandbox/sdk/go"

// TestNoProductionPackageImportsSDK pins the phase-4 end state: with the
// launcher owning the whole config model, the microsandbox SDK is an
// implementation detail of internal/sandbox/msb alone. Every other package's
// non-test sources must be SDK-free; see
// .operator-shared/specs/architecture/msb-seam.md.
//
// The scan uses go/parser and skips _test.go files, so a test import of the SDK
// never counts.
func TestNoProductionPackageImportsSDK(t *testing.T) {
	moduleRoot := filepath.Join("..", "..", "..")
	offenders, err := findSDKImporters(moduleRoot, sdkImportPath)
	if err != nil {
		t.Fatalf("scan module for SDK imports: %v", err)
	}
	for _, offender := range offenders {
		t.Errorf(
			"%s imports the microsandbox SDK; only internal/sandbox/msb may",
			offender,
		)
	}
}

// findSDKImporters returns the module-relative paths of production directories
// under moduleRoot that import sdkPath, excluding the adapter and anything that
// is not part of this module: a nested directory with its own go.mod (a
// throwaway spike module, a vendored tree) owns its own import graph, so the
// boundary does not reach into it.
func findSDKImporters(moduleRoot, sdkPath string) ([]string, error) {
	adapterDir := filepath.Join(moduleRoot, "internal", "sandbox", "msb")
	var offenders []string
	err := filepath.WalkDir(moduleRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		if path != moduleRoot {
			name := entry.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" {
				return filepath.SkipDir
			}
			if _, statErr := os.Stat(filepath.Join(path, "go.mod")); statErr == nil {
				return filepath.SkipDir
			}
		}
		// The adapter itself owns the SDK.
		if filepath.ToSlash(path) == filepath.ToSlash(adapterDir) {
			return filepath.SkipDir
		}
		if !productionImportsSDK(path, sdkPath) {
			return nil
		}
		relative, relErr := filepath.Rel(moduleRoot, path)
		if relErr != nil {
			relative = path
		}
		offenders = append(offenders, filepath.ToSlash(relative))
		return nil
	})
	return offenders, err
}

// TestSDKBoundaryDetectsAnImporter is the positive control for the scan: the
// adapter itself imports the SDK, so the scan must report at least it. Without
// this, a broken scan could pass vacuously.
func TestSDKBoundaryDetectsAnImporter(t *testing.T) {
	adapterDir := filepath.Join("..", "..", "..", "internal", "sandbox", "msb")
	if !productionImportsSDK(adapterDir, sdkImportPath) {
		t.Fatal("boundary scan did not detect the adapter's own SDK import")
	}
}

// TestSDKBoundaryModuleScoping pins how the scan treats module boundaries: a
// subdirectory carrying its own go.mod is a separate module, not part of this
// one, so its SDK import must be ignored; a package in the scanned module that
// imports the SDK must still be reported.
func TestSDKBoundaryModuleScoping(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example/root\n")
	writeSdkImporter(t, filepath.Join(root, "internal", "pkg"))
	writeSdkImporter(t, filepath.Join(root, "spike", "probe"))
	writeFile(t, filepath.Join(root, "spike", "probe", "go.mod"), "module spike/probe\n")

	offenders, err := findSDKImporters(root, sdkImportPath)
	if err != nil {
		t.Fatalf("scan temp module: %v", err)
	}
	if len(offenders) != 1 || offenders[0] != "internal/pkg" {
		t.Fatalf("offenders = %v, want [internal/pkg]", offenders)
	}
}

// writeFile creates path and its parent directories with fixed content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// writeSdkImporter drops a minimal production file importing the SDK into dir.
func writeSdkImporter(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "main.go"),
		"package main\n\nimport _ \""+sdkImportPath+"\"\n\nfunc main() {}\n")
}

// productionImportsSDK reports whether any non-test Go file in dir imports sdkPath.
// It parses every file regardless of build constraints, because go/build's default
// context would skip the macOS-only sources on a Linux runner and let an SDK import
// there escape the boundary.
func productionImportsSDK(dir, sdkPath string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, parser.ImportsOnly)
		if parseErr != nil {
			continue
		}
		for _, importSpec := range file.Imports {
			if path, unquoteErr := strconv.Unquote(importSpec.Path.Value); unquoteErr == nil && path == sdkPath {
				return true
			}
		}
	}
	return false
}
