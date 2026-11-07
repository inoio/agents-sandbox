package configpaths

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFailFastConfigPathsPanics verifies that every method of the default
// fail-fast implementation panics, so any test that reaches real path
// resolution without opting in fails loudly rather than silently resolving.
func TestFailFastConfigPathsPanics(t *testing.T) {
	f := &failFastConfigPaths{}

	cases := []struct {
		name string
		fn   func()
	}{
		{"UserConfigDir", func() { f.UserConfigDir() }},
		{"UserSlugConfigDir", func() { f.UserSlugConfigDir("widget-bbb") }},
		{"UserCacheDir", func() { f.UserCacheDir() }},
		{"UserStateDir", func() { f.UserStateDir() }},
		{"UserDataDir", func() { f.UserDataDir() }},
		{"UserHomeDir", func() { _, _ = f.UserHomeDir() }},
		{"ProjectConfigDir", func() { f.ProjectConfigDir() }},
		{"ProjectConfigDirAbs", func() { _ = f.ProjectConfigDirAbs() }},
		{"ProjectDockerfile", func() { f.ProjectDockerfile() }},
		{"IgnoredWorktreeConfigDir", func() { f.IgnoredWorktreeConfigDir() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertPanics(t, tc.fn)
		})
	}
}

// TestEnsureMockConfigDirectoryFailurePath triggers the error branch of
// ensureMockConfigDirectory by asking it to create a directory whose parent
// path component is a regular file, which MkdirAll cannot satisfy. The helper
// still returns the requested path, and the error must be reported on stdout
// so a mock tree that could not be created is visible rather than silently
// absent.
func TestEnsureMockConfigDirectoryFailurePath(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "sub")

	readEnd, writeEnd, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatal(pipeErr)
	}
	stdout := os.Stdout
	os.Stdout = writeEnd
	t.Cleanup(func() {
		// Restore and close even if the helper panics or a check fatals, so the
		// redirected global can never leak into a later test.
		os.Stdout = stdout
		_ = readEnd.Close()
		_ = writeEnd.Close()
	})

	got := ensureMockConfigDirectory(path)
	os.Stdout = stdout
	if err := writeEnd.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(readEnd)
	if err != nil {
		t.Fatal(err)
	}

	if got != path {
		t.Errorf("ensureMockConfigDirectory(%q) = %q, want %q", path, got, path)
	}
	if !strings.Contains(string(output), blocker) {
		t.Errorf("failure output = %q, want it to report the MkdirAll error for %q", output, blocker)
	}
}

// TestEnsureMockConfigDirectoryMode pins that the helper requests 0o750 (owner
// rwx, group rx, no world access) so a mock tree never becomes wider than the
// real resolver's directories. MkdirAll masks the mode with the process umask,
// so assert no bits outside 0o750 and no world access rather than the exact
// value: a stricter umask (e.g. 0077) is still correct.
func TestEnsureMockConfigDirectoryMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "created", "nested")
	if got := ensureMockConfigDirectory(path); got != path {
		t.Fatalf("ensureMockConfigDirectory(%q) = %q, want %q", path, got, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	if got := info.Mode().Perm(); got&^0o750 != 0 || got&0o007 != 0 {
		t.Errorf("created directory mode = %o, want no wider than 0o750", got)
	}
}

// TestFailFastConfigPathsFactory pins the default factory's concrete value:
// the panic a test hits on reaching real path resolution must come from the
// fail-fast implementation, not from a nil ConfigPaths dereference.
func TestFailFastConfigPathsFactory(t *testing.T) {
	f, ok := FailFastConfigPaths().(*failFastConfigPaths)
	if !ok {
		t.Fatalf("FailFastConfigPaths() = %T, want *failFastConfigPaths", FailFastConfigPaths())
	}
	assertPanics(t, func() { f.UserConfigDir() })
}

func TestGetConfigPathsFailFastDefault(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("GetConfigPaths() should panic by default in tests when no mock is installed, but did not")
		}
	}()
	Get().UserConfigDir()
}

