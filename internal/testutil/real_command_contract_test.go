package testutil

import (
	"go/ast"
	"go/token"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ambientTools are external commands whose behavior depends on the developer's
// machine — the global config, credentials, the ssh-agent, or an askpass program
// — rather than on the test fixture. A default-suite test must not execute one:
// it would read or prompt through the host's configuration, the way a
// commit-signing git config popped ksshaskpass mid-`make test`. Put such a test
// in <unit>_integration_test.go with `//go:build integration`.
var ambientTools = []string{"docker", "gh", "git", "gpg", "msb", "ssh"}

// realCommandUse records a default-suite test source that reaches a real
// external command.
type realCommandUse struct {
	pos  token.Position
	what string
}

// TestUntaggedTestsExecuteNoAmbientTool enforces the real-command half of the
// test-isolation contract: every *_test.go the default build compiles must not
// call testutil.RunGit/ConfigureRepo nor exec an ambient tool unless the
// statement carries, or has immediately above it, a reason-bearing
// //testenv:allow marker — the same in-place sanction the ambient gate reads, so
// the check needs no hand-maintained allowlist. The fixture tests below pin the
// scanner itself.
func TestUntaggedTestsExecuteNoAmbientTool(t *testing.T) {
	root := moduleRoot(t)
	modulePath := moduleImportPath(t, root)
	for _, use := range realCommandUses(t, root, modulePath, modulePackages(t, root)) {
		t.Errorf(
			"%s: default-suite test reaches a real ambient command (%s); add `%s <reason>` if intentional, or move it to <unit>_integration_test.go with //go:build integration",
			use.pos,
			use.what,
			testEnvAllowMarker,
		)
	}
}

// realCommandUses returns every ambient-command use in the *_test.go files the
// default build compiles. Files excluded from the default build (an
// integration- or msb-tagged file) are skipped.
func realCommandUses(t *testing.T, root, modulePath string, packages []modulePackage) []realCommandUse {
	t.Helper()
	var uses []realCommandUse
	for _, source := range defaultBuildTestSources(t, root, packages) {
		parsed := cachedParseGoFile(t, source.path, source.src)
		uses = append(
			uses,
			realCommandUsesIn(parsed.fset, parsed.file, modulePath, strings.Split(string(source.src), "\n"))...)
	}
	sort.Slice(uses, func(i, j int) bool { return uses[i].pos.String() < uses[j].pos.String() })
	return uses
}

// realCommandUsesIn reports the ambient-command uses in one parsed test file: a
// call to the shared real-git helper, or an exec of an ambient tool named by a
// string literal (Command's first argument, CommandContext's second). A use the
// file's source sanctions with a reason-bearing //testenv:allow marker is not
// reported, the same in-place escape the ambient gate honors.
func realCommandUsesIn(fset *token.FileSet, file *ast.File, modulePath string, lines []string) []realCommandUse {
	imports := importedPackagesByLocalName(file)
	execName := ""
	for local, importPath := range imports {
		if importPath == "os/exec" {
			execName = local
		}
	}
	markers := newTestEnvMarkers(fset, file, lines)
	statements := fileStatements(file)
	var uses []realCommandUse
	record := func(call *ast.CallExpr, what string) {
		if callIsSanctioned(fset, markers, statements, call) {
			return
		}
		uses = append(uses, realCommandUse{pos: fset.Position(call.Pos()), what: what})
	}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		// An unqualified call resolves inside the scanned file's own package, so
		// only a testutil test file can reach the helpers this way.
		if ident, ok := call.Fun.(*ast.Ident); ok {
			if file.Name.Name == "testutil" && (ident.Name == "RunGit" || ident.Name == "ConfigureRepo") {
				record(call, "testutil."+ident.Name)
			}
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if base, ok := selector.X.(*ast.Ident); ok {
			switch {
			case base.Name == execName && (selector.Sel.Name == "Command" || selector.Sel.Name == "CommandContext"):
				toolIndex := 0
				if selector.Sel.Name == "CommandContext" {
					toolIndex = 1
				}
				if tool := stringLiteralArg(call, toolIndex); slices.Contains(ambientTools, tool) {
					record(call, "exec."+selector.Sel.Name+"("+tool+")")
				}
			case imports[base.Name] == modulePath+"/internal/testutil" &&
				(selector.Sel.Name == "RunGit" || selector.Sel.Name == "ConfigureRepo"):
				record(call, "testutil."+selector.Sel.Name)
			}
		}
		return true
	})
	return uses
}

// stringLiteralArg returns the value of call's index-th argument when it is a
// string literal (interpreted or raw), and "" otherwise.
func stringLiteralArg(call *ast.CallExpr, index int) string {
	if index >= len(call.Args) {
		return ""
	}
	literal, ok := call.Args[index].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return ""
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return ""
	}
	return value
}

