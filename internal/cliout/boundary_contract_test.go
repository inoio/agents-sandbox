package cliout

import (
	"go/build"
	"strings"
	"testing"
)

// TestPackageStaysDomainFree pins the package boundary from issue #357: cliout is
// the reusable renderer, so its non-test sources must not import a sandbox domain
// package. The domain Printer implementations live in internal/view.
func TestPackageStaysDomainFree(t *testing.T) {
	pkg, err := build.Default.ImportDir(".", build.ImportComment)
	if err != nil {
		t.Fatalf("import dir: %v", err)
	}
	const domainPrefix = "github.com/inoio/agents-sandbox/internal/sandbox"
	for _, imported := range pkg.Imports {
		if imported == domainPrefix || strings.HasPrefix(imported, domainPrefix+"/") {
			t.Errorf("cliout imports domain package %s; move that Printer to the view package", imported)
		}
	}
}
