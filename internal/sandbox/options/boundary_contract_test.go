package options

import (
	"go/build"
	"testing"
)

// TestPackageDoesNotImportState pins issue #532: the run-options aggregate must
// not depend on the state persistence package. The ProjectVMLock seam takes the
// leaf ProjectVMKey type, and vm adapts it to state.Key, so options stays clear
// of the state package's lock and state model.
func TestPackageDoesNotImportState(t *testing.T) {
	pkg, err := build.Default.ImportDir(".", build.ImportComment)
	if err != nil {
		t.Fatalf("import dir: %v", err)
	}
	const statePackage = "github.com/inoio/agents-sandbox/internal/sandbox/state"
	for _, imported := range pkg.Imports {
		if imported == statePackage {
			t.Errorf("options imports %s; type the ProjectVMLock seam with a leaf key type instead", imported)
		}
	}
}
