//deadcode:ignore reason="test support: referenced only by _test.go files"
package configpaths

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

type mockConfigPaths struct {
	baseDir string
	homeErr error
}

// InstallFailFastConfigPaths binds the sandbox config-path factory to its
// fail-fast default. Exported so packages dispatching through the factory —
// e.g., viperconfig, cmd/agents-sandbox — can list it in their own InitMocks call.
var InstallFailFastConfigPaths = func() { Get = FailFastConfigPaths }

// FailFastConfigPaths is the ConfigPaths installed by default under tests: any
// method call panics to signal a test reached real path resolution without opting in.
func FailFastConfigPaths() ConfigPaths { return &failFastConfigPaths{} }

func WithMockConfigPaths(t *testing.T) {
	t.Helper() // gomutants:disable STATEMENT_REMOVE reason="t.Helper only affects the stack trace a failing test reports, so removing it cannot change observable test behaviour"
	orig := Get
	tempDir := t.TempDir()
	// Point $HOME at the mock tree as well, so any stray os.UserHomeDir or
	// os.Getenv("HOME") in a test resolves inside the temp tree instead of the
	// developer's real home. This is what keeps host-home writes (e.g. the
	// agent's own ~/.config/opencode) from leaking into the real home. Do not
	// mark such tests t.Parallel: t.Setenv forbids it.
	t.Setenv("HOME", ensureMockConfigDirectory(filepath.Join(tempDir, "home")))
	Get = func() ConfigPaths { return &mockConfigPaths{baseDir: tempDir} }
	t.Cleanup(func() { Get = orig })
}

// WithMockConfigPathsHomeError installs a mock whose UserHomeDir fails, so tests
// can exercise host-home resolution failures without unsetting $HOME or falling
// back to the real resolver.
func WithMockConfigPathsHomeError(t *testing.T, err error) {
	t.Helper() // gomutants:disable STATEMENT_REMOVE reason="t.Helper only affects the stack trace a failing test reports, so removing it cannot change observable test behaviour"
	orig := Get
	tempDir := t.TempDir()
	// Redirect $HOME like WithMockConfigPaths so a resolver that bypasses the
	// seam still cannot read the developer's real home.
	t.Setenv("HOME", ensureMockConfigDirectory(filepath.Join(tempDir, "home")))
	Get = func() ConfigPaths { return &mockConfigPaths{baseDir: tempDir, homeErr: err} }
	t.Cleanup(func() { Get = orig })
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
	orig := Get
	Get = func() ConfigPaths { return &realConfigPaths{} }
	t.Cleanup(func() { Get = orig })
	t.Chdir(pinProjectCWD(t.TempDir()))
}