// TestMockConfigPaths exercises the WithMockConfigPaths helper that other
// packages' tests rely on, verifying each method resolves to a path rooted in
// the isolated temp base directory rather than the real XDG locations.
func TestMockConfigPaths(t *testing.T) {
	WithMockConfigPaths(t)
	cfgPaths := Get()
	m := Get().(*mockConfigPaths)
	base := m.baseDir

	if got := cfgPaths.UserConfigDir(); got != filepath.Join(base, "userconfig") {
		t.Errorf("UserConfigDir() = %q, want %q", got, filepath.Join(base, "userconfig"))
	}
	if got := cfgPaths.UserSlugConfigDir("widget-bbb"); got != filepath.Join(base, "userconfig", "widget-bbb") {
		t.Errorf(
			"UserSlugConfigDir() = %q, want %q",
			got,
			filepath.Join(base, "userconfig", "widget-bbb"),
		)
	}
	if got := cfgPaths.UserCacheDir(); got != filepath.Join(base, "usercache") {
		t.Errorf("UserCacheDir() = %q, want %q", got, filepath.Join(base, "usercache"))
	}
	if got := cfgPaths.UserStateDir(); got != filepath.Join(base, "userstate") {
		t.Errorf("UserStateDir() = %q, want %q", got, filepath.Join(base, "userstate"))
	}
	if got := cfgPaths.UserDataDir(); got != filepath.Join(base, "userdata") {
		t.Errorf("UserDataDir() = %q, want %q", got, filepath.Join(base, "userdata"))
	}
	home, err := cfgPaths.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}
	if home != filepath.Join(base, "home") {
		t.Errorf("UserHomeDir() = %q, want %q", home, filepath.Join(base, "home"))
	}
	//testenv:allow asserts the HOME the mock path resolver exported
	if got := os.Getenv("HOME"); got != filepath.Join(base, "home") {
		t.Errorf("$HOME = %q, want %q (WithMockConfigPaths must redirect HOME)", got, filepath.Join(base, "home"))
	}
	if got := cfgPaths.ProjectConfigDirAbs(); got != filepath.Join(base, "projectconfig") {
		t.Errorf("ProjectConfigDirAbs() = %q, want %q", got, filepath.Join(base, "projectconfig"))
	}
	if got := cfgPaths.ProjectConfigDir(); got != filepath.Join(base, "projectconfig") {
		t.Errorf("ProjectConfigDir() = %q, want %q", got, filepath.Join(base, "projectconfig"))
	}
	if got := cfgPaths.ProjectDockerfile(); got != filepath.Join(base, "projectconfig", dockerfileName) {
		t.Errorf("ProjectDockerfile() = %q, want %q", got, filepath.Join(base, "projectconfig", dockerfileName))
	}
	if got := cfgPaths.IgnoredWorktreeConfigDir(); got != "" {
		t.Errorf("IgnoredWorktreeConfigDir() = %q, want empty; the mock cannot express a bare base", got)
	}
}

// TestWithMockConfigPathsHomeError verifies the error variant returns the
// injected home-resolution error while keeping the other paths mocked.
func TestWithMockConfigPathsHomeError(t *testing.T) {
	wantErr := errors.New("home unavailable")
	WithMockConfigPathsHomeError(t, wantErr)
	if _, err := Get().UserHomeDir(); !errors.Is(err, wantErr) {
		t.Fatalf("UserHomeDir() error = %v, want %v", err, wantErr)
	}
	if got := Get().UserConfigDir(); got == "" {
		t.Error("UserConfigDir() = empty, want a mocked path")
	}
}

// TestWithMockConfigPathsHomeErrorRedirectsHome pins the error variant's
// $HOME redirect: a resolver that bypasses the seam must still be confined to
// the mock tree rather than reading the developer's real home.
func TestWithMockConfigPathsHomeErrorRedirectsHome(t *testing.T) {
	WithMockConfigPathsHomeError(t, errors.New("home unavailable"))
	base := Get().(*mockConfigPaths).baseDir
	//testenv:allow asserts the HOME the mock path resolver exported
	if got, want := os.Getenv("HOME"), filepath.Join(base, "home"); got != want {
		t.Errorf("$HOME = %q, want %q (WithMockConfigPathsHomeError must redirect HOME)", got, want)
	}
}

// TestWithMockConfigPathsHomeErrorRestoresGet pins the helper's cleanup: once
// the installing test returns, Get must be back to the factory the helper
// replaced, so the mock does not leak into whichever test runs next.
func TestWithMockConfigPathsHomeErrorRestoresGet(t *testing.T) {
	t.Run("installed", func(t *testing.T) {
		WithMockConfigPathsHomeError(t, errors.New("home unavailable"))
		if _, ok := Get().(*mockConfigPaths); !ok {
			t.Fatal("Get did not install a mockConfigPaths")
		}
	})
	if _, ok := Get().(*mockConfigPaths); ok {
		t.Error(
			"Get still returns a mockConfigPaths after WithMockConfigPathsHomeError's cleanup ran; " +
				"the replaced factory was not restored",
		)
	}
}

// TestMockAccessorsAreStableAcrossCalls pins the contract the create-once
// optimization must preserve: a repeated accessor call returns the same path
// and that path still exists. It is the guard for the per-mock directory cache,
// which skips the redundant os.MkdirAll on the second call.
func TestMockAccessorsAreStableAcrossCalls(t *testing.T) {
	WithMockConfigPaths(t)
	paths := Get()

	for name, accessor := range map[string]func() string{
		"UserConfigDir": paths.UserConfigDir,
		"UserCacheDir":  paths.UserCacheDir,
		"UserStateDir":  paths.UserStateDir,
		"UserDataDir":   paths.UserDataDir,
	} {
		first := accessor()
		if second := accessor(); second != first {
			t.Errorf("%s returned %q then %q, want a stable path", name, first, second)
		}
		if info, err := os.Stat(first); err != nil || !info.IsDir() {
			t.Errorf("%s path %q does not exist after repeated calls (stat err = %v)", name, first, err)
		}
	}
}

func TestCreateDirCreatesAndReturnsTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "userconfig", "opencode")

	if got := CreateDir(dir); got != dir {
		t.Errorf("CreateDir() = %q, want %q", got, dir)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("CreateDir did not create %q (stat err = %v)", dir, err)
	}
}
