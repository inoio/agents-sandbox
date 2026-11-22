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

// Driver closure of the shared untagged launch driver the msb suites import.
// It is test-support code that lives in non-test .go files, so the same
// "nothing test-only ships" rule must cover what it imports: a production
// package that leaked into it would link testing into every suite binary.
const driverPackage = "./test/msb/driver"

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
// ./cmd/agents-sandbox must not link testing or a test-support package. A
// production package that imported the untagged test-support package under
// test/ (the shared launch driver) would link testing into every binary; the
// driver's own closure is checked by TestDriverClosureLinksNoTesting.
func TestProductionClosureLinksNoTesting(t *testing.T) {
	root := moduleRoot(t)

	// Go's test cache tracks the files a test opens and the environment it
	// reads, but not what a subprocess reads, so a `go list` invocation alone
	// left the gate cache-blind: editing a production source reused a stale
	// PASS. Reading the module's production sources here makes every one of them
	// a tracked input, so changing cmd/ — the realistic regression, a
	// production import of `testing` — invalidates the cached result.
	readProductionSources(t, root)

	dependencies := productionClosure(t, root, launcherPackage)

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

// TestDriverClosureLinksNoTesting is the sibling gate for the shared untagged
// driver: the spec names the cmd-closure check as "what keeps the shared driver
// honest", but a driver that imported a production package would not appear in
// the cmd closure at all. It reads the same production-source cache input — so
// editing a source invalidates the cached result the same way — and rejects
// testing and any test-support package from the driver's closure.
func TestDriverClosureLinksNoTesting(t *testing.T) {
	root := moduleRoot(t)

	readProductionSources(t, root)

	dependencies := productionClosure(t, root, driverPackage)

	for _, dependency := range dependencies {
		if isTestOnlyDependency(dependency) {
			t.Errorf(
				"%s: the driver closure links %s; a test-only package must not reach the shared driver's tests",
				driverPackage,
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

// productionClosure returns the package paths `go list -deps pkg` links,
// without the -test variant.
func productionClosure(t *testing.T, root, pkg string) []string {
	t.Helper()
	list := exec.Command("go", "list", "-deps", pkg)
	list.Dir = root
	out, err := list.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps %s: %v\n%s", pkg, err, out)
	}
	dependencies := strings.Fields(string(out))
	if len(dependencies) == 0 {
		t.Fatalf("go list -deps %s printed no dependencies", pkg)
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
