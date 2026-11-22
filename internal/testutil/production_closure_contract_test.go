package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Production closure of the CLI: the packages `go build ./cmd/agents-sandbox`
// links, without the -test variant. `go list -deps` reads the import graph and
// prints one package path per line.
const launcherPackage = "./cmd/agents-sandbox"

// testOnlyDependencies are the packages the production closure must not link.
// `testing` pulls in flag and os/exec's test plumbing; a test-support package
// (internal/testutil and its subtree) exists only to be imported by _test.go
// files.
var testOnlyDependencies = []string{
	"testing",
	"github.com/inoio/agents-sandbox/internal/testutil",
}

// TestProductionClosureLinksNoTesting enforces the "nothing test-only ships"
// convention on the CLI's production closure: whatever a consumer builds from
// ./cmd/agents-sandbox must not link testing or a test-support package. The
// untagged test-support package under test/ (the shared launch driver) relies
// on this gate: a production package that imported it would link testing into
// every binary.
func TestProductionClosureLinksNoTesting(t *testing.T) {
	root := moduleRoot(t)

	// Go's test cache tracks the files a test opens and the environment it
	// reads, but not what a subprocess reads, so a `go list` invocation alone
	// left the gate cache-blind: editing a production source reused a stale
	// PASS. Reading the module's production sources here makes every one of them
	// a tracked input, so changing cmd/ — the realistic regression, a
	// production import of `testing` — invalidates the cached result.
	readProductionSources(t, root)

	dependencies := productionClosure(t, root)

	for _, dependency := range dependencies {
		if isTestOnlyDependency(dependency) {
			t.Errorf(
				"%s: the production closure links %s; a test-only package must not reach production code",
				launcherPackage,
				dependency,
			)
		}
	}
}

// readProductionSources opens every non-test .go file in the module, so the test
// cache records them as inputs. It deliberately reads rather than parses: the
// content is irrelevant, only the read is, and os.ReadFile is the cheapest way
// to record it.
func readProductionSources(t *testing.T, root string) {
	t.Helper()
	read := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root {
				if name := entry.Name(); name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		if _, err := os.ReadFile(path); err != nil {
			return err
		}
		read++
		return nil
	})
	if err != nil {
		t.Fatalf("read production sources: %v", err)
	}
	if read == 0 {
		t.Fatal("no production Go sources found; is the module root correct?")
	}
}

// productionClosure returns the package paths `go build ./cmd/agents-sandbox`
// links, without the -test variant.
func productionClosure(t *testing.T, root string) []string {
	t.Helper()
	list := exec.Command("go", "list", "-deps", launcherPackage)
	list.Dir = root
	out, err := list.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps %s: %v\n%s", launcherPackage, err, out)
	}
	dependencies := strings.Fields(string(out))
	if len(dependencies) == 0 {
		t.Fatalf("go list -deps %s printed no dependencies", launcherPackage)
	}
	return dependencies
}

// isTestOnlyDependency reports whether the dependency path is one the production
// closure must not link: `testing` itself, a package whose final path element is
// testing (a dependency's own test helper), or an internal/testutil package and
// its subtree. The trailing slash on the testutil prefix keeps an unrelated
// `internal/testutilx` from matching.
func isTestOnlyDependency(path string) bool {
	if slices.Contains(testOnlyDependencies, path) {
		return true
	}
	if strings.HasSuffix(path, "/testing") {
		return true
	}
	return strings.HasPrefix(path, "github.com/inoio/agents-sandbox/internal/testutil/")
}
