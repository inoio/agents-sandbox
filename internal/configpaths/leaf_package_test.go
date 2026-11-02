package configpaths

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestPackageImportsNoInternalPackages keeps path resolution a leaf (#364):
// domain decisions such as where a bare checkout keeps its project config are
// injected (UseBareCheckoutLocator) instead of imported, so every package can
// ask for a path without pulling in git or agent logic.
func TestPackageImportsNoInternalPackages(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, spec := range parsed.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("unquote import %s in %s: %v", spec.Path.Value, file, err)
			}
			if strings.HasPrefix(importPath, "github.com/inoio/agents-sandbox/") {
				t.Errorf("%s imports %s; configpaths must not depend on other internal packages", file, importPath)
			}
		}
	}
}
