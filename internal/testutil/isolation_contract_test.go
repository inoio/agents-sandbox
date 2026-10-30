package testutil

import (
	"errors"
	"fmt"
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

// fullSetInstaller is the wrapper that installs every seam family at once,
// declared in fullSetInstallerPackage.
const (
	fullSetInstaller        = "InitFailFastMocks"
	fullSetInstallerPackage = "internal/testutil/mocks"
)

// seamFamily ties a fail-fast family to the package that owns it and the
// exported declaration that installs it.
type seamFamily struct {
	name        string
	dir         string
	packageName string
	installer   string
}

var seamFamilies = []seamFamily{
	{
		name:        "configpaths",
		dir:         "internal/configpaths",
		packageName: "configpaths",
		installer:   "InstallFailFastConfigPaths",
	},
	{name: "docker", dir: "internal/sandbox/docker", packageName: "docker", installer: "InstallFailFastGet"},
	{name: "doctor", dir: "internal/sandbox/doctor", packageName: "doctor", installer: "InstallFailFast"},
	{name: "msb", dir: "internal/sandbox/msb", packageName: "msb", installer: "InstallFailFastGet"},
	{name: "notify", dir: "internal/notify", packageName: "notify", installer: "InstallFailFast"},
	{name: "opener", dir: "internal/opener", packageName: "opener", installer: "InstallFailFast"},
}

// installerPrefix names the exported declarations that install a seam family's
// fail-fast default. It is the convention that makes an installer discoverable,
// so a family whose installer does not carry it must still be added to
// seamFamilies by hand.
const installerPrefix = "InstallFailFast"

// registeredInstaller identifies a seam installer by where it is declared.
type registeredInstaller struct {
	dir         string
	packageName string
	installer   string
}

// declaredSeamInstallers returns every exported installer declaration in the
// module's non-test sources, so a family that exists in the code but not in
// seamFamilies fails instead of going unchecked.
func declaredSeamInstallers(t *testing.T, root string, packages []modulePackage) []registeredInstaller {
	t.Helper()
	var installers []registeredInstaller
	for _, pkg := range packages {
		if !pkg.hasNonTest {
			continue
		}
		dir := filepath.Join(root, pkg.dir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", pkg.dir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", filepath.Join(pkg.dir, name), err)
			}
			for _, decl := range file.Decls {
				installers = append(installers, declaredInstallersIn(pkg.dir, file.Name.Name, decl)...)
			}
		}
	}
	sortInstallers(installers)
	return installers
}

// declaredInstallersIn returns the installers declared by one declaration: an
// installer is an exported top-level identifier named with installerPrefix,
// whether a function or a package-level var, as configpaths declares it.
func declaredInstallersIn(dir, packageName string, decl ast.Decl) []registeredInstaller {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if d.Recv != nil || !strings.HasPrefix(d.Name.Name, installerPrefix) {
			return nil
		}
		return []registeredInstaller{{dir: dir, packageName: packageName, installer: d.Name.Name}}
	case *ast.GenDecl:
		var installers []registeredInstaller
		for _, spec := range d.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range value.Names {
				if strings.HasPrefix(name.Name, installerPrefix) {
					installers = append(installers, registeredInstaller{
						dir: dir, packageName: packageName, installer: name.Name,
					})
				}
			}
		}
		return installers
	}
	return nil
}

// registeredInstallers lists seamFamilies as declared installers.
func registeredInstallers() []registeredInstaller {
	installers := make([]registeredInstaller, 0, len(seamFamilies))
	for _, family := range seamFamilies {
		installers = append(installers, registeredInstaller{
			dir: family.dir, packageName: family.packageName, installer: family.installer,
		})
	}
	sortInstallers(installers)
	return installers
}

// sortInstallers orders installers by declaration site, since the same installer
// name is declared by more than one family (docker and msb).
func sortInstallers(installers []registeredInstaller) {
	sort.Slice(installers, func(i, j int) bool {
		if installers[i].dir != installers[j].dir {
			return installers[i].dir < installers[j].dir
		}
		return installers[i].installer < installers[j].installer
	})
}

