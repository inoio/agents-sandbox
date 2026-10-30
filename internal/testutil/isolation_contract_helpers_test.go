package testutil

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// writeFixtureFile writes content at root/rel, creating parent directories.
func writeFixtureFile(t *testing.T, root, rel, content string) {
	t.Helper()
	fullPath := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(rel), err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// TestModulePackages walks a fixture tree so the module root, which holds the
// packages beside go.mod, is inspected like any other directory.
func TestModulePackages(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "root.go", "package fixtures\n")
	writeFixtureFile(t, root, "root_test.go", "package fixtures\n")
	writeFixtureFile(t, root, "cmd/tool/main.go", "package main\n")
	writeFixtureFile(t, root, "internal/onlytest/only_test.go", "package onlytest\n")
	writeFixtureFile(t, root, "vendor/dependency/dependency.go", "package dependency\n")
	writeFixtureFile(t, root, "testdata/golden/golden.go", "package golden\n")
	writeFixtureFile(t, root, ".hidden/hidden.go", "package hidden\n")

	want := []modulePackage{
		{dir: ".", hasNonTest: true, hasTests: true},
		{dir: "cmd/tool", hasNonTest: true},
		{dir: "internal/onlytest", hasTests: true},
	}
	if got := modulePackages(t, root); !slices.Equal(got, want) {
		t.Errorf("modulePackages() = %v, want %v", got, want)
	}
}