// pinProjectCWD returns dir when the resolver reads it as the relative project
// base, panicking otherwise — i.e. when dir sits inside a bare checkout and
// resolves to that checkout's bare root, so the pin could not isolate the base.
// It panics rather than calling t.Fatal so a test can reach the guard.
func pinProjectCWD(dir string) string {
	if base, _ := resolveProjectConfig(dir); base != projectConfigDir {
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

// MustDir returns dir, panicking on err. Test support: the agent config-dir
// accessors only fail for an invalid profile — a programming error a test
// cannot meaningfully recover from — so call sites stay terse.
func MustDir(dir string, err error) string {
	if err != nil {
		panic(err)
	}
	return dir
}

// MustDirs is MustDir for the candidate accessors.
func MustDirs(dirs []string, err error) []string {
	if err != nil {
		panic(err)
	}
	return dirs
}

func (m *mockConfigPaths) UserConfigDir() string {
	return ensureMockConfigDirectory(filepath.Join(m.baseDir, "userconfig"))
}

func (m *mockConfigPaths) UserSlugConfigDir(slug string) string {
	return ensureMockConfigDirectory(filepath.Join(m.UserConfigDir(), slug))
}

func (m *mockConfigPaths) UserCacheDir() string {
	return ensureMockConfigDirectory(filepath.Join(m.baseDir, "usercache"))
}

func (m *mockConfigPaths) UserStateDir() string {
	return ensureMockConfigDirectory(filepath.Join(m.baseDir, "userstate"))
}

func (m *mockConfigPaths) UserDataDir() string {
	return ensureMockConfigDirectory(filepath.Join(m.baseDir, "userdata"))
}

func (m *mockConfigPaths) UserAgentConfigDir(a agent.Agent) (string, error) {
	name, err := validConfigDirName(a.Name(), a.ConfigDirName())
	if err != nil {
		return "", err
	}
	return ensureMockConfigDirectory(filepath.Join(m.UserConfigDir(), name)), nil
}

func (m *mockConfigPaths) UserHomeDir() (string, error) {
	if m.homeErr != nil {
		return "", m.homeErr
	}
	return ensureMockConfigDirectory(filepath.Join(m.baseDir, "home")), nil
}

func (m *mockConfigPaths) UserAgentConfigDirCandidates(a agent.Agent) ([]string, error) {
	return agentConfigDirCandidates(filepath.Join(m.baseDir, "userconfig"), a)
}

func (m *mockConfigPaths) ProjectAgentConfigDirCandidates(a agent.Agent) ([]string, error) {
	return agentConfigDirCandidates(filepath.Join(m.baseDir, "projectconfig"), a)
}

func (m *mockConfigPaths) ProjectConfigDir() string {
	return ensureMockConfigDirectory(filepath.Join(m.baseDir, "projectconfig"))
}

func (m *mockConfigPaths) ProjectConfigDirAbs() string {
	return filepath.Join(m.baseDir, "projectconfig")
}

func (m *mockConfigPaths) UserEnvFile() string {
	return filepath.Join(m.UserConfigDir(), envFileName)
}

func (m *mockConfigPaths) UserEnvSecretFile() string {
	return filepath.Join(m.UserConfigDir(), envSecretFileName)
}

func (m *mockConfigPaths) ProjectAgentConfigDir(a agent.Agent) (string, error) {
	name, err := validConfigDirName(a.Name(), a.ConfigDirName())
	if err != nil {
		return "", err
	}
	return ensureMockConfigDirectory(filepath.Join(m.ProjectConfigDir(), name)), nil
}

func (m *mockConfigPaths) ProjectEnvFile() string {
	return filepath.Join(m.ProjectConfigDir(), envFileName)
}

func (m *mockConfigPaths) ProjectEnvSecretFile() string {
	return filepath.Join(m.ProjectConfigDir(), envSecretFileName)
}

func (m *mockConfigPaths) UserEnvSecretYAMLFile() string {
	return filepath.Join(m.UserConfigDir(), envSecretYAMLFileName)
}

func (m *mockConfigPaths) ProjectEnvSecretYAMLFile() string {
	return filepath.Join(m.ProjectConfigDir(), envSecretYAMLFileName)
}

func (m *mockConfigPaths) ProjectDockerfile() string {
	return filepath.Join(m.ProjectConfigDir(), dockerfileName)
}

func (m *mockConfigPaths) IgnoredWorktreeConfigDir() string {
	return ""
}

// failFastConfigPaths is the default ConfigPaths installed for tests. Any of
// its methods panicking signals that a test called production code without
// installing an isolated or explicit config-paths implementation.
type failFastConfigPaths struct{}

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
func (f *failFastConfigPaths) UserDataDir() string  { f.mustMock(); return "" }

func (f *failFastConfigPaths) UserAgentConfigDir(_ agent.Agent) (string, error) {
	f.mustMock()
	return "", nil
}

func (f *failFastConfigPaths) UserAgentConfigDirCandidates(_ agent.Agent) ([]string, error) {
	f.mustMock()
	return nil, nil
}

func (f *failFastConfigPaths) ProjectConfigDirAbs() string {
	f.mustMock()
	return ""
}

func (f *failFastConfigPaths) ProjectAgentConfigDirCandidates(_ agent.Agent) ([]string, error) {
	f.mustMock()
	return nil, nil
}

func (f *failFastConfigPaths) UserHomeDir() (string, error) { f.mustMock(); return "", nil }

func (f *failFastConfigPaths) UserEnvFile() string       { f.mustMock(); return "" }
func (f *failFastConfigPaths) UserEnvSecretFile() string { f.mustMock(); return "" }
func (f *failFastConfigPaths) ProjectConfigDir() string  { f.mustMock(); return "" }
func (f *failFastConfigPaths) ProjectAgentConfigDir(_ agent.Agent) (string, error) {
	f.mustMock()
	return "", nil
}

func (f *failFastConfigPaths) ProjectEnvFile() string       { f.mustMock(); return "" }
func (f *failFastConfigPaths) ProjectEnvSecretFile() string { f.mustMock(); return "" }
func (f *failFastConfigPaths) ProjectEnvSecretYAMLFile() string {
	f.mustMock()
	return ""
}
func (f *failFastConfigPaths) UserEnvSecretYAMLFile() string { f.mustMock(); return "" }
func (f *failFastConfigPaths) ProjectDockerfile() string     { f.mustMock(); return "" }

func (f *failFastConfigPaths) IgnoredWorktreeConfigDir() string {
	f.mustMock()
	return ""
}