// TestEveryTestPackageInstallsItsAcyclicFailFastMocks enforces the
// test-isolation contract: a package with tests must install, from main_test.go's
// TestMain, the fail-fast default for every seam family its tests can reach
// without an import cycle. The expected set is derived from the module's import
// graph, so this test needs no hand-maintained per-package exception list: a
// family is skipped only when importing it would close a cycle.
//
// The registry it reads is kept complete by TestSeamFamiliesMatchDeclaredInstallers
// and the wrapper it credits by TestInitFailFastMocksInstallsEveryFamily.
func TestEveryTestPackageInstallsItsAcyclicFailFastMocks(t *testing.T) {
	root := moduleRoot(t)
	modulePath := moduleImportPath(t, root)
	packages := modulePackages(t, root)
	if len(packages) == 0 {
		t.Fatal("no Go packages found; is the module root correct?")
	}
	transitive := transitiveModuleImports(t, root, modulePath, packages)

	checked := 0
	for _, pkg := range packages {
		if !pkg.hasTests {
			continue
		}
		checked++
		want := acyclicSeamFamilies(pkg.dir, transitive)
		got, err := installedSeamFamilies(root, pkg.dir, modulePath)
		if err != nil {
			t.Errorf("%s: %v", pkg.dir, err)
			continue
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: fail-fast mocks installed = %v, want %v", pkg.dir, got, want)
		}
	}
	if checked == 0 {
		t.Fatal("no test packages found; is the module root correct?")
	}
}

// TestInitFailFastMocksInstallsEveryFamily guards the credit the contract test
// gives to a package that calls the wrapper: the wrapper must really install
// every registered family, or every such package silently loses a fail-fast
// default.
func TestInitFailFastMocksInstallsEveryFamily(t *testing.T) {
	root := moduleRoot(t)
	modulePath := moduleImportPath(t, root)

	got := wrapperInstalledFamilies(t, root, fullSetInstallerPackage, modulePath)
	if want := everyFamilyName(); !slices.Equal(got, want) {
		t.Errorf("%s (%s) installs %v, want every seam family %v",
			fullSetInstaller, fullSetInstallerPackage, got, want)
	}
}

// TestSeamFamiliesMatchDeclaredInstallers keeps seamFamilies complete, so a new
// seam family cannot go unchecked behind a hand-maintained list: every declared
// installer must be registered.
func TestSeamFamiliesMatchDeclaredInstallers(t *testing.T) {
	root := moduleRoot(t)

	declared := declaredSeamInstallers(t, root, modulePackages(t, root))
	if registered := registeredInstallers(); !slices.Equal(declared, registered) {
		t.Errorf("declared fail-fast installers = %v, want the seamFamilies installers %v", declared, registered)
	}
}

type modulePackage struct {
	dir        string
	hasNonTest bool
	hasTests   bool
}

// moduleRoot walks up from the working directory to the directory holding go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the working directory")
		}
		dir = parent
	}
}

// moduleImportPath reads the module path declared in go.mod.
func moduleImportPath(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if trimmed, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(trimmed)
		}
	}
	t.Fatal("go.mod has no module directive")
	return ""
}

// modulePackages lists every directory under root holding at least one .go file,
// recording whether it holds non-test and test sources.
func modulePackages(t *testing.T, root string) []modulePackage {
	t.Helper()
	var packages []modulePackage
	err := filepath.WalkDir(root, func(p string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if p != root {
			if name := entry.Name(); name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return err
		}
		var pkg modulePackage
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		pkg.dir = filepath.ToSlash(rel)
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			if strings.HasSuffix(e.Name(), "_test.go") {
				pkg.hasTests = true
			} else {
				pkg.hasNonTest = true
			}
		}
		if pkg.hasNonTest || pkg.hasTests {
			packages = append(packages, pkg)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].dir < packages[j].dir })
	return packages
}