// TestRealCommandUses pins the scanner: an ambient exec (a raw-string command
// name included) or the real-git helper in a default-suite file is flagged —
// whether reached through a qualified or a same-package unqualified call — an
// integration-tagged file is skipped, and a non-ambient command (a shell
// interpreter) is allowed.
func TestRealCommandUses(t *testing.T) {
	const modulePath = "example.com/mod"

	tests := []struct {
		name string
		file string
		want []string
	}{
		{
			name: "direct git exec",
			file: `package example

import (
	"os/exec"
	"testing"
)

func TestX(t *testing.T) {
	exec.Command("git", "commit")
}
`,
			want: []string{"exec.Command(git)"},
		},
		{
			name: "git exec through CommandContext",
			file: `package example

import (
	"context"
	"os/exec"
	"testing"
)

func TestX(t *testing.T) {
	exec.CommandContext(context.Background(), "git", "commit")
}
`,
			want: []string{"exec.CommandContext(git)"},
		},
		{
			name: "a raw-string git exec is flagged",
			file: `package example

import (
	"os/exec"
	"testing"
)

func TestX(t *testing.T) {
	exec.Command(` + "`git`" + `, "commit")
}
`,
			want: []string{"exec.Command(git)"},
		},
		{
			name: "the shared real-git helper",
			file: `package example

import (
	"testing"

	"example.com/mod/internal/testutil"
)

func TestX(t *testing.T) {
	testutil.RunGit(t, t.TempDir(), "status")
}
`,
			want: []string{"testutil.RunGit"},
		},
		{
			name: "a same-package unqualified helper call",
			file: `package testutil

import "testing"

func TestX(t *testing.T) {
	RunGit(t, t.TempDir(), "status")
}
`,
			want: []string{"testutil.RunGit"},
		},
		{
			name: "a same-package unqualified ConfigureRepo call",
			file: `package testutil

import "testing"

func TestX(t *testing.T) {
	ConfigureRepo(t, t.TempDir())
}
`,
			want: []string{"testutil.ConfigureRepo"},
		},
		{
			name: "a non-ambient shell interpreter is allowed",
			file: `package example

import (
	"os/exec"
	"testing"
)

func TestX(t *testing.T) {
	exec.Command("sh", "-c", "true")
}
`,
			want: nil,
		},
		{
			name: "the go-git fixture builder is allowed",
			file: `package example

import (
	"testing"

	"example.com/mod/internal/testutil"
)

func TestX(t *testing.T) {
	testutil.InitRepo(t)
}
`,
			want: nil,
		},
		{
			name: "an integration-tagged ambient exec is skipped",
			file: `//go:build integration

package example

import (
	"os/exec"
	"testing"
)

func TestX(t *testing.T) {
	exec.Command("git", "commit")
}
`,
			want: nil,
		},
		{
			name: "a marker with a reason sanctions an ambient exec",
			file: `package example

import (
	"os/exec"
	"testing"
)

func TestX(t *testing.T) {
	exec.Command("docker", "rmi", "tag") //testenv:allow the smoke suite removes real runner images
}
`,
			want: nil,
		},
		{
			name: "a marker without a reason does not sanction an ambient exec",
			file: `package example

import (
	"os/exec"
	"testing"
)

func TestX(t *testing.T) {
	exec.Command("git", "commit") //testenv:allow
}
`,
			want: []string{"exec.Command(git)"},
		},
		{
			name: "a standalone marker above the statement sanctions an ambient exec",
			file: `package example

import (
	"os/exec"
	"testing"
)

func TestX(t *testing.T) {
	//testenv:allow the smoke suite drives the real docker daemon
	exec.Command("docker", "rmi", "tag")
}
`,
			want: nil,
		},
		{
			name: "a marker sanctions the shared real-git helper",
			file: `package testutil

import "testing"

func TestX(t *testing.T) {
	RunGit(t, t.TempDir(), "status") //testenv:allow the fixture pins the git it drives
}
`,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeFixtureFile(t, root, "internal/example/example_test.go", tt.file)

			var got []string
			for _, use := range realCommandUses(t, root, modulePath, modulePackages(t, root)) {
				got = append(got, use.what)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("realCommandUses() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestRealCommandScanCoversNonTestSourcesUnderTest pins the scan scope for the
// ambient-tool half: a non-test .go file under test/ is scanned like a
// *_test.go file, so the shared untagged driver package cannot hide an ambient
// command behind a non-test file name. A file outside test/ is still scanned
// only when it is a *_test.go file, and the default-build guard keeps an
// msb-tagged file under test/ out of the scan.
func TestRealCommandScanCoversNonTestSourcesUnderTest(t *testing.T) {
	const modulePath = "example.com/mod"

	tests := []struct {
		name string
		rel  string
		file string
		want []string
	}{
		{
			name: "a non-test file under test/ is scanned",
			rel:  "test/foo/helper.go",
			file: `package foo

import "os/exec"

func Helper() { exec.Command("git", "status") }
`,
			want: []string{"exec.Command(git)"},
		},
		{
			name: "a marker sanctions a non-test file under test/",
			rel:  "test/foo/helper.go",
			file: `package foo

import "os/exec"

func Helper() {
	exec.Command("git", "status") //testenv:allow the fixture pins the git the driver drives
}
`,
			want: nil,
		},
		{
			name: "an msb-tagged file under test/ is not scanned",
			rel:  "test/foo/helper.go",
			file: `//go:build msb

package foo

import "os/exec"

func Helper() { exec.Command("git", "status") }
`,
			want: nil,
		},
		{
			name: "a non-test file outside test/ is not scanned",
			rel:  "internal/foo/helper.go",
			file: `package foo

import "os/exec"

func Helper() { exec.Command("git", "status") }
`,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeFixtureFile(t, root, tt.rel, tt.file)

			var got []string
			for _, use := range realCommandUses(t, root, modulePath, modulePackages(t, root)) {
				got = append(got, use.what)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("realCommandUses() = %v, want %v", got, tt.want)
			}
		})
	}
}
