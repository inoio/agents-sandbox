package testutil

import (
	"os/exec"
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
// files. `go list` names the standard library's `testing` as a bare path, so the
// exact-match list is enough — unlike the historical underscore-prefixed build
// artifacts, a test-support package here sits on a plain import path.
var testOnlyDependencies = []string{
	"testing",
	"github.com/inoio/agents-sandbox/internal/testutil",
}

// TestProductionClosureLinksNoTesting enforces the "nothing test-only ships"
// convention on the CLI's production closure: whatever a consumer builds from
// ./cmd/agents-sandbox must not link testing or a test-support package. It
// reads the import graph only — no build, no VM — so it is cheap enough for the
// default suite. The untagged test-support package under test/ (the shared
// launch driver) relies on this gate: a production package that imported it
// would link testing into every binary.
func TestProductionClosureLinksNoTesting(t *testing.T) {
	root := moduleRoot(t)

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

// isTestOnlyDependency reports whether the dependency path is one the production
// closure must not link: `testing` itself, or a test-support package — either
// the vendored gopkg.in/yaml-style path with a /testing suffix (a package whose
// last element is testing, e.g. a dependency's own test helper) or an
// internal/testutil package and its subtree.
func isTestOnlyDependency(path string) bool {
	if slices.Contains(testOnlyDependencies, path) {
		return true
	}
	if path == "testing" || strings.HasSuffix(path, "/testing") {
		return true
	}
	return strings.HasPrefix(path, "github.com/inoio/agents-sandbox/internal/testutil")
}