// transitiveModuleImports resolves, for every package, the set of module-local
// packages it imports directly or indirectly (non-test imports only).
func transitiveModuleImports(
	t *testing.T,
	root, modulePath string,
	packages []modulePackage,
) map[string]map[string]bool {
	t.Helper()
	known := map[string]bool{}
	for _, pkg := range packages {
		if pkg.hasNonTest {
			known[pkg.dir] = true
		}
	}
	direct := map[string]map[string]bool{}
	for dir := range known {
		direct[dir] = directModuleImports(t, filepath.Join(root, dir), modulePath, known)
	}
	transitive := map[string]map[string]bool{}
	var resolve func(dir string) map[string]bool
	resolve = func(dir string) map[string]bool {
		if cached, ok := transitive[dir]; ok {
			return cached
		}
		transitive[dir] = map[string]bool{} // cycle guard
		out := map[string]bool{}
		for dep := range direct[dir] {
			out[dep] = true
			for nested := range resolve(dep) {
				out[nested] = true
			}
		}
		transitive[dir] = out
		return out
	}
	for dir := range known {
		resolve(dir)
	}
	return transitive
}

// directModuleImports returns the module-local packages imported by the non-test
// files in absDir, honouring build constraints.
func directModuleImports(t *testing.T, absDir, modulePath string, known map[string]bool) map[string]bool {
	t.Helper()
	pkg, err := build.Default.ImportDir(absDir, build.ImportComment)
	if err != nil {
		if _, ok := errors.AsType[*build.NoGoError](err); !ok {
			t.Fatalf("import dir %s: %v", absDir, err)
		}
		return map[string]bool{}
	}
	out := map[string]bool{}
	for _, imported := range pkg.Imports {
		rel, ok := strings.CutPrefix(imported, modulePath+"/")
		if ok && known[rel] {
			out[rel] = true
		}
	}
	return out
}

// acyclicSeamFamilies returns the families whose fail-fast a package's tests can
// install: its own family, plus every family that does not itself import the
// package (importing such a family from the package's internal test would cycle).
func acyclicSeamFamilies(pkgDir string, transitive map[string]map[string]bool) []string {
	var families []string
	for _, family := range seamFamilies {
		if family.dir == pkgDir || !transitive[family.dir][pkgDir] {
			families = append(families, family.name)
		}
	}
	sort.Strings(families)
	return families
}

// installedSeamFamilies parses a package's main_test.go and reports the seam
// families its TestMain installs before the tests run.
func installedSeamFamilies(root, pkgDir, modulePath string) ([]string, error) {
	mainTestPath := filepath.Join(root, pkgDir, "main_test.go")
	file, err := parser.ParseFile(token.NewFileSet(), mainTestPath, nil, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("no main_test.go (add one with a TestMain that installs the fail-fast mocks)")
		}
		return nil, fmt.Errorf("parse main_test.go: %w", err)
	}
	testMain := findFunc(file, "TestMain")
	if testMain == nil {
		return nil, errors.New("main_test.go has no TestMain")
	}
	return installedFamiliesIn(file, testMain, modulePath), nil
}

// wrapperInstalledFamilies parses the wrapper declared in pkgDir and returns the
// seam families its body installs.
func wrapperInstalledFamilies(t *testing.T, root, pkgDir, modulePath string) []string {
	t.Helper()
	dir := filepath.Join(root, pkgDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", pkgDir, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", filepath.Join(pkgDir, name), err)
		}
		if wrapper := findFunc(file, fullSetInstaller); wrapper != nil {
			return installedFamiliesIn(file, wrapper, modulePath)
		}
	}
	t.Fatalf("%s declares no %s", pkgDir, fullSetInstaller)
	return nil
}

