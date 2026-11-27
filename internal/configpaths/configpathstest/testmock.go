//deadcode:ignore reason="test support: referenced only by _test.go files"
package configpathstest

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

type mockConfigPaths struct {
	baseDir string
	homeErr error
	// msbHomeFromEnv makes UserDataDir serve the private microsandbox home from
	// MSB_HOME instead of the temp baseDir, so configpaths.MSBHome() names the
	// home the msb-tagged harness installed.
	msbHomeFromEnv bool
	// created records the directories this instance already made, so a repeated
	// accessor call skips the os.MkdirAll the config layer would otherwise pay
	// on every accessor call within one test.
	created map[string]struct{}
}

// InstallFailFastConfigPaths binds the sandbox config-path factory to its
// fail-fast default. Exported so packages dispatching through the factory —
// e.g., viperconfig, cmd/agents-sandbox — can list it in their own InitMocks call.
var InstallFailFastConfigPaths = func() { configpaths.Get = FailFastConfigPaths }

// FailFastConfigPaths is the ConfigPaths installed by default under tests: any
// method call panics to signal a test reached real path resolution without opting in.
func FailFastConfigPaths() configpaths.ConfigPaths { return &failFastConfigPaths{} }

func WithMockConfigPaths(t *testing.T) {
	t.Helper() // gomutants:disable STATEMENT_REMOVE reason="t.Helper only affects the stack trace a failing test reports, so removing it cannot change observable test behaviour"
	withMockConfigPaths(t, false)
}

// WithMockConfigPathsMSBHomeFromEnv establishes the temp-rooted mock, then opts
// it into serving the private microsandbox home from MSB_HOME the way
// UseMSBHomeFromEnv does for the fail-fast resolver. An msb-tagged test that
// creates a volume through the real client (which resolves its destination from
// MSB_HOME) and then resolves the volume's host path through the adapter (which
// resolves it from configpaths.MSBHome()) needs both to name one home; the
// state directories stay rooted in the temp tree, which is why the mock is still
// installed.
//
// The opt-in is scoped to the installed mock and reset by cleanup with it, so it
// does not leak across tests. Only the msb-tagged harness uses it; every other
// test keeps the temp baseDir.
func WithMockConfigPathsMSBHomeFromEnv(t *testing.T) {
	t.Helper()
	withMockConfigPaths(t, true)
}

func withMockConfigPaths(t *testing.T, msbHomeFromEnv bool) {
	t.Helper() // gomutants:disable STATEMENT_REMOVE reason="t.Helper only affects the stack trace a failing test reports, so removing it cannot change observable test behaviour"
	orig := configpaths.Get
	tempDir := t.TempDir()
	// Point $HOME at the mock tree as well, so any stray os.UserHomeDir or
	// os.Getenv("HOME") in a test resolves inside the temp tree instead of the
	// developer's real home. This is what keeps host-home writes (e.g. the
	// agent's own ~/.config/opencode) from leaking into the real home. Do not
	// mark such tests t.Parallel: t.Setenv forbids it.
	t.Setenv("HOME", ensureMockConfigDirectory(filepath.Join(tempDir, "home")))
	configpaths.Get = func() configpaths.ConfigPaths {
		return &mockConfigPaths{baseDir: tempDir, msbHomeFromEnv: msbHomeFromEnv}
	}
	t.Cleanup(func() { configpaths.Get = orig })
}

// msbHomeFromEnvMSBHome returns filepath.Dir(filepath.Clean(MSB_HOME)) — the
// user-data dir whose "msb" child is the private microsandbox home — or "" when
// MSB_HOME is unset.
func msbHomeFromEnvMSBHome() string {
	home := os.Getenv("MSB_HOME")
	if home == "" {
		return ""
	}
	return filepath.Dir(filepath.Clean(home))
}

