package configpaths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pinHostDirs points the host home and XDG directories at temp trees, so a test
// never reads or writes the developer's real config. It does not install the
// resolver; callers that need the real one add WithRealConfigPaths (or
// isolateProjectConfig does both).
func pinHostDirs(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
}

// isolateProjectConfig pins the host home and XDG directories inside a temp
// tree and installs the real resolver, so bare-checkout resolution never reads
// the developer's real config or creates a directory in it.
func isolateProjectConfig(t *testing.T) {
	t.Helper()
	pinHostDirs(t)
	WithRealConfigPaths(t)
}

// writeBareCheckout lays out root/main as the worktree of a bare checkout and
// installs a locator that reports this layout the way git.BareCheckoutRoots
// reports a real one: the bare root for root itself, and root/main as the
// worktree root for any directory inside it. Other directories are outside.
func writeBareCheckout(t *testing.T) string {
	t.Helper()
	root := physicalDir(t, t.TempDir())
	worktree := filepath.Join(root, "main")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", worktree, err)
	}
	WithBareCheckoutLocator(t, func(cwd string) (string, string, bool) {
		switch {
		case cwd == root:
			return root, root, true
		case isWithin(cwd, worktree):
			return root, worktree, true
		default:
			return "", "", false
		}
	})
	return root
}

func isWithin(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// physicalDir resolves a path's symlinks, so the fake layout matches the path
// os.Getwd reports after a chdir even when the temp base contains a symlink.
func physicalDir(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}
	return resolved
}

func assertBareProjectBase(t *testing.T, root string) {
	t.Helper()
	wantBase := filepath.Join(root, ".agents-sandbox")
	if got := Get().ProjectConfigDir(); got != wantBase {
		t.Errorf("ProjectConfigDir() = %q, want %q", got, wantBase)
	}
	if got := Get().ProjectConfigDirAbs(); got != wantBase {
		t.Errorf("ProjectConfigDirAbs() = %q, want %q", got, wantBase)
	}
	if got := Get().ProjectDockerfile(); got != filepath.Join(wantBase, DockerFileName) {
		t.Errorf("ProjectDockerfile() = %q, want %q", got, filepath.Join(wantBase, DockerFileName))
	}
	if got := Get().ProjectEnvFile(); got != filepath.Join(wantBase, EnvFileName) {
		t.Errorf("ProjectEnvFile() = %q, want %q", got, filepath.Join(wantBase, EnvFileName))
	}
	if got := Get().ProjectEnvSecretFile(); got != filepath.Join(wantBase, EnvSecretFileName) {
		t.Errorf("ProjectEnvSecretFile() = %q, want %q", got, filepath.Join(wantBase, EnvSecretFileName))
	}
	if got := Get().ProjectEnvSecretYAMLFile(); got != filepath.Join(wantBase, envSecretYAMLFileName) {
		t.Errorf("ProjectEnvSecretYAMLFile() = %q, want %q", got, filepath.Join(wantBase, envSecretYAMLFileName))
	}
}

func assertRelativeProjectBase(t *testing.T) {
	t.Helper()
	if got := Get().ProjectConfigDir(); got != ".agents-sandbox" {
		t.Errorf("ProjectConfigDir() = %q, want the relative .agents-sandbox", got)
	}
	if got := Get().ProjectDockerfile(); got != filepath.Join(".agents-sandbox", DockerFileName) {
		t.Errorf("ProjectDockerfile() = %q, want %q", got, filepath.Join(".agents-sandbox", DockerFileName))
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if got := Get().ProjectConfigDirAbs(); got != filepath.Join(cwd, ".agents-sandbox") {
		t.Errorf("ProjectConfigDirAbs() = %q, want %q", got, filepath.Join(cwd, ".agents-sandbox"))
	}
}

func TestProjectConfigBaseBareWorktree(t *testing.T) {
	isolateProjectConfig(t)
	root := writeBareCheckout(t)
	t.Chdir(filepath.Join(root, "main"))

	assertBareProjectBase(t, root)
}

func TestProjectConfigBaseBareWorktreeSubdirectory(t *testing.T) {
	isolateProjectConfig(t)
	root := writeBareCheckout(t)
	sub := filepath.Join(root, "main", "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", sub, err)
	}
	t.Chdir(sub)

	assertBareProjectBase(t, root)
}

func TestProjectConfigBaseBareRoot(t *testing.T) {
	isolateProjectConfig(t)
	root := writeBareCheckout(t)
	t.Chdir(root)

	assertBareProjectBase(t, root)
}

// TestProjectConfigBaseBareWithoutConfigDir covers a bare checkout where no
// .agents-sandbox exists anywhere: the base is still the bare root and nothing
// is created or required.
func TestProjectConfigBaseBareWithoutConfigDir(t *testing.T) {
	isolateProjectConfig(t)
	root := writeBareCheckout(t)
	t.Chdir(filepath.Join(root, "main"))

	assertBareProjectBase(t, root)

	wantBase := filepath.Join(root, ".agents-sandbox")
	if _, err := os.Stat(wantBase); !os.IsNotExist(err) {
		t.Errorf("resolving the base created %s (stat err = %v)", wantBase, err)
	}
}

func TestProjectConfigBaseOutsideBareCheckoutIsRelative(t *testing.T) {
	isolateProjectConfig(t)
	writeBareCheckout(t)
	t.Chdir(t.TempDir())

	assertRelativeProjectBase(t)
}

// TestProjectConfigBaseWithoutLocatorIsRelative pins the default: until the
// composition root installs a locator, no directory resolves to a bare base.
func TestProjectConfigBaseWithoutLocatorIsRelative(t *testing.T) {
	isolateProjectConfig(t)
	WithBareCheckoutLocator(t, notInBareCheckout)

	assertRelativeProjectBase(t)
}

