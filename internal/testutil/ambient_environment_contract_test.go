package testutil

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// testEnvAllowMarker sanctions one default-suite test statement's ambient use of
// the host. It mirrors the dead-code gate's //deadcode:ignore: the marker must
// carry a reason, so every sanctioned read of the host records why it is safe. A
// new accidental leak fails TestUntaggedTestsReachNoUnsanctionedEnvironment
// instead of reaching the host.
const testEnvAllowMarker = "//testenv:allow"

// ambientNetCalls are the net package's functions that open a real socket or
// resolve a real name; an httptest server is addressed through its own client,
// not these.
var ambientNetCalls = map[string]bool{
	"Dial": true, "DialTimeout": true, "Listen": true, "ListenPacket": true,
	"LookupHost": true, "LookupIP": true, "LookupAddr": true,
	"LookupCNAME": true, "LookupMX": true, "LookupNS": true,
	"LookupSRV": true, "LookupTXT": true,
}

// ambientEnvCalls are the os package's process-environment reads and writes.
// Go's testing package owns this surface: t.Setenv reads and restores through it,
// so a direct call bypasses the per-test guard and leaks across tests.
var ambientEnvCalls = map[string]bool{
	"Getenv": true, "LookupEnv": true, "Setenv": true, "Unsetenv": true, "Environ": true,
}

// ambientDeviceCalls are the os package's file functions, flagged only when the
// path argument is a literal device path (under /dev), which is host state no
// temp-dir fixture can imitate.
var ambientDeviceCalls = map[string]bool{
	"Open": true, "OpenFile": true, "Stat": true, "Lstat": true,
	"ReadFile": true, "ReadDir": true, "Readlink": true,
}

// hostProbe identifies a module-local function that reads a real host figure
// from the machine the suite runs on (installed RAM, CPU count, available
// memory). A read reached only through production code is invisible to a scan of
// test sources, so the durable rule is to seam such a production read (as
// session's bootMaxMemoryMiB does); the direct call a test makes is flagged here
// and is then either seamed or marked with a reason.
type hostProbe struct {
	importSuffix string
	name         string
}

var hostProbeCalls = []hostProbe{
	{importSuffix: "/internal/sysinfo", name: "NumCPUs"},
	{importSuffix: "/internal/sysinfo", name: "TotalMemoryGiB"},
	{importSuffix: "/internal/sysinfo", name: "AvailableMemoryMiB"},
	{importSuffix: "/internal/sandbox/vm", name: "BootMaxMemoryMiB"},
}

// isHostProbe reports whether a call is a known host-figure read: the import
// path ends with the owning package and the selector is the probe's name. The
// suffix match keeps the checker independent of the module path.
func isHostProbe(importPath, name string) bool {
	for _, probe := range hostProbeCalls {
		if name == probe.name && strings.HasSuffix(importPath, probe.importSuffix) {
			return true
		}
	}
	return false
}

// ambientUse records a default-suite test source location that reaches the real
// environment without a //testenv:allow reason.
type ambientUse struct {
	pos  token.Position
	what string
}

// TestUntaggedTestsReachNoUnsanctionedEnvironment enforces the ambient half of
// the test-isolation contract: a *_test.go the default build compiles must not
// open a real socket, read or write the process environment, stat a device path,
// send a signal, exec an absolute-path binary, or read a host figure through a
// known host-probe function unless the line carries a reason-bearing
// //testenv:allow marker. The check reads the source, so a site is sanctioned in
// place rather than through a maintained allowlist; the fixture test below pins
// the scanner.
func TestUntaggedTestsReachNoUnsanctionedEnvironment(t *testing.T) {
	root := moduleRoot(t)
	for _, use := range ambientUses(t, root, modulePackages(t, root)) {
		t.Errorf(
			"%s: default-suite test reaches the ambient environment (%s); add `%s <reason>` if intentional, or move it behind a seam",
			use.pos,
			use.what,
			testEnvAllowMarker,
		)
	}
}

// ambientUses returns every unsanctioned ambient-environment use in the
// *_test.go files the default build compiles. Files excluded from the default
// build (an integration- or msb-tagged file) are skipped.
func ambientUses(t *testing.T, root string, packages []modulePackage) []ambientUse {
	t.Helper()
	var uses []ambientUse
	for _, source := range defaultBuildTestSources(t, root, packages) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, source.path, source.src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", source.path, err)
		}
		uses = append(uses, ambientEnvironmentUsesIn(fset, file, strings.Split(string(source.src), "\n"))...)
	}
	sort.Slice(uses, func(i, j int) bool { return uses[i].pos.String() < uses[j].pos.String() })
	return uses
}

