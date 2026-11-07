package testutil

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"sort"
	"strconv"
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
// call testutil.RunGit/ConfigureRepo nor exec an ambient tool. The check reads
// the source, so it needs no hand-maintained exception list; the fixture tests
// below pin the scanner itself.
func TestUntaggedTestsExecuteNoAmbientTool(t *testing.T) {
	root := moduleRoot(t)
	modulePath := moduleImportPath(t, root)
	for _, use := range realCommandUses(t, root, modulePath, modulePackages(t, root)) {
		t.Errorf(
			"%s: default-suite test reaches a real ambient command (%s); move it to <unit>_integration_test.go with //go:build integration",
			use.pos,
			use.what,
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
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, source.path, source.src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", source.path, err)
		}
		uses = append(uses, realCommandUsesIn(fset, file, modulePath)...)
	}
	sort.Slice(uses, func(i, j int) bool { return uses[i].pos.String() < uses[j].pos.String() })
	return uses
}

// realCommandUsesIn reports the ambient-command uses in one parsed test file: a
// call to the shared real-git helper, or an exec of an ambient tool named by a
// string literal (Command's first argument, CommandContext's second).
func realCommandUsesIn(fset *token.FileSet, file *ast.File, modulePath string) []realCommandUse {
	imports := importedPackagesByLocalName(file)
	execName := ""
	for local, importPath := range imports {
		if importPath == "os/exec" {
			execName = local
		}
	}
	var uses []realCommandUse
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		// An unqualified call resolves inside the scanned file's own package, so
		// only a testutil test file can reach the helpers this way.
		if ident, ok := call.Fun.(*ast.Ident); ok {
			if file.Name.Name == "testutil" && (ident.Name == "RunGit" || ident.Name == "ConfigureRepo") {
				uses = append(uses, realCommandUse{
					pos:  fset.Position(call.Pos()),
					what: "testutil." + ident.Name,
				})
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
					uses = append(uses, realCommandUse{
						pos:  fset.Position(call.Pos()),
						what: "exec." + selector.Sel.Name + "(" + tool + ")",
					})
				}
			case imports[base.Name] == modulePath+"/internal/testutil" &&
				(selector.Sel.Name == "RunGit" || selector.Sel.Name == "ConfigureRepo"):
				uses = append(uses, realCommandUse{
					pos:  fset.Position(call.Pos()),
					what: "testutil." + selector.Sel.Name,
				})
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