// TestProjectConfigLocatesTheBareCheckoutOncePerCwd pins the cache: path
// helpers are called many times per launch, and each must not repeat the
// filesystem walk that locating the checkout costs.
func TestProjectConfigLocatesTheBareCheckoutOncePerCwd(t *testing.T) {
	isolateProjectConfig(t)
	first := physicalDir(t, t.TempDir())
	second := physicalDir(t, t.TempDir())
	locatedDirectories := map[string]int{}
	WithBareCheckoutLocator(t, func(cwd string) (string, string, bool) {
		locatedDirectories[cwd]++
		return "", "", false
	})

	for _, dir := range []string{first, second, first} {
		t.Chdir(dir)
		Get().ProjectConfigDir()
		Get().ProjectConfigDirAbs()
		Get().ProjectDockerfile()
		Get().ProjectEnvFile()
		Get().IgnoredWorktreeConfigDir()
	}

	for _, dir := range []string{first, second} {
		if got := locatedDirectories[dir]; got != 1 {
			t.Errorf("locator called %d times for %q, want once", got, dir)
		}
	}
}

// TestUseBareCheckoutLocatorDropsCachedResults guards against a stale layout:
// installing another locator must not keep answers the previous one gave.
func TestUseBareCheckoutLocatorDropsCachedResults(t *testing.T) {
	isolateProjectConfig(t)
	root := writeBareCheckout(t)
	t.Chdir(filepath.Join(root, "main"))
	assertBareProjectBase(t, root)

	WithBareCheckoutLocator(t, notInBareCheckout)

	assertRelativeProjectBase(t)
}

func TestIgnoredWorktreeConfigDir(t *testing.T) {
	isolateProjectConfig(t)
	root := writeBareCheckout(t)
	mainDir := filepath.Join(root, "main")
	worktreeConfig := filepath.Join(mainDir, ".agents-sandbox")
	if err := os.MkdirAll(worktreeConfig, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", worktreeConfig, err)
	}
	t.Chdir(mainDir)

	assertBareProjectBase(t, root)

	if got := Get().IgnoredWorktreeConfigDir(); got != worktreeConfig {
		t.Errorf("IgnoredWorktreeConfigDir() = %q, want %q", got, worktreeConfig)
	}
}

// TestIgnoredWorktreeConfigDirFromWorktreeSubdirectory covers a launch from a
// directory inside the worktree: the ignored directory is still the worktree
// root's .agents-sandbox, not one derived from the CWD.
func TestIgnoredWorktreeConfigDirFromWorktreeSubdirectory(t *testing.T) {
	isolateProjectConfig(t)
	root := writeBareCheckout(t)
	mainDir := filepath.Join(root, "main")
	if err := os.MkdirAll(filepath.Join(mainDir, ".agents-sandbox"), 0o755); err != nil {
		t.Fatalf("mkdir worktree config: %v", err)
	}
	sub := filepath.Join(mainDir, "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", sub, err)
	}
	t.Chdir(sub)

	assertBareProjectBase(t, root)

	want := filepath.Join(mainDir, ".agents-sandbox")
	if got := Get().IgnoredWorktreeConfigDir(); got != want {
		t.Errorf("IgnoredWorktreeConfigDir() from a worktree subdirectory = %q, want %q", got, want)
	}
}

// TestIgnoredWorktreeConfigDirEmptyWithoutWorktreeCopy covers a bare worktree
// with no .agents-sandbox of its own: there is nothing to warn about.
func TestIgnoredWorktreeConfigDirEmptyWithoutWorktreeCopy(t *testing.T) {
	isolateProjectConfig(t)
	root := writeBareCheckout(t)
	t.Chdir(filepath.Join(root, "main"))

	if got := Get().IgnoredWorktreeConfigDir(); got != "" {
		t.Errorf("IgnoredWorktreeConfigDir() = %q, want empty without a worktree copy", got)
	}
}

// TestIgnoredWorktreeConfigDirEmptyAtBareRoot covers launching from the bare
// root itself with <bare>/.agents-sandbox present: that directory is the base,
// so nothing is ignored and the warning must not name it.
func TestIgnoredWorktreeConfigDirEmptyAtBareRoot(t *testing.T) {
	isolateProjectConfig(t)
	root := writeBareCheckout(t)
	if err := os.MkdirAll(filepath.Join(root, ".agents-sandbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	assertBareProjectBase(t, root)

	if got := Get().IgnoredWorktreeConfigDir(); got != "" {
		t.Errorf("IgnoredWorktreeConfigDir() = %q, want empty when the CWD is the bare root", got)
	}
}

func TestIgnoredWorktreeConfigDirEmptyOutsideBareCheckout(t *testing.T) {
	isolateProjectConfig(t)
	writeBareCheckout(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".agents-sandbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	if got := Get().IgnoredWorktreeConfigDir(); got != "" {
		t.Errorf("IgnoredWorktreeConfigDir() = %q, want empty outside a bare checkout", got)
	}
}

// TestIgnoredWorktreeConfigDirEmptyWhenCwdUnreadable covers the Getwd fallback:
// with no readable CWD nothing is reported as ignored, mirroring
// ProjectConfigDirAbs's fallback to the relative base.
func TestIgnoredWorktreeConfigDirEmptyWhenCwdUnreadable(t *testing.T) {
	isolateProjectConfig(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	if got := Get().IgnoredWorktreeConfigDir(); got != "" {
		t.Errorf("IgnoredWorktreeConfigDir() with an unreadable cwd = %q, want empty", got)
	}
}