// ambientEnvironmentUsesIn reports the ambient-environment uses in one parsed
// test file that its source lines do not sanction with a reason-bearing
// //testenv:allow marker. A marker sanctions the statement it sits in, so a
// formatter that wraps a call does not detach the marker from it.
func ambientEnvironmentUsesIn(fset *token.FileSet, file *ast.File, lines []string) []ambientUse {
	imports := importedPackagesByLocalName(file)
	statements := fileStatements(file)
	var uses []ambientUse
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		base, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		what := ambientCallKind(imports[base.Name], selector.Sel.Name, call)
		if what == "" {
			return true
		}
		if callIsSanctioned(fset, lines, statements, call) {
			return true
		}
		uses = append(uses, ambientUse{pos: fset.Position(call.Pos()), what: what})
		return true
	})
	return uses
}

// fileStatements returns every statement in the file, so a call can find the
// statements whose source range contains it.
func fileStatements(file *ast.File) []ast.Stmt {
	var statements []ast.Stmt
	ast.Inspect(file, func(node ast.Node) bool {
		if statement, ok := node.(ast.Stmt); ok {
			statements = append(statements, statement)
		}
		return true
	})
	return statements
}

// callIsSanctioned reports whether a //testenv:allow marker sanctions call: on a
// line within the call itself, or on a line of the innermost statement that
// contains it (or immediately above that statement). The innermost statement is
// the unit a reader reasons about ("this statement intentionally touches the
// host"); a marker elsewhere in the enclosing function must not sanction it.
func callIsSanctioned(fset *token.FileSet, lines []string, statements []ast.Stmt, call *ast.CallExpr) bool {
	start, end := fset.Position(call.Pos()).Line, fset.Position(call.End()).Line
	if lineRangeHasTestEnvAllow(lines, start, end) || precedingLineHasTestEnvAllow(lines, start) {
		return true
	}
	statement := innermostContainingStatement(statements, call)
	if statement == nil {
		return false
	}
	start, end = fset.Position(statement.Pos()).Line, fset.Position(statement.End()).Line
	return precedingLineHasTestEnvAllow(lines, start) || lineRangeHasTestEnvAllow(lines, start, end)
}

// innermostContainingStatement returns the smallest non-block statement whose
// source range contains call. Block, case and comm clauses are containers, not
// the statement a reader attaches intent to, so a marker inside one must not
// sanction every call in the whole function.
func innermostContainingStatement(statements []ast.Stmt, call *ast.CallExpr) ast.Stmt {
	var innermost ast.Stmt
	for _, statement := range statements {
		switch statement.(type) {
		case *ast.BlockStmt, *ast.CaseClause, *ast.CommClause:
			continue
		}
		if statement.Pos() > call.Pos() || call.End() > statement.End() {
			continue
		}
		if innermost == nil || statement.End()-statement.Pos() < innermost.End()-innermost.Pos() {
			innermost = statement
		}
	}
	return innermost
}

// ambientCallKind classifies one selector call against the ambient surfaces,
// returning a human-readable description or "" for a call that is not ambient.
func ambientCallKind(importPath, name string, call *ast.CallExpr) string {
	switch {
	case importPath == "net" && ambientNetCalls[name]:
		return "net." + name
	case importPath == "os" && ambientEnvCalls[name]:
		return "os." + name
	case importPath == "os" && ambientDeviceCalls[name] && isDevPath(stringLiteralArg(call, 0)):
		return "os." + name + "(device path)"
	case importPath == "syscall" && name == "Kill":
		return "syscall.Kill"
	case importPath == "os/exec" && (name == "Command" || name == "CommandContext"):
		toolIndex := 0
		if name == "CommandContext" {
			toolIndex = 1
		}
		if tool := stringLiteralArg(call, toolIndex); strings.HasPrefix(tool, "/") {
			return "exec." + name + "(" + tool + ")"
		}
	case isHostProbe(importPath, name):
		return path.Base(importPath) + "." + name
	}
	return ""
}

// isDevPath reports whether path names a device under /dev, which resolves to
// real host state rather than to a test's temp tree.
func isDevPath(path string) bool {
	return strings.HasPrefix(path, "/dev/")
}