// WithMockConfigPathsHomeError installs a mock whose UserHomeDir fails, so tests
// can exercise host-home resolution failures without unsetting $HOME or falling
// back to the real resolver.
func WithMockConfigPathsHomeError(t *testing.T, err error) {
	t.Helper() // gomutants:disable STATEMENT_REMOVE reason="t.Helper only affects the stack trace a failing test reports, so removing it cannot change observable test behaviour"
	orig := configpaths.Get
	tempDir := t.TempDir()
	// Redirect $HOME like WithMockConfigPaths so a resolver that bypasses the
	// seam still cannot read the developer's real home.
	t.Setenv("HOME", ensureMockConfigDirectory(filepath.Join(tempDir, "home")))
	configpaths.Get = func() configpaths.ConfigPaths { return &mockConfigPaths{baseDir: tempDir, homeErr: err} }
	t.Cleanup(func() { configpaths.Get = orig })
}

// WithRealConfigPaths opts a test into the real XDG path resolution instead of
// the fail-fast default installed by TestMain. Only use it when a test
// deliberately exercises the real resolver and isolates its directories via
// t.Setenv, as config_paths_test.go does.
//
// It also pins the CWD to a fresh temp directory. The real resolver derives the
// project base from the CWD's checkout layout, so without the pin a test would
// inherit whatever layout the suite runs in — a bare checkout's worktree
// resolves the base to that checkout's bare root, not the relative constant the
// tests expect. A test that needs a specific layout chdirs into its own fixture
// after this call, as the bare-checkout tests do.
func WithRealConfigPaths(t *testing.T) {
	t.Helper() // gomutants:disable STATEMENT_REMOVE reason="t.Helper only affects the stack trace a failing test reports, so removing it cannot change observable test behaviour"
	orig := configpaths.Get
	configpaths.Get = configpaths.NewRealConfigPaths
	t.Cleanup(func() { configpaths.Get = orig })
	t.Chdir(pinProjectCWD(t.TempDir()))
}

// WithBareCheckoutLocator installs locator for the test and restores the
// previous one on cleanup. Tests that exercise bare-checkout resolution through
// the real resolver pass git.BareCheckoutRoots, as the composition root does.
func WithBareCheckoutLocator(t *testing.T, locator configpaths.BareCheckoutLocator) {
	t.Helper() // gomutants:disable STATEMENT_REMOVE reason="t.Helper only affects the stack trace a failing test reports, so removing it cannot change observable test behaviour"
	previous := configpaths.GetBareCheckoutLocator()
	configpaths.UseBareCheckoutLocator(locator)
	t.Cleanup(func() { configpaths.UseBareCheckoutLocator(previous) })
}

// pinProjectCWD returns dir when the resolver reads it as the relative project
// base, panicking otherwise — i.e. when dir sits inside a bare checkout and
// resolves to that checkout's bare root, so the pin could not isolate the base.
// It panics rather than calling t.Fatal so a test can reach the guard.
func pinProjectCWD(dir string) string {
	if base := configpaths.ProjectConfigBase(dir); base != configpaths.ProjectConfigDir {
		panic(
			fmt.Sprintf("temp dir %q resolves to the bare-root base %q; set TMPDIR outside a bare checkout", dir, base),
		)
	}
	return dir
}

func ensureMockConfigDirectory(path string) string {
	err := os.MkdirAll(path, 0o750)
	if err != nil {
		fmt.Fprintln(os.Stdout, err)
	}
	return path
}

// ensureDir creates path once per mock instance. It is ensureMockConfigDirectory
// plus a cache: the accessor methods are called many times per test, and the
// redundant MkdirAll on an already-created directory is pure cost. The mock tree
// is create-only — a path exists after its first call and is not re-created if a
// test removes it — which no current test relies on.
func (m *mockConfigPaths) ensureDir(path string) string {
	if m.created == nil {
		m.created = map[string]struct{}{}
	}
	if _, ok := m.created[path]; ok {
		return path
	}
	ensureMockConfigDirectory(path)
	m.created[path] = struct{}{}
	return path
}

// CreateDir creates dir like a mock directory and returns it. Test support:
// tests write agent config into the agent's config dir under the mock tree.
func CreateDir(dir string) string {
	return ensureMockConfigDirectory(dir)
}