func TestInstalledSeamFamilies(t *testing.T) {
	const modulePath = "example.com/mod"
	const pkgDir = "internal/example"

	writeMainTest := func(t *testing.T, source string) string {
		t.Helper()
		root := t.TempDir()
		if source != "" {
			writeFixtureFile(t, root, filepath.Join(pkgDir, "main_test.go"), source)
		}
		return root
	}

	tests := []struct {
		name    string
		source  string
		want    []string
		wantErr bool
	}{
		{
			name: "full set via the testutil wrapper",
			source: `package example

import (
	"testing"

	mocks "example.com/mod/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	mocks.InitFailFastMocks(m)
}
`,
			want: []string{"configpaths", "docker", "doctor", "msb", "notify", "opener"},
		},
		{
			name: "an unqualified wrapper call in the wrapper's own package",
			source: `package mocks

import "testing"

func TestMain(m *testing.M) {
	InitFailFastMocks(m)
}
`,
			want: []string{"configpaths", "docker", "doctor", "msb", "notify", "opener"},
		},
		{
			name: "direct installers with an aliased import",
			source: `package example

import (
	"testing"

	"example.com/mod/internal/configpaths"
	"example.com/mod/internal/sandbox/docker"
	sandboxmsb "example.com/mod/internal/sandbox/msb"
)

func TestMain(m *testing.M) {
	configpaths.InstallFailFastConfigPaths()
	docker.InstallFailFastGet()
	sandboxmsb.InstallFailFastGet()
	m.Run()
}
`,
			want: []string{"configpaths", "docker", "msb"},
		},
		{
			name: "same-package installer via an unqualified call",
			source: `package doctor

import (
	"testing"

	"example.com/mod/internal/configpaths"
	"example.com/mod/internal/sandbox/docker"
	"example.com/mod/internal/sandbox/msb"
)

func TestMain(m *testing.M) {
	configpaths.InstallFailFastConfigPaths()
	docker.InstallFailFastGet()
	InstallFailFast()
	msb.InstallFailFastGet()
	m.Run()
}
`,
			want: []string{"configpaths", "docker", "doctor", "msb"},
		},
		{
			name: "a comment mentioning an installer is not a call",
			source: `package example

import "testing"

func TestMain(m *testing.M) {
	// cannot call mocks.InitFailFastMocks(m) because of a cycle
	m.Run()
}
`,
			want: []string{},
		},
		{
			name: "an installer deferred to after the tests is not counted",
			source: `package example

import (
	"testing"

	"example.com/mod/internal/sandbox/docker"
)

func TestMain(m *testing.M) {
	defer docker.InstallFailFastGet()
	m.Run()
}
`,
			want: []string{},
		},
		{
			name: "an installer called after m.Run() is not counted",
			source: `package example

import (
	"testing"

	"example.com/mod/internal/sandbox/docker"
)

func TestMain(m *testing.M) {
	m.Run()
	docker.InstallFailFastGet()
}
`,
			want: []string{},
		},
		{
			name: "an installer inside a closure that never runs is not counted",
			source: `package example

import (
	"testing"

	"example.com/mod/internal/sandbox/docker"
)

func TestMain(m *testing.M) {
	install := func() { docker.InstallFailFastGet() }
	_ = install
	m.Run()
}
`,
			want: []string{},
		},
		{
			name: "an installer from another module is ignored",
			source: `package example

import (
	"testing"

	"example.com/other/thing"
)

func TestMain(m *testing.M) {
	thing.InstallFailFastGet()
	m.Run()
}
`,
			want: []string{},
		},
		{
			name:    "missing main_test.go",
			source:  "",
			wantErr: true,
		},
		{
			name: "main_test.go without a TestMain",
			source: `package example

import "testing"

func TestSomething(t *testing.T) {}
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeMainTest(t, tt.source)
			got, err := installedSeamFamilies(root, pkgDir, modulePath)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("installedSeamFamilies() error = nil, want error; got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("installedSeamFamilies() error = %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("installedSeamFamilies() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestWrapperInstalledFamilies pins what the wrapper check reads: the wrapper's
// own body, not the registry, so a dropped installer is visible.
func TestWrapperInstalledFamilies(t *testing.T) {
	const modulePath = "example.com/mod"
	completeWrapper := `package mocks

import (
	"testing"

	"example.com/mod/internal/configpaths"
	"example.com/mod/internal/notify"
	"example.com/mod/internal/sandbox/docker"
	"example.com/mod/internal/sandbox/doctor"
	"example.com/mod/internal/sandbox/msb"
)

func InitFailFastMocks(m *testing.M) {
	configpaths.InstallFailFastConfigPaths()
	docker.InstallFailFastGet()
	doctor.InstallFailFast()
	msb.InstallFailFastGet()
	notify.InstallFailFast()
	m.Run()
}
`
	wrapperMissingDocker := `package mocks

import (
	"testing"

	"example.com/mod/internal/configpaths"
	"example.com/mod/internal/notify"
	"example.com/mod/internal/sandbox/doctor"
	"example.com/mod/internal/sandbox/msb"
)

func InitFailFastMocks(m *testing.M) {
	configpaths.InstallFailFastConfigPaths()
	doctor.InstallFailFast()
	msb.InstallFailFastGet()
	notify.InstallFailFast()
	m.Run()
}
`

	tests := []struct {
		name    string
		wrapper string
		want    []string
	}{
		{
			name:    "a wrapper installing every family",
			wrapper: completeWrapper,
			want:    []string{"configpaths", "docker", "doctor", "msb", "notify"},
		},
		{
			name:    "a wrapper whose docker install was dropped",
			wrapper: wrapperMissingDocker,
			want:    []string{"configpaths", "doctor", "msb", "notify"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeFixtureFile(t, root, filepath.Join(fullSetInstallerPackage, "testutil.go"), tt.wrapper)

			got := wrapperInstalledFamilies(t, root, fullSetInstallerPackage, modulePath)
			if !slices.Equal(got, tt.want) {
				t.Errorf("wrapperInstalledFamilies() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDeclaredSeamInstallers pins the discovery rule: exported top-level
// identifiers named with the installer prefix, whether a function or a
// package-level var (configpaths installs through a var), in non-test sources.
func TestDeclaredSeamInstallers(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "internal/one/one.go", "package one\n")
	writeFixtureFile(t, root, "internal/one/testmock.go", "package one\n\nfunc InstallFailFastOne() {}\n")
	writeFixtureFile(t, root, "internal/two/testmock.go", "package two\n\nvar InstallFailFastTwo = func() {}\n")
	writeFixtureFile(t, root, "internal/three/three.go",
		"package three\n\nfunc installFailFastLowercase() {}\n\nfunc NotAnInstaller() {}\n")
	writeFixtureFile(
		t,
		root,
		"internal/four/four_test.go",
		"package four\n\nfunc InstallFailFastOnlyInATestFile() {}\n",
	)

	want := []registeredInstaller{
		{dir: "internal/one", packageName: "one", installer: "InstallFailFastOne"},
		{dir: "internal/two", packageName: "two", installer: "InstallFailFastTwo"},
	}
	if got := declaredSeamInstallers(t, root, modulePackages(t, root)); !slices.Equal(got, want) {
		t.Errorf("declaredSeamInstallers() = %v, want %v", got, want)
	}
}

func TestAcyclicSeamFamilies(t *testing.T) {
	// The real module graph's shape among the seam packages: docker imports
	// nothing module-local, configpaths imports only internal/git (which imports
	// no module-local package), msb imports configpaths, notify imports the
	// sandbox packages it reports on, and doctor imports all of docker,
	// configpaths, msb, and termio.
	transitive := map[string]map[string]bool{
		"internal/configpaths":    {},
		"internal/sandbox/docker": {},
		"internal/sandbox/msb": {
			"internal/configpaths": true,
			"internal/agent":       true,
			"internal/filewalk":    true,
		},
		"internal/notify": {
			"internal/agent":           true,
			"internal/atomicwrite":     true,
			"internal/configpaths":     true,
			"internal/filewalk":        true,
			"internal/sandbox/mounts":  true,
			"internal/sandbox/msb":     true,
			"internal/sandbox/naming":  true,
			"internal/sandbox/network": true,
			"internal/sandbox/state":   true,
			"internal/termio":          true,
		},
		"internal/sandbox/doctor": {
			"internal/sandbox/docker":  true,
			"internal/configpaths":     true,
			"internal/sandbox/msb":     true,
			"internal/termio":          true,
			"internal/sandbox/runtime": true,
		},
	}

	tests := []struct {
		name   string
		pkgDir string
		want   []string
	}{
		{
			"a seam imported by others drops those importers",
			"internal/configpaths",
			[]string{"configpaths", "docker", "opener"},
		},
		{
			"a seam imported by doctor drops only doctor",
			"internal/sandbox/docker",
			[]string{"configpaths", "docker", "msb", "notify", "opener"},
		},
		{"another seam imported by doctor", "internal/sandbox/msb", []string{"configpaths", "docker", "msb", "opener"}},
		{
			"the top of the graph may install every family",
			"internal/sandbox/doctor",
			[]string{"configpaths", "docker", "doctor", "msb", "notify", "opener"},
		},
		{
			"a package imported by doctor and notify drops both",
			"internal/termio",
			[]string{"configpaths", "docker", "msb", "opener"},
		},
		{
			"a package notify imports drops notify",
			"internal/sandbox/state",
			[]string{"configpaths", "docker", "doctor", "msb", "opener"},
		},
		{
			"an unrelated package may install every family",
			"internal/sandbox/vm",
			[]string{"configpaths", "docker", "doctor", "msb", "notify", "opener"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := acyclicSeamFamilies(tt.pkgDir, transitive)
			if !slices.Equal(got, tt.want) {
				t.Errorf("acyclicSeamFamilies(%q) = %v, want %v", tt.pkgDir, got, tt.want)
			}
		})
	}
}
