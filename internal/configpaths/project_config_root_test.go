package configpaths

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/testutil"
)

// pinHostDirs points the host home and XDG directories at temp trees, so a test
// and any git subprocess it spawns never read or write the developer's real
// config. It does not install the resolver; callers that need the real one add
// WithRealConfigPaths (or isolateProjectConfig does both).
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

// writeBareCheckout builds a bare checkout whose worktrees live inside the bare
// root: root/.git is a bare clone and root/main is a worktree of it.
func writeBareCheckout(t *testing.T) string {
	t.Helper()
	origin := testutil.InitRepo(t)
	root := t.TempDir()
	testutil.RunGit(t, root, "clone", "--bare", origin, ".git")
	testutil.RunGit(t, filepath.Join(root, ".git"), "worktree", "add", filepath.Join(root, "main"))
	return root
}

// physicalDir resolves a path's symlinks, the same way the resolver derives the
// bare base, so expectations hold even when the temp base contains a symlink.
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
	wantBase := filepath.Join(physicalDir(t, root), ".agents-sandbox")
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

	wantBase := filepath.Join(physicalDir(t, root), ".agents-sandbox")
	if _, err := os.Stat(wantBase); !os.IsNotExist(err) {
		t.Errorf("resolving the base created %s (stat err = %v)", wantBase, err)
	}
}

// TestProjectConfigBaseBareViaSymlink pins $PWD to the symlinked path so
// os.Getwd returns the logical path; the resolver must still report the
// physical bare root as its base.
func TestProjectConfigBaseBareViaSymlink(t *testing.T) {
	isolateProjectConfig(t)
	root := writeBareCheckout(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatalf("symlink %s -> %s: %v", link, root, err)
	}
	target := filepath.Join(link, "main")
	t.Setenv("PWD", target)
	t.Chdir(target)

	logical, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if logical != target {
		t.Fatalf(
			"os.Getwd() = %q, want the symlinked path %q: the PWD pin is not exercising the logical path",
			logical,
			target,
		)
	}

	wantBase := filepath.Join(physicalDir(t, root), ".agents-sandbox")
	if got := Get().ProjectConfigDir(); got != wantBase {
		t.Errorf("ProjectConfigDir() = %q, want the physical base %q", got, wantBase)
	}
}

func TestProjectConfigBaseNonBareSubdirectoryUnchanged(t *testing.T) {
	isolateProjectConfig(t)
	repo := testutil.InitRepo(t)
	sub := filepath.Join(repo, "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", sub, err)
	}
	t.Chdir(sub)

	assertRelativeProjectBase(t)
}

func TestProjectConfigBaseNonRepoUnchanged(t *testing.T) {
	isolateProjectConfig(t)
	t.Chdir(t.TempDir())

	assertRelativeProjectBase(t)
}

// TestResolveProjectConfigUnresolvableCwdFallsBack covers the EvalSymlinks
// fallback: a CWD that cannot be resolved is used as-is, and since it is outside
// any repository resolution degrades to the relative base with nothing ignored.
func TestResolveProjectConfigUnresolvableCwdFallsBack(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	base, ignored := resolveProjectConfig(missing)

	if base != projectConfigDir {
		t.Errorf("base = %q, want %q", base, projectConfigDir)
	}
	if ignored != "" {
		t.Errorf("ignored = %q, want empty", ignored)
	}
}

// TestResolveProjectConfigUnresolvableCwdInsideBareWorktree pins the #354
// consequence inside a bare checkout: identity resolution now refuses an
// unresolvable cwd, so the upward walk no longer reaches the enclosing bare
// root and the read degrades to the relative base like any other unresolvable
// cwd. (The earlier raw-path walk is gone with the slug-affecting fallback.)
func TestResolveProjectConfigUnresolvableCwdInsideBareWorktree(t *testing.T) {
	isolateProjectConfig(t)
	root := physicalDir(t, writeBareCheckout(t))
	missing := filepath.Join(root, "main", "gone")

	base, ignored := resolveProjectConfig(missing)

	if base != projectConfigDir {
		t.Errorf("base = %q, want the relative base %q", base, projectConfigDir)
	}
	if ignored != "" {
		t.Errorf("ignored = %q, want empty", ignored)
	}
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

	want := filepath.Join(physicalDir(t, mainDir), ".agents-sandbox")
	if got := Get().IgnoredWorktreeConfigDir(); got != want {
		t.Errorf("IgnoredWorktreeConfigDir() = %q, want %q", got, want)
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

	want := filepath.Join(physicalDir(t, mainDir), ".agents-sandbox")
	if got := Get().IgnoredWorktreeConfigDir(); got != want {
		t.Errorf("IgnoredWorktreeConfigDir() from a worktree subdirectory = %q, want %q", got, want)
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

func TestIgnoredWorktreeConfigDirEmptyForNonBare(t *testing.T) {
	isolateProjectConfig(t)
	repo := testutil.InitRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, ".agents-sandbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	if got := Get().IgnoredWorktreeConfigDir(); got != "" {
		t.Errorf("IgnoredWorktreeConfigDir() = %q, want empty for a non-bare checkout", got)
	}
}

// TestIgnoredWorktreeConfigDirEmptyForOutsideWorktree covers a worktree that
// lives outside its bare shared git dir: nothing is ignored, and the base stays
// the relative constant.
func TestIgnoredWorktreeConfigDirEmptyForOutsideWorktree(t *testing.T) {
	isolateProjectConfig(t)
	origin := testutil.InitRepo(t)
	root := t.TempDir()
	testutil.RunGit(t, root, "clone", "--bare", origin, ".git")
	worktree := filepath.Join(t.TempDir(), "wt")
	testutil.RunGit(t, filepath.Join(root, ".git"), "worktree", "add", worktree)
	if err := os.MkdirAll(filepath.Join(worktree, ".agents-sandbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(worktree)

	assertRelativeProjectBase(t)

	if got := Get().IgnoredWorktreeConfigDir(); got != "" {
		t.Errorf("IgnoredWorktreeConfigDir() = %q, want empty for a worktree outside its checkout", got)
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
