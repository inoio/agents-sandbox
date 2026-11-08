package msb

import (
	"go/build"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// sdkBoundaryResidue names the production packages that may still import the
// microsandbox SDK directly, with the reason. They are the deliberate exceptions
// to the phase-4 end state; a new entry is a decision, not a default.
var sdkBoundaryResidue = map[string]string{
	"internal/sandbox/runtime": "the runtime install/version surface (SDKVersion, " +
		"RuntimeConfig, EnsureRuntime/InstallRuntime) has no launcher-owned equivalent yet",
}

// sdkImportPath is the microsandbox SDK import no non-`msb` production package
// may use.
const sdkImportPath = "github.com/superradcompany/microsandbox/sdk/go"

// TestNoProductionPackageImportsSDK pins the phase-4 end state: with the
// launcher owning the whole config model, the microsandbox SDK is an
// implementation detail of internal/sandbox/msb alone. Every other package's
// non-test sources must be SDK-free, modulo sdkBoundaryResidue; see
// .operator-shared/specs/architecture/msb-seam.md.
//
// The scan uses go/build so a _test.go import of the SDK is never counted:
// Package.Imports covers non-test files only, with TestImports/XTestImports
// kept separate.
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
		if _, documented := sdkBoundaryResidue[relative]; documented {
			return nil
		}
		t.Errorf(
			"%s imports the microsandbox SDK; only internal/sandbox/msb and the documented residue (%v) may",
			relative, sdkBoundaryResidue,
		)
		return nil
	})
	if err != nil {
		t.Fatalf("scan module for SDK imports: %v", err)
	}
}

// TestSDKBoundaryResiduePackagesExist guards against a stale residue entry: a
// package named there must exist and must actually import the SDK, so the list
// is a live exemption rather than a silent hole. The import assertion is also the
// positive control for TestNoProductionPackageImportsSDK: it proves the scan
// detects a real production importer.
func TestSDKBoundaryResiduePackagesExist(t *testing.T) {
	for relative := range sdkBoundaryResidue {
		dir := filepath.Join("..", "..", "..", relative)
		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("residue package %s does not exist: %v", relative, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("residue path %s is not a directory", relative)
			continue
		}
		if !productionImportsSDK(dir, sdkImportPath) {
			t.Errorf("residue package %s does not import the SDK; drop it from sdkBoundaryResidue", relative)
		}
	}
}

// productionImportsSDK reports whether the package at dir imports sdkPath in its
// non-test sources. A directory that is not a buildable package (no buildable
// Go files, or a build error) reports false, so the walk skips non-package
// directories.
func productionImportsSDK(dir, sdkPath string) bool {
	pkg, err := build.ImportDir(dir, 0)
	if err != nil {
		// A non-package directory (no buildable Go files, or a build error) is
		// skipped, not a boundary violation.
		return false
	}
	return slices.Contains(pkg.Imports, sdkPath)
}