// lineRangeHasTestEnvAllow reports whether any line in the inclusive 1-based
// range [start,end] carries a //testenv:allow marker with a non-empty reason.
// Scanning the range covers a call or statement that a formatter wrapped, so the
// marker can sit anywhere in it rather than only on its first line.
func lineRangeHasTestEnvAllow(lines []string, start, end int) bool {
	for line := start; line <= end; line++ {
		if lineHasTestEnvAllow(lines, line) {
			return true
		}
	}
	return false
}

// precedingLineHasTestEnvAllow reports whether the line immediately above start
// is a standalone //testenv:allow marker, the form that keeps a wrapped
// statement readable. A trailing marker on the previous statement is not a
// standalone marker for this one.
func precedingLineHasTestEnvAllow(lines []string, start int) bool {
	line := start - 1
	if line < 1 || line > len(lines) {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(lines[line-1]), testEnvAllowMarker) &&
		lineHasTestEnvAllow(lines, line)
}

// lineHasTestEnvAllow reports whether the 1-based source line carries a
// //testenv:allow marker followed by a reason. A bare marker, or a longer word
// such as "allowance", is not a sanction.
func lineHasTestEnvAllow(lines []string, line int) bool {
	if line < 1 || line > len(lines) {
		return false
	}
	_, rest, found := strings.Cut(lines[line-1], testEnvAllowMarker)
	if !found || rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
		return false
	}
	reason := strings.TrimSpace(rest)
	return reason != "" && reason != "TODO" && reason != "FIXME"
}

// defaultBuildTestSources returns the source of every *_test.go file the default
// build compiles, so a scanner can read both the AST and the raw lines.
func defaultBuildTestSources(t *testing.T, root string, packages []modulePackage) []testSource {
	t.Helper()
	var sources []testSource
	for _, pkg := range packages {
		if !pkg.hasTests {
			continue
		}
		dir := filepath.Join(root, pkg.dir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", pkg.dir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, "_test.go") {
				continue
			}
			inDefaultBuild, err := build.Default.MatchFile(dir, name)
			if err != nil {
				t.Fatalf("match %s: %v", filepath.Join(pkg.dir, name), err)
			}
			if !inDefaultBuild {
				continue
			}
			path := filepath.Join(dir, name)
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			sources = append(sources, testSource{path: path, src: src})
		}
	}
	return sources
}

// testSource is one default-build test file's path and contents.
type testSource struct {
	path string
	src  []byte
}