func (m *mockConfigPaths) UserConfigDir() string {
	return m.ensureDir(filepath.Join(m.baseDir, "userconfig"))
}

func (m *mockConfigPaths) UserSlugConfigDir(slug string) string {
	return m.ensureDir(filepath.Join(m.UserConfigDir(), slug))
}

func (m *mockConfigPaths) UserCacheDir() string {
	return m.ensureDir(filepath.Join(m.baseDir, "usercache"))
}

func (m *mockConfigPaths) UserStateDir() string {
	return m.ensureDir(filepath.Join(m.baseDir, "userstate"))
}

func (m *mockConfigPaths) UserDataDir() string {
	if m.msbHomeFromEnv {
		if home := msbHomeFromEnvMSBHome(); home != "" {
			return home
		}
	}
	return m.ensureDir(filepath.Join(m.baseDir, "userdata"))
}

func (m *mockConfigPaths) UserHomeDir() (string, error) {
	if m.homeErr != nil {
		return "", m.homeErr
	}
	return m.ensureDir(filepath.Join(m.baseDir, "home")), nil
}

func (m *mockConfigPaths) ProjectConfigDir() string {
	return m.ensureDir(filepath.Join(m.baseDir, "projectconfig"))
}

func (m *mockConfigPaths) ProjectConfigDirAbs() string {
	return filepath.Join(m.baseDir, "projectconfig")
}

func (m *mockConfigPaths) ProjectDockerfile() string {
	return filepath.Join(m.ProjectConfigDir(), configpaths.DockerFileName)
}

func (m *mockConfigPaths) IgnoredWorktreeConfigDir() string {
	return ""
}

// failFastConfigPaths is the default ConfigPaths installed for tests. Any of
// its methods panicking signals that a test called production code without
// installing an isolated or explicit config-paths implementation.
type failFastConfigPaths struct{}

// msbHomeFromEnv opts the fail-fast resolver into serving the private
// microsandbox home from MSB_HOME instead of panicking. The msb-tagged suites
// pin their runtime home through MSB_HOME (msbtest/runtime.InstallIsolatedRuntimeHome),
// while the launcher's sandbox/volume adapter resolves its explicit local
// destination from configpaths.MSBHome(); this keeps the two on one home. It is
// read live, so a child process that re-points MSB_HOME follows its parent.
var msbHomeFromEnv bool

// UseMSBHomeFromEnv makes the fail-fast resolver serve the private
// microsandbox home from MSB_HOME. Only the msb-tagged test harness calls it.
func UseMSBHomeFromEnv() { msbHomeFromEnv = true }

func (f *failFastConfigPaths) mustMock() {
	panic(
		"GetConfigPaths reached real path resolution; install WithMockConfigPaths(t) or WithRealConfigPaths(t) in the test",
	)
}

func (f *failFastConfigPaths) UserConfigDir() string { f.mustMock(); return "" }
func (f *failFastConfigPaths) UserSlugConfigDir(_ string) string {
	f.mustMock()
	return ""
}
func (f *failFastConfigPaths) UserCacheDir() string { f.mustMock(); return "" }
func (f *failFastConfigPaths) UserStateDir() string { f.mustMock(); return "" }

// UserDataDir serves the msb harness's home (from MSB_HOME, whose last segment
// is "msb") when it opted in, and panics otherwise.
func (f *failFastConfigPaths) UserDataDir() string {
	if msbHomeFromEnv {
		if home := msbHomeFromEnvMSBHome(); home != "" {
			return home
		}
	}
	f.mustMock()
	return ""
}

func (f *failFastConfigPaths) ProjectConfigDirAbs() string {
	f.mustMock()
	return ""
}

func (f *failFastConfigPaths) UserHomeDir() (string, error) { f.mustMock(); return "", nil }

func (f *failFastConfigPaths) ProjectConfigDir() string  { f.mustMock(); return "" }
func (f *failFastConfigPaths) ProjectDockerfile() string { f.mustMock(); return "" }

func (f *failFastConfigPaths) IgnoredWorktreeConfigDir() string {
	f.mustMock()
	return ""
}