// installedFamiliesIn reports the seam families fn installs. Only direct call
// statements in fn's own body, before the statement that starts the tests, count:
// an installer reached from a helper, deferred, wrapped in a closure, or placed
// after the tests start installs nothing while the tests run. Installers must
// therefore be called directly in TestMain, as every main_test.go already does.
func installedFamiliesIn(file *ast.File, fn *ast.FuncDecl, modulePath string) []string {
	imports := importedPackagesByLocalName(file)
	testRunParam := firstParamName(fn)
	installed := map[string]bool{}
	for _, statement := range fn.Body.List {
		if call, ok := directCall(statement); ok {
			creditInstaller(call, imports, file.Name.Name, modulePath, installed)
		}
		if statementRunsTests(statement, testRunParam) {
			break
		}
	}
	return sortedFamilyNames(installed)
}

// creditInstaller records the seam family a call installs. The call's import
// path (or, for a same-package installer, the declaring package) must match a
// family, so an installer name reused elsewhere cannot credit the wrong one.
func creditInstaller(
	call *ast.CallExpr,
	imports map[string]string,
	packageName, modulePath string,
	installed map[string]bool,
) {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		base, ok := fun.X.(*ast.Ident)
		if !ok {
			return
		}
		importPath := imports[base.Name]
		if importPath == modulePath+"/"+fullSetInstallerPackage && fun.Sel.Name == fullSetInstaller {
			creditEveryFamily(installed)
			return
		}
		for _, family := range seamFamilies {
			if importPath == modulePath+"/"+family.dir && fun.Sel.Name == family.installer {
				installed[family.name] = true
			}
		}
	case *ast.Ident:
		if fun.Name == fullSetInstaller {
			// An unqualified call can only be the wrapper's own package calling it,
			// since the module declares the wrapper once.
			creditEveryFamily(installed)
			return
		}
		for _, family := range seamFamilies {
			if packageName == family.packageName && fun.Name == family.installer {
				installed[family.name] = true
			}
		}
	}
}

// directCall returns the call a statement makes directly, if it is a bare call
// statement. Calls behind defer or go, and calls inside function literals, are
// not direct.
func directCall(statement ast.Stmt) (*ast.CallExpr, bool) {
	expression, ok := statement.(*ast.ExprStmt)
	if !ok {
		return nil, false
	}
	call, ok := expression.X.(*ast.CallExpr)
	if !ok {
		return nil, false
	}
	return call, true
}

// statementRunsTests reports whether a statement starts the tests, nested or not
// (e.g. os.Exit(m.Run())). Every installer must already have run by then.
func statementRunsTests(statement ast.Stmt, testRunParam string) bool {
	runsTests := false
	ast.Inspect(statement, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		receiver, ok := selector.X.(*ast.Ident)
		if ok && receiver.Name == testRunParam && selector.Sel.Name == "Run" {
			runsTests = true
		}
		return true
	})
	return runsTests
}

// importedPackagesByLocalName maps each import's local name to its full path.
func importedPackagesByLocalName(file *ast.File) map[string]string {
	imports := map[string]string{}
	for _, spec := range file.Imports {
		importPath := strings.Trim(spec.Path.Value, `"`)
		local := path.Base(importPath)
		if spec.Name != nil {
			local = spec.Name.Name
		}
		imports[local] = importPath
	}
	return imports
}

// firstParamName returns the name of fn's first parameter, or "" when it has none.
func firstParamName(fn *ast.FuncDecl) string {
	if fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
		return ""
	}
	names := fn.Type.Params.List[0].Names
	if len(names) == 0 {
		return ""
	}
	return names[0].Name
}

// creditEveryFamily marks every registered family as installed.
func creditEveryFamily(installed map[string]bool) {
	for _, family := range seamFamilies {
		installed[family.name] = true
	}
}

// everyFamilyName returns the name of every registered family, sorted.
func everyFamilyName() []string {
	names := make([]string, 0, len(seamFamilies))
	for _, family := range seamFamilies {
		names = append(names, family.name)
	}
	sort.Strings(names)
	return names
}

// sortedFamilyNames returns the installed family names, sorted.
func sortedFamilyNames(installed map[string]bool) []string {
	families := make([]string, 0, len(installed))
	for name := range installed {
		families = append(families, name)
	}
	sort.Strings(families)
	return families
}

// findFunc returns the package-level function named name, if declared.
func findFunc(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Recv == nil && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}
