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
	sdkPath := sdkImportPath
	moduleRoot := filepath.Join("..", "..", "..")
	adapterDir := filepath.Join(moduleRoot, "internal", "sandbox", "msb")

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
		relative = filepath.ToSlash(relative)
		t.Errorf(
			"%s imports the microsandbox SDK; only internal/sandbox/msb may",
			relative,
		)
		return nil
	})
	if err != nil {
		t.Fatalf("scan module for SDK imports: %v", err)
	}
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