// TestAmbientEnvironmentUses pins the scanner: each ambient surface is flagged
// (a socket, the process environment, a device path, a signal, an absolute-path
// exec, a host-figure probe), a //testenv:allow marker with a reason on the
// statement sanctions it, a marker without a reason does not, a device-path
// check is flagged while an ordinary temp path is not, and an absolute-path exec
// is flagged while a bare non-ambient tool and os.Args[0] are not. The
// default-build file filter itself is exercised by TestRealCommandUses.
func TestAmbientEnvironmentUses(t *testing.T) {
	tests := []struct {
		name string
		file string
		want []string
	}{
		{
			name: "a real socket bind is flagged",
			file: `package example
import (
	"net"
	"testing"
)
func TestX(t *testing.T) { _, _ = net.Listen("tcp", "127.0.0.1:0") }
`,
			want: []string{"net.Listen"},
		},
		{
			name: "a name lookup is flagged",
			file: `package example
import (
	"net"
	"testing"
)
func TestX(t *testing.T) { _, _ = net.LookupHost("example.com") }
`,
			want: []string{"net.LookupHost"},
		},
		{
			name: "a process-environment read is flagged",
			file: `package example
import (
	"os"
	"testing"
)
func TestX(t *testing.T) { _ = os.Getenv("HOME") }
`,
			want: []string{"os.Getenv"},
		},
		{
			name: "a process-environment write is flagged",
			file: `package example
import (
	"os"
	"testing"
)
func TestX(t *testing.T) { _ = os.Unsetenv("HOME") }
`,
			want: []string{"os.Unsetenv"},
		},
		{
			name: "a device-path stat is flagged",
			file: `package example
import (
	"os"
	"testing"
)
func TestX(t *testing.T) { _, _ = os.Stat("/dev/kvm") }
`,
			want: []string{"os.Stat(device path)"},
		},
		{
			name: "an ordinary temp-dir path is not flagged",
			file: `package example
import (
	"os"
	"testing"
)
func TestX(t *testing.T) { _, _ = os.Stat("/tmp/whatever") }
`,
			want: nil,
		},
		{
			name: "a process signal is flagged",
			file: `package example
import (
	"syscall"
	"testing"
)
func TestX(t *testing.T) { _ = syscall.Kill(1, syscall.SIGINT) }
`,
			want: []string{"syscall.Kill"},
		},
		{
			name: "an absolute-path exec is flagged",
			file: `package example
import (
	"os/exec"
	"testing"
)
func TestX(t *testing.T) { _ = exec.Command("/bin/true") }
`,
			want: []string{"exec.Command(/bin/true)"},
		},
		{
			name: "an absolute-path exec through CommandContext is flagged",
			file: `package example
import (
	"context"
	"os/exec"
	"testing"
)
func TestX(t *testing.T) { _ = exec.CommandContext(context.Background(), "/bin/true") }
`,
			want: []string{"exec.CommandContext(/bin/true)"},
		},
		{
			name: "a host-probe read is flagged",
			file: `package example
import (
	"testing"

	"example.com/mod/internal/sysinfo"
)
func TestX(t *testing.T) { _ = sysinfo.NumCPUs() }
`,
			want: []string{"sysinfo.NumCPUs"},
		},
		{
			name: "a host-probe read through another package is flagged",
			file: `package example
import (
	"testing"

	"example.com/mod/internal/sandbox/vm"
)
func TestX(t *testing.T) { _ = vm.BootMaxMemoryMiB() }
`,
			want: []string{"vm.BootMaxMemoryMiB"},
		},
		{
			name: "a marker sanctions a host-probe read",
			file: `package example
import (
	"testing"

	"example.com/mod/internal/sysinfo"
)
func TestX(t *testing.T) { _ = sysinfo.NumCPUs() } //testenv:allow asserts the host CPU default
`,
			want: nil,
		},
		{
			name: "a bare shell interpreter and os.Args[0] are not flagged",
			file: `package example
import (
	"os"
	"os/exec"
	"testing"
)
func TestX(t *testing.T) {
	_ = exec.Command("sh", "-c", "true")
	_ = exec.Command(os.Args[0], "-test.run=TestX")
}
`,
			want: nil,
		},
		{
			name: "a marker with a reason sanctions the call",
			file: `package example
import (
	"os"
	"testing"
)
func TestX(t *testing.T) { _ = os.Getenv("MSB_HOME") //testenv:allow the assertion pins the value ApplyHome wrote
}
`,
			want: nil,
		},
		{
			name: "a marker without a reason does not sanction the call",
			file: `package example
import (
	"os"
	"testing"
)
func TestX(t *testing.T) { _ = os.Getenv("HOME") //testenv:allow
}
`,
			want: []string{"os.Getenv"},
		},
		{
			name: "a marker on a wrapped statement's closing line still sanctions it",
			file: `package example
import (
	"os"
	"testing"
)
func TestX(t *testing.T) {
	_ = os.Getenv(
		"HOME",
	) //testenv:allow the statement keeps its marker when wrapped
}
`,
			want: nil,
		},
		{
			name: "a marker elsewhere in the same function does not sanction the call",
			file: `package example
import (
	"os"
	"testing"
)
func TestX(t *testing.T) {
	_ = os.Getenv("SANCTIONED") //testenv:allow the first call is intentional
	_ = os.Getenv("ACCIDENTAL")
}
`,
			want: []string{"os.Getenv"},
		},
		{
			name: "a marker on an enclosing block does not sanction a call in the block header",
			file: `package example
import (
	"os"
	"testing"
)
func TestX(t *testing.T) {
	if got := os.Getenv(
		"HOME",
	); got != "" {
		_ = got
	} //testenv:allow this marker belongs to the body, not the init call
}
`,
			want: []string{"os.Getenv"},
		},
		{
			name: "a standalone marker above the statement sanctions it",
			file: `package example
import (
	"os"
	"testing"
)
func TestX(t *testing.T) {
	//testenv:allow a standalone marker sanctions the statement below
	_ = os.Getenv("HOME")
}
`,
			want: nil,
		},
		{
			name: "a marker that is only a prefix of a longer word does not sanction the call",
			file: `package example
import (
	"os"
	"testing"
)
func TestX(t *testing.T) { _ = os.Getenv("HOME") //testenv:allowance is not a marker
}
`,
			want: []string{"os.Getenv"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "example_test.go", tt.file, 0)
			if err != nil {
				t.Fatalf("parse fixture: %v", err)
			}
			var got []string
			for _, use := range ambientEnvironmentUsesIn(fset, file, strings.Split(tt.file, "\n")) {
				got = append(got, use.what)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("ambientEnvironmentUsesIn() = %v, want %v", got, tt.want)
			}
		})
	}
}
