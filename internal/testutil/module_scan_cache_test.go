package testutil

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sync"
	"testing"
)

// This file memoizes the module-wide scans the contract tests share. Four tests
// (TestUntaggedTestsExecuteNoAmbientTool, TestUntaggedTestsReachNoUnsanctionedEnvironment,
// TestEveryTestPackageInstallsItsAcyclicFailFastMocks, TestSeamFamiliesMatchDeclaredInstallers)
// each walk the module, read its sources, and parse them; run serially, that
// repeated I/O and parsing is the package's dominant cost. The module is static
// for the life of a test binary, so a memoized result cannot go stale. The
// fixture tests build a fresh temp root and a fresh file path per subtest, so
// their requests never collide with a cached module scan.

var (
	modulePackageCache     sync.Map // root -> []modulePackage
	defaultTestSourceCache sync.Map // root -> []testSource
	parsedModuleFileCache  sync.Map // path -> parsedModuleFile
)

// parsedModuleFile is one parsed Go source file with the FileSet its positions
// are relative to.
type parsedModuleFile struct {
	fset *token.FileSet
	file *ast.File
}

// modulePackages memoizes scanModulePackages by module root.
func modulePackages(t *testing.T, root string) []modulePackage {
	t.Helper()
	if cached, ok := modulePackageCache.Load(root); ok {
		return cached.([]modulePackage)
	}
	packages := scanModulePackages(t, root)
	modulePackageCache.Store(root, packages)
	return packages
}

// defaultBuildTestSources memoizes scanDefaultBuildTestSources by module root.
func defaultBuildTestSources(t *testing.T, root string, _ []modulePackage) []testSource {
	t.Helper()
	if cached, ok := defaultTestSourceCache.Load(root); ok {
		return cached.([]testSource)
	}
	sources := scanDefaultBuildTestSources(t, root, modulePackages(t, root))
	defaultTestSourceCache.Store(root, sources)
	return sources
}

// cachedParseGoFile parses path once and memoizes the result. src may be nil, in
// which case the file is read from disk. Comments are retained so both the
// ambient-marker scan and the plain AST scans can share one parse.
func cachedParseGoFile(t *testing.T, path string, src []byte) parsedModuleFile {
	t.Helper()
	parsed, err := loadParsedModuleFile(path, src)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return parsed
}

// loadParsedModuleFile is cachedParseGoFile without a testing.TB, so a caller
// that treats a read or parse failure as data (installedSeamFamilies) can
// inspect the error instead of failing the test.
func loadParsedModuleFile(path string, src []byte) (parsedModuleFile, error) {
	if cached, ok := parsedModuleFileCache.Load(path); ok {
		return cached.(parsedModuleFile), nil
	}
	if src == nil {
		read, err := os.ReadFile(path)
		if err != nil {
			return parsedModuleFile{}, err
		}
		src = read
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return parsedModuleFile{}, err
	}
	parsed := parsedModuleFile{fset: fset, file: file}
	parsedModuleFileCache.Store(path, parsed)
	return parsed, nil
}
