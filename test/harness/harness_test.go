package harness

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/hostlock"
	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// TestSeedHomeFiles pins that seeds are written under the isolated HOME
// (<root>/home/...) with parents created, so the launcher provisions them into
// the VM.
func TestSeedHomeFiles(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		driver.HermesConfigPath: "model: {}\n",
		".config/x":             "y",
	}
	if err := seedHomeFiles(root, files); err != nil {
		t.Fatalf("seedHomeFiles: %v", err)
	}
	for rel, want := range files {
		got, err := os.ReadFile(filepath.Join(root, homeDirName, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
}

// TestSeedLauncherConfig pins that the isolated launcher config enables
// host-config provisioning and carries the home: manifest. The opt-in is what
// makes the agent's ProvisionRules copy a harness's SeedHomeFiles; the manifest
// seeds a target outside those rules. The written config is parsed back with the
// launcher's own home-manifest reader, so a shape the launcher would reject
// fails here.
func TestSeedLauncherConfig(t *testing.T) {
	homeBase := t.TempDir()
	files := map[string]string{
		driver.ClaudeCodeConfigPath: driver.ClaudeCodeSmokeConfig,
		".nested/seed":              "content",
	}
	if err := seedLauncherConfig(homeBase, files); err != nil {
		t.Fatalf("seedLauncherConfig: %v", err)
	}
	for target, want := range files {
		got, err := os.ReadFile(filepath.Join(homeBase, seedDirName, target))
		if err != nil {
			t.Fatalf("read seed %s: %v", target, err)
		}
		if string(got) != want {
			t.Errorf("seed %s = %q, want %q", target, got, want)
		}
	}
	configPath := filepath.Join(homeBase, configDirName, launcherConfigDirName, launcherConfigFileName)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read launcher config: %v", err)
	}
	assertProvisioningEnabled(t, data)
	manifest, ok, err := homeconfig.ParseHomeConfig(configPath, data)
	if err != nil {
		t.Fatalf("parse launcher config: %v", err)
	}
	if !ok {
		t.Fatalf("launcher config has no home: key:\n%s", data)
	}
	for target := range files {
		entry, ok := manifest[target]
		if !ok {
			t.Errorf("home manifest missing target %s", target)
			continue
		}
		if want := filepath.Join(homeBase, seedDirName, target); entry.Source != want {
			t.Errorf("manifest source for %s = %q, want %q", target, entry.Source, want)
		}
	}
}

// TestSeedLauncherConfigWithoutManifest pins that a harness with no
// SeedHomeManifest still gets a launcher config that enables host-config
// provisioning (its SeedHomeFiles depend on it) and declares no home: manifest.
func TestSeedLauncherConfigWithoutManifest(t *testing.T) {
	homeBase := t.TempDir()
	if err := seedLauncherConfig(homeBase, nil); err != nil {
		t.Fatalf("seedLauncherConfig(nil): %v", err)
	}
	configPath := filepath.Join(homeBase, configDirName, launcherConfigDirName, launcherConfigFileName)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read launcher config: %v", err)
	}
	assertProvisioningEnabled(t, data)
	if _, ok, err := homeconfig.ParseHomeConfig(configPath, data); err != nil {
		t.Fatalf("parse launcher config: %v", err)
	} else if ok {
		t.Errorf("launcher config has a home: key for an empty manifest:\n%s", data)
	}
}

// assertProvisioningEnabled fails unless data is a launcher config that turns
// host-config provisioning on, the opt-in a SeedHomeFiles seed needs.
func assertProvisioningEnabled(t *testing.T, data []byte) {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal launcher config: %v", err)
	}
	if got, ok := raw["provision-host-config"]; !ok || got != true {
		t.Errorf("launcher config provision-host-config = %v (present %v), want true:\n%s", got, ok, data)
	}
}

// TestSeedAgentSnippets pins that snippets land in the agent's config dir
// under the isolated launcher config dir (XDG_CONFIG_HOME/agents-sandbox),
// where the launcher's snippet merge reads them, and that none writes nothing.
func TestSeedAgentSnippets(t *testing.T) {
	homeBase := t.TempDir()
	if err := seedAgentSnippets(homeBase, "kimi-code", map[string]string{"config-smoke.toml": "a = 1\n"}); err != nil {
		t.Fatalf("seedAgentSnippets: %v", err)
	}
	path := filepath.Join(homeBase, configDirName, launcherConfigDirName, "kimi-code", "config-smoke.toml")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snippet: %v", err)
	}
	if string(got) != "a = 1\n" {
		t.Errorf("snippet = %q, want %q", got, "a = 1\n")
	}

	emptyBase := t.TempDir()
	if err := seedAgentSnippets(emptyBase, "kimi-code", nil); err != nil {
		t.Fatalf("seedAgentSnippets(nil): %v", err)
	}
	if _, err := os.Stat(filepath.Join(emptyBase, configDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("seedAgentSnippets(nil) created a config dir: stat err = %v", err)
	}
}

// TestPrepareHomeCreatesTheChildEnvTree pins that prepareHome materialises every
// directory isolatedEnv points the child at. A missing TMPDIR is fatal: the
// launcher and the microsandbox runtime create temporary files through
// os.TempDir() while setting up a sandbox, so an uncreated TMPDIR fails the run
// with "create prefill sandbox: io error: no such file or directory".
func TestPrepareHomeCreatesTheChildEnvTree(t *testing.T) {
	homeBase := filepath.Join(t.TempDir(), "run")
	if err := prepareHome(homeBase); err != nil {
		t.Fatalf("prepareHome: %v", err)
	}
	for _, dir := range []string{homeDirName, tmpDirName, configDirName, stateDirName, cacheDirName} {
		info, err := os.Stat(filepath.Join(homeBase, dir))
		if err != nil {
			t.Errorf("stat %s: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
	}
}

// TestPrepareHomeReportsFailures pins that a home base that cannot be created
// names the directory and preserves the filesystem cause, so the smoke test
// reports why preparation failed rather than a bare message.
func TestPrepareHomeReportsFailures(t *testing.T) {
	// A file where the home base must go makes the first MkdirAll fail.
	homeBase := filepath.Join(t.TempDir(), "run")
	if err := os.WriteFile(homeBase, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := prepareHome(homeBase)
	if err == nil {
		t.Fatal("prepareHome = nil, want an error when the home base cannot be created")
	}
	if !strings.Contains(err.Error(), "create "+homeDirName) {
		t.Errorf("prepareHome error %q, want it to name the directory it could not create", err)
	}
	if !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("prepareHome error %q, want it to wrap the filesystem cause ENOTDIR", err)
	}
}

// TestSeedHomeFilesReportsFailures pins both failure surfaces: a parent that
// cannot be created, and a target that cannot be written. Each error names the
// seed it was writing and preserves the filesystem cause, so the smoke test
// reports why provisioning failed rather than a bare message.
func TestSeedHomeFilesReportsFailures(t *testing.T) {
	t.Run("parent cannot be created", func(t *testing.T) {
		root := t.TempDir()
		// A file where the seed's parent directory must go makes MkdirAll fail.
		if err := os.WriteFile(filepath.Join(root, homeDirName), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		err := seedHomeFiles(root, map[string]string{driver.HermesConfigPath: "y"})
		if err == nil {
			t.Fatal("seedHomeFiles = nil, want an error when a parent cannot be created")
		}
		if !strings.Contains(err.Error(), "create parent for .hermes") {
			t.Errorf("seedHomeFiles error %q, want it to name the parent it could not create", err)
		}
		if !errors.Is(err, syscall.ENOTDIR) {
			t.Errorf("seedHomeFiles error %q, want it to wrap the filesystem cause ENOTDIR", err)
		}
	})
	t.Run("target cannot be written", func(t *testing.T) {
		root := t.TempDir()
		// A directory at the target path makes the file write fail.
		if err := os.MkdirAll(filepath.Join(root, homeDirName, ".hermes", "config.yaml"), 0o755); err != nil {
			t.Fatal(err)
		}
		err := seedHomeFiles(root, map[string]string{driver.HermesConfigPath: "y"})
		if err == nil {
			t.Fatal("seedHomeFiles = nil, want an error when the target cannot be written")
		}
		if !strings.Contains(err.Error(), "write .hermes/config.yaml") {
			t.Errorf("seedHomeFiles error %q, want it to name the file it could not write", err)
		}
		if !errors.Is(err, syscall.EISDIR) {
			t.Errorf("seedHomeFiles error %q, want it to wrap the filesystem cause EISDIR", err)
		}
	})
}

// TestSeedHomeFilesUsesPrivateModes pins the modes the seed writes with through
// the filesystem seams, so the assertion does not depend on the process umask:
// the seeded parent directory is group-traversable but not world-readable, and
// the file is owner-only.
func TestSeedHomeFilesUsesPrivateModes(t *testing.T) {
	root := t.TempDir()

	var dirMode, fileMode os.FileMode
	originalMkdirAll, originalWriteFile := mkdirAll, writeFile
	t.Cleanup(func() { mkdirAll, writeFile = originalMkdirAll, originalWriteFile })
	mkdirAll = func(path string, perm os.FileMode) error {
		dirMode = perm
		return os.MkdirAll(path, perm)
	}
	writeFile = func(name string, data []byte, perm os.FileMode) error {
		fileMode = perm
		return os.WriteFile(name, data, perm)
	}

	if err := seedHomeFiles(root, map[string]string{driver.HermesConfigPath: "model: {}\n"}); err != nil {
		t.Fatalf("seedHomeFiles: %v", err)
	}
	if dirMode != 0o750 {
		t.Errorf("seed parent mode = %#o, want 0o750", dirMode)
	}
	if fileMode != 0o600 {
		t.Errorf("seed file mode = %#o, want 0o600", fileMode)
	}
}

func TestIsolatedEnvAllowlist(t *testing.T) {
	inherited := []string{
		"PATH=/usr/bin:/bin",
		"TERM=dumb",
		"LANG=en_US.UTF-8",
		"LC_ALL=C.UTF-8",
		"HTTP_PROXY=http://proxy.example:3128",
		"HOME=/real/home",
		"XDG_DATA_HOME=/real/data",
		"AGENTS_SANDBOX_NETWORK_PROFILE=public",
		"OPENCODE_SANDBOX_NOTIFY=on",
		"MSB_HOME=/real/msb",
		"SECRET_TOKEN=leak",
	}
	env := isolatedEnv("/root", "/root", inherited)
	got := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	want := map[string]string{
		"PATH":            "/usr/bin:/bin",
		"TERM":            "xterm-256color",
		"LANG":            "en_US.UTF-8",
		"LC_ALL":          "C.UTF-8",
		"HTTP_PROXY":      "http://proxy.example:3128",
		"HOME":            "/root/home",
		"XDG_CONFIG_HOME": "/root/config",
		"XDG_DATA_HOME":   "/root/data",
		"XDG_STATE_HOME":  "/root/state",
		"XDG_CACHE_HOME":  "/root/cache",
		"TMPDIR":          "/root/tmp",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	for _, k := range []string{
		"AGENTS_SANDBOX_NETWORK_PROFILE",
		"OPENCODE_SANDBOX_NOTIFY",
		"MSB_HOME",
		"SECRET_TOKEN",
	} {
		if _, ok := got[k]; ok {
			t.Errorf("%s must not be inherited (got %q)", k, got[k])
		}
	}
}

// TestSmokeRootStaysShortEnoughForSandboxSockets pins why the suite anchors its
// shared temp root at a short, fixed base instead of the host's TMPDIR:
// microsandbox derives its sandbox control-socket path from the private home
// under XDG_DATA_HOME (<root>/data/agents-sandbox/msb), and a long TMPDIR —
// macOS's default is ~49 bytes — pushes the derived path past a Unix socket's
// sun_path limit, failing every harness before its TUI with "sandbox runtime
// socket path is too long".
func TestSmokeRootStaysShortEnoughForSandboxSockets(t *testing.T) {
	// os.MkdirTemp appends nine random characters after the pattern.
	const mkdirTempSuffix = "123456789"
	root := filepath.Join(smokeRootBaseDefault, smokeRootPrefix+mkdirTempSuffix)
	if !rootFitsSandboxSocket(root) {
		t.Errorf(
			"the default suite root %q derives a %d-byte sandbox control-socket path, at or above the %d-byte sun_path limit; keep smokeRootBaseDefault, any %s override, and smokeRootPrefix short",
			root,
			len(msbHomeUnderRoot(root))+sandboxSocketPathSuffix,
			sunPathLimit,
			smokeRootBaseEnv,
		)
	}
}

// shortTempDir creates a unique directory under smokeRootBaseDefault. Its path
// is short enough for a reuse root's derived sandbox control socket, and being
// unique per invocation keeps a test from sharing (and deleting) a fixed /tmp
// path another concurrent run uses.
func shortTempDir(t *testing.T) string {
	t.Helper()
	// mkdtemp (the resolveSuiteRoot seam) with the short smokeRootBaseDefault,
	// not t.TempDir: t.TempDir's test-named path under TMPDIR is too long for the
	// socket budget.
	dir, err := mkdtemp(smokeRootBaseDefault, smokeRootPrefix+"t-")
	if err != nil {
		t.Fatalf("create short temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestResolveSuiteRootReusesEnv pins the reuse escape hatch: a set
// smokeRootReuseEnv reuses (and creates) that directory, while an unset one
// creates a fresh temp root, and a reused path whose derived socket would exceed
// the budget is rejected.
func TestResolveSuiteRootReusesEnv(t *testing.T) {
	shortRoot := shortTempDir(t)

	t.Setenv(smokeRootReuseEnv, shortRoot)
	root, reused, release, err := resolveSuiteRoot()
	if err != nil {
		t.Fatalf("resolveSuiteRoot: %v", err)
	}
	if !reused || root != shortRoot {
		t.Errorf("resolveSuiteRoot = (%q, %v), want (%q, true)", root, reused, shortRoot)
	}
	if release == nil {
		t.Error("resolveSuiteRoot returned no lock release for a reused root")
	} else {
		t.Cleanup(release)
	}

	t.Setenv(smokeRootReuseEnv, filepath.Join(t.TempDir(), strings.Repeat("x", sunPathLimit)))
	if _, reused, _, err := resolveSuiteRoot(); err == nil {
		t.Error("resolveSuiteRoot = nil error, want a too-long reuse path rejected")
	} else if reused {
		t.Error("resolveSuiteRoot reported a rejected reuse path as reused")
	}

	t.Setenv(smokeRootReuseEnv, "")
	root, reused, release, err = resolveSuiteRoot()
	if err != nil {
		t.Fatalf("resolveSuiteRoot: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if release != nil {
		t.Error("resolveSuiteRoot returned a lock release for a fresh root, want nil")
	}
	if reused || root == "" {
		t.Errorf("resolveSuiteRoot = (%q, %v), want a fresh, non-reused root", root, reused)
	}
}

// TestSmokeRootBaseOverride pins that AGENTS_SANDBOX_MSB_ROOT_BASE moves a fresh
// suite root, so a host whose /tmp is a RAM-backed tmpfs can create roots on a
// disk-backed base. The mkdtemp seam keeps the returned root short enough for the
// socket budget regardless of the (necessarily short) base's own length.
func TestSmokeRootBaseOverride(t *testing.T) {
	originalMkdtemp := mkdtemp
	t.Cleanup(func() { mkdtemp = originalMkdtemp })
	var gotBase, gotPrefix string
	mkdtemp = func(base, prefix string) (string, error) {
		gotBase, gotPrefix = base, prefix
		return filepath.Join(base, "root"), nil
	}

	base := shortTempDir(t)
	t.Setenv(smokeRootReuseEnv, "")
	t.Setenv(smokeRootBaseEnv, base)

	root, reused, release, err := resolveSuiteRoot()
	if err != nil {
		t.Fatalf("resolveSuiteRoot: %v", err)
	}
	if release != nil {
		t.Error("resolveSuiteRoot returned a lock release for a fresh root, want nil")
	}
	if reused {
		t.Error("resolveSuiteRoot reused a root with the reuse env unset")
	}
	if gotBase != base {
		t.Errorf("resolveSuiteRoot created under base %q, want the override %q", gotBase, base)
	}
	if gotPrefix != smokeRootPrefix {
		t.Errorf("resolveSuiteRoot created with prefix %q, want %q", gotPrefix, smokeRootPrefix)
	}
	if want := filepath.Join(base, "root"); root != want {
		t.Errorf("resolveSuiteRoot root = %q, want %q", root, want)
	}
}

// TestSmokeRootBaseOverrideRejectsLongBase pins that a base which cannot fit the
// socket budget is reported at resolution, not left to fail a harness later.
func TestSmokeRootBaseOverrideRejectsLongBase(t *testing.T) {
	longBase := filepath.Join(t.TempDir(), strings.Repeat("x", sunPathLimit))
	if err := os.MkdirAll(longBase, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv(smokeRootReuseEnv, "")
	t.Setenv(smokeRootBaseEnv, longBase)

	root, reused, release, err := resolveSuiteRoot()
	if err == nil {
		t.Fatal("resolveSuiteRoot = nil error, want a too-long base rejected")
	}
	if !strings.Contains(err.Error(), smokeRootBaseEnv) {
		t.Errorf("resolveSuiteRoot error %q, want it to name %s", err, smokeRootBaseEnv)
	}
	if root != "" || reused || release != nil {
		t.Errorf("resolveSuiteRoot = (%q, %v, release %v), want zero values on failure", root, reused, release != nil)
	}
}

// TestResolveSuiteRootMakesTheReuseRootAbsolute pins that a relative reuse root
// is resolved before use: the image prebuild runs the launcher with the project
// directory as its working directory, so a relative smokeBinary path would look
// for the binary beneath the project dir instead of where it was built.
func TestResolveSuiteRootMakesTheReuseRootAbsolute(t *testing.T) {
	workDir := shortTempDir(t)
	t.Chdir(workDir)

	t.Setenv(smokeRootReuseEnv, "r")
	root, reused, release, err := resolveSuiteRoot()
	if err != nil {
		t.Fatalf("resolveSuiteRoot: %v", err)
	}
	t.Cleanup(release)
	if !reused {
		t.Fatal("resolveSuiteRoot did not reuse the configured root")
	}
	if !filepath.IsAbs(root) {
		t.Errorf("resolveSuiteRoot root = %q, want an absolute path", root)
	}
	if want := filepath.Join(workDir, "r"); root != want {
		t.Errorf("resolveSuiteRoot root = %q, want %q", root, want)
	}
}

// TestResolveSuiteRootLocksTheReuseRoot pins that a reused root is held under an
// exclusive lock for the run's lifetime: two runs sharing a root serialize there
// instead of one run's sandbox sweep destroying the other's live VMs.
func TestResolveSuiteRootLocksTheReuseRoot(t *testing.T) {
	shortRoot := shortTempDir(t)
	t.Setenv(smokeRootReuseEnv, shortRoot)

	_, reused, release, err := resolveSuiteRoot()
	if err != nil {
		t.Fatalf("resolveSuiteRoot: %v", err)
	}
	if !reused {
		t.Fatal("resolveSuiteRoot did not reuse the configured root")
	}
	if release == nil {
		t.Fatal("resolveSuiteRoot returned no lock release for a reused root")
	}

	if !lockContended(t, reuseLockPath(shortRoot)) {
		t.Error("the reuse lock is free while the run holds it, want it held")
	}
	release()
	if lockContended(t, reuseLockPath(shortRoot)) {
		t.Error("the reuse lock is still held after release")
	}
}

// lockContended reports whether another open file description holds the lock at
// path.
func lockContended(t *testing.T, path string) bool {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open lock %s: %v", path, err)
	}
	defer f.Close()
	err = hostlock.ExclusiveNonBlocking(f)
	if err == nil {
		return false
	}
	if !hostlock.IsContention(err) {
		t.Fatalf("flock %s: %v", path, err)
	}
	return true
}

// TestResolveSuiteRootReportsTheAbsFailure pins that a reuse root that cannot be
// resolved against the working directory (a deleted CWD) is reported, not
// mistaken for a successful reuse.
func TestResolveSuiteRootReportsTheAbsFailure(t *testing.T) {
	originalAbsPath := absPath
	t.Cleanup(func() { absPath = originalAbsPath })
	absPath = func(string) (string, error) { return "", syscall.ENOENT }

	t.Setenv(smokeRootReuseEnv, "r")
	_, reused, release, err := resolveSuiteRoot()
	if err == nil {
		t.Fatal("resolveSuiteRoot = nil error, want the resolution failure reported")
	}
	if !errors.Is(err, syscall.ENOENT) {
		t.Errorf("resolveSuiteRoot error %q, want it to wrap the filesystem cause ENOENT", err)
	}
	if !strings.Contains(err.Error(), "resolve reuse root") {
		t.Errorf("resolveSuiteRoot error %q, want it to name the reuse-root resolution", err)
	}
	if reused || release != nil {
		t.Errorf("resolveSuiteRoot = (reused %v, release %v), want neither on failure", reused, release != nil)
	}
}

// TestResolveSuiteRootReportsTheLockFailure pins that a reuse root whose lock
// cannot be taken is reported, not returned as a locked reuse root with a nil
// release.
func TestResolveSuiteRootReportsTheLockFailure(t *testing.T) {
	shortRoot := shortTempDir(t)
	// A directory where the lock file belongs makes the lock open fail.
	lockPath := reuseLockPath(shortRoot)
	if err := os.MkdirAll(lockPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(lockPath) })

	t.Setenv(smokeRootReuseEnv, shortRoot)
	_, reused, release, err := resolveSuiteRoot()
	if err == nil {
		t.Fatal("resolveSuiteRoot = nil error, want the lock failure reported")
	}
	if !errors.Is(err, syscall.EISDIR) {
		t.Errorf("resolveSuiteRoot error %q, want it to wrap the filesystem cause EISDIR", err)
	}
	if !strings.Contains(err.Error(), "lock reuse root") {
		t.Errorf("resolveSuiteRoot error %q, want it to name the reuse-root lock", err)
	}
	if reused || release != nil {
		t.Errorf("resolveSuiteRoot = (reused %v, release %v), want neither on failure", reused, release != nil)
	}
}

// TestResolveSuiteRootReportsCreateFailures pins both failure surfaces: a reuse
// root that cannot be created (a file where its parent must be), and a temp root
// that cannot be created. Each error preserves the filesystem cause and names
// the surface, so a run reports why it failed.
func TestResolveSuiteRootReportsCreateFailures(t *testing.T) {
	t.Run("reuse root cannot be created", func(t *testing.T) {
		// A file where the reuse root's parent must be makes MkdirAll fail; the
		// path stays short so it passes the sun_path budget check first.
		blocker := filepath.Join(shortTempDir(t), "f")
		if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}

		t.Setenv(smokeRootReuseEnv, filepath.Join(blocker, "child"))
		_, reused, release, err := resolveSuiteRoot()
		if err == nil {
			t.Fatal("resolveSuiteRoot = nil error, want the create failure reported")
		}
		if !errors.Is(err, syscall.ENOTDIR) {
			t.Errorf("resolveSuiteRoot error %q, want it to wrap the filesystem cause ENOTDIR", err)
		}
		if !strings.Contains(err.Error(), "create reuse root") {
			t.Errorf("resolveSuiteRoot error %q, want it to name the reuse-root creation", err)
		}
		if reused || release != nil {
			t.Errorf("resolveSuiteRoot = (reused %v, release %v), want neither on failure", reused, release != nil)
		}
	})

	t.Run("temp root cannot be created", func(t *testing.T) {
		originalMkdtemp := mkdtemp
		t.Cleanup(func() { mkdtemp = originalMkdtemp })
		mkdtemp = func(string, string) (string, error) { return "", syscall.ENOSPC }

		t.Setenv(smokeRootReuseEnv, "")
		_, reused, release, err := resolveSuiteRoot()
		if err == nil {
			t.Fatal("resolveSuiteRoot = nil error, want the temp-root failure reported")
		}
		if !errors.Is(err, syscall.ENOSPC) {
			t.Errorf("resolveSuiteRoot error %q, want it to wrap the filesystem cause ENOSPC", err)
		}
		if reused || release != nil {
			t.Errorf("resolveSuiteRoot = (reused %v, release %v), want neither on failure", reused, release != nil)
		}
	})
}

// TestRootFitsSandboxSocketBoundary pins the strict boundary of the sun_path
// budget predicate: a root whose derived control-socket path is one byte short
// of the limit fits, and one whose path reaches the limit does not.
func TestRootFitsSandboxSocketBoundary(t *testing.T) {
	// msbHomeUnderRoot appends a fixed suffix to root, and the derived socket
	// path adds sandboxSocketPathSuffix; `len(appended)` isolates the suffix
	// length for the boundary computation.
	appended := len(msbHomeUnderRoot("x")) - len("x")
	fitsLen := sunPathLimit - sandboxSocketPathSuffix - appended - 1
	rootOfLength := func(n int) string { return "/" + strings.Repeat("a", n-1) }

	if !rootFitsSandboxSocket(rootOfLength(fitsLen)) {
		t.Errorf("rootFitsSandboxSocket rejected a %d-byte root, want the tightest fitting root accepted", fitsLen)
	}
	if rootFitsSandboxSocket(rootOfLength(fitsLen + 1)) {
		t.Errorf("rootFitsSandboxSocket accepted a %d-byte root, want one reaching the limit rejected", fitsLen+1)
	}
}

// TestIsolatedEnvSharesDataKeepsHome pins the per-harness split: two harnesses
// sharing one root keep separate HOME/config/state/cache dirs (so a seeded config
// cannot clobber another harness) while sharing XDG_DATA_HOME (so the
// microsandbox image cache stays common).
func TestIsolatedEnvSharesDataKeepsHome(t *testing.T) {
	root := "/suite"
	first := envMap(isolatedEnv(root, harnessHomeBase(root, "opencode"), nil))
	second := envMap(isolatedEnv(root, harnessHomeBase(root, "pi"), nil))

	if first["HOME"] == second["HOME"] {
		t.Errorf("HOME = %q for both harnesses, want a distinct per-harness home", first["HOME"])
	}
	if first["XDG_STATE_HOME"] == second["XDG_STATE_HOME"] {
		t.Errorf(
			"XDG_STATE_HOME = %q for both harnesses, want a distinct per-harness state dir",
			first["XDG_STATE_HOME"],
		)
	}
	wantData := filepath.Join(root, dataDirName)
	for name, env := range map[string]map[string]string{"opencode": first, "pi": second} {
		if env["XDG_DATA_HOME"] != wantData {
			t.Errorf("%s XDG_DATA_HOME = %q, want the shared %q", name, env["XDG_DATA_HOME"], wantData)
		}
	}
}

// envMap flattens a KEY=VALUE slice into a map.
func envMap(env []string) map[string]string {
	got := make(map[string]string, len(env))
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	return got
}

// TestIsolatedEnvKeepsDockerEndpoint pins that the docker endpoint variables
// survive: the child needs the same daemon the preflight reaches, and they
// carry no agents-sandbox config or state.
func TestIsolatedEnvKeepsDockerEndpoint(t *testing.T) {
	inherited := []string{
		"DOCKER_HOST=unix:///run/user/1000/docker.sock",
		"DOCKER_CONTEXT=colima",
		"DOCKER_CONFIG=/real/.docker",
	}
	got := map[string]string{}
	for _, kv := range isolatedEnv("/root", "/root", inherited) {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	for k, want := range map[string]string{
		"DOCKER_HOST":    "unix:///run/user/1000/docker.sock",
		"DOCKER_CONTEXT": "colima",
		"DOCKER_CONFIG":  "/real/.docker",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}

// TestIsolatedEnvDefaultsTerm pins the TERM fallback: an absent, empty, or
// "dumb" inherited value is replaced with a modern terminfo entry, and any
// other value passes through.
func TestIsolatedEnvDefaultsTerm(t *testing.T) {
	for _, test := range []struct {
		name  string
		set   bool
		value string
		want  string
	}{
		{name: "absent", set: false, value: "", want: driver.DefaultTerm},
		{name: "empty", set: true, value: "", want: driver.DefaultTerm},
		{name: "dumb", set: true, value: driver.TermDumb, want: driver.DefaultTerm},
		{name: "present", set: true, value: "screen-256color", want: "screen-256color"},
	} {
		t.Run(test.name, func(t *testing.T) {
			inherited := []string{"PATH=/usr/bin:/bin"}
			if test.set {
				inherited = append(inherited, driver.EnvTerm+"="+test.value)
			}
			if got := envValue(isolatedEnv("/root", "/root", inherited), driver.EnvTerm); got != test.want {
				t.Errorf("TERM = %q, want %q", got, test.want)
			}
		})
	}
}

// envValue returns the value of name in env, or "" when it is absent.
func envValue(env []string, name string) string {
	for _, entry := range env {
		if k, v, _ := strings.Cut(entry, "="); k == name {
			return v
		}
	}
	return ""
}

// TestTuiLeft pins the alternate-screen-leave signal the quit loop stops tapping
// on: an alternate-screen harness emits it while giving the screen back as it
// tears down, and falls back to cooked mode there, so a further tap would be
// reprocessed as SIGINT and kill it mid-teardown. It must not fire on the enter
// sequence the readiness wait watches for.
func TestTuiLeft(t *testing.T) {
	if !driver.TuiLeft([]byte("tui output\n\x1b[?1049lbye")) {
		t.Error("TuiLeft = false, want true for the alternate-screen leave sequence")
	}
	if driver.TuiLeft([]byte("tui output\nstill up")) {
		t.Error("TuiLeft = true, want false without the leave sequence")
	}
	if driver.TuiLeft([]byte("launcher output\n\x1b[?1049htui")) {
		t.Error("TuiLeft = true for the enter sequence, want false")
	}
}

// TestQuitIntervalKeepsPairsRapidAndWaitsBetweenThem pins the quit tap cadence:
// the two presses of a pair stay one quitPressInterval apart (so a repeated-key
// exit chains), and every even press ends a pair and is followed by the longer
// quitRetryInterval, which gives a slow teardown room to finish before the next
// press.
func TestQuitIntervalKeepsPairsRapidAndWaitsBetweenThem(t *testing.T) {
	for _, test := range []struct {
		presses int
		want    time.Duration
	}{
		{presses: 1, want: quitPressInterval},
		{presses: 2, want: quitRetryInterval},
		{presses: 3, want: quitPressInterval},
		{presses: 4, want: quitRetryInterval},
	} {
		if got := quitIntervalAfterPress(test.presses); got != test.want {
			t.Errorf("quitIntervalAfterPress(%d) = %s, want %s", test.presses, got, test.want)
		}
	}
	// The pair must stay inside qwen-code's measured chaining window (~1.2s).
	if quitPressInterval >= quitRetryInterval {
		t.Errorf("quitPressInterval %s is not shorter than quitRetryInterval %s", quitPressInterval, quitRetryInterval)
	}
}

// TestHarnessConcurrencyForFreeMemory pins the memory-derived parallel-harness
// count: as many smoke VMs as fit after the host reserve, floored, and never
// fewer than one.
func TestHarnessConcurrencyForFreeMemory(t *testing.T) {
	perVM := smokeWorkingSetMiB
	tests := []struct {
		name    string
		freeMiB int
		want    int
	}{
		{"plenty of headroom", 32 * 1024, (32*1024 - harnessHostReserveMiB) / perVM},
		{"exactly the reserve", harnessHostReserveMiB, 1},
		{"below the reserve", 512, 1},
		{"one smoke VM fits", harnessHostReserveMiB + perVM, 1},
		{"a partial VM does not round up", harnessHostReserveMiB + 3*perVM + perVM - 1, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := harnessConcurrencyFor(tc.freeMiB); got != tc.want {
				t.Errorf("harnessConcurrencyFor(%d) = %d, want %d", tc.freeMiB, got, tc.want)
			}
		})
	}
}

// TestHarnessRunArgsCapTheSmokeMemory pins that a harness smoke run boots its VM
// at the smoke memory, not the launcher's larger default, passes --yes so the
// launcher's first-run setup menu never blocks the pty before the harness, and
// passes --no-rebuild so the run reuses the suite's prebuilt image without the
// agent upgrade check (which, against the run's fresh machine-global state,
// re-resolves each agent's latest release every run).
func TestHarnessRunArgsCapTheSmokeMemory(t *testing.T) {
	got := harnessRunArgs("opencode", "none")
	want := []string{
		"run",
		"--agent",
		"opencode",
		"--network",
		"none",
		"--memory",
		smokeMemorySpec(),
		"--yes",
		"--no-rebuild",
	}
	if !slices.Equal(got, want) {
		t.Errorf("harnessRunArgs = %v, want %v", got, want)
	}
}

// TestDockerStorageShellArgsReuseThePrebuiltImage pins that the docker-storage
// sibling's `shell` reuses the suite's prebuilt image too: it drives a
// docker-enabled opencode VM under the given network with --no-rebuild, so it
// makes no release-resolution request.
func TestDockerStorageShellArgsReuseThePrebuiltImage(t *testing.T) {
	got := dockerStorageShellArgs("none")
	want := []string{"shell", "--agent", "opencode", "--docker", "--network", "none", "--no-rebuild"}
	if !slices.Equal(got, want) {
		t.Errorf("dockerStorageShellArgs = %v, want %v", got, want)
	}
}

// TestSmokeVMCPUsFromValue pins the per-VM vCPU cap parsing: a positive 8-bit
// integer caps the VM, and every other value (unset, zero, non-numeric, or
// beyond a uint8) leaves the launcher's default.
func TestSmokeVMCPUsFromValue(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  uint8
	}{
		{"unset", "", 0},
		{"zero", "0", 0},
		{"positive", "2", 2},
		{"padded", "  3  ", 3},
		{"non-numeric", "abc", 0},
		{"negative", "-1", 0},
		{"beyond uint8", "256", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := smokeVMCPUsFromValue(tc.value); got != tc.want {
				t.Errorf("smokeVMCPUsFromValue(%q) = %d, want %d", tc.value, got, tc.want)
			}
		})
	}
}

// TestWithVMCPUsAppendsOnlyWhenCapped pins that a zero cap leaves the argv
// untouched (so a host run is unchanged) and a non-zero cap appends the
// launcher's --cpus flag with the value.
func TestWithVMCPUsAppendsOnlyWhenCapped(t *testing.T) {
	base := []string{"run", "--agent", "opencode"}
	if got := withVMCPUs(append([]string(nil), base...), 0); !slices.Equal(got, base) {
		t.Errorf("withVMCPUs(cpus 0) = %v, want %v", got, base)
	}
	got := withVMCPUs(append([]string(nil), base...), 2)
	want := []string{"run", "--agent", "opencode", "--cpus", "2"}
	if !slices.Equal(got, want) {
		t.Errorf("withVMCPUs(cpus 2) = %v, want %v", got, want)
	}
}

// TestWithGitHubTokenAddsTheTokenWhenPresent pins that a prebuild child gets the
// environment's GitHub API token, so its release-resolution requests
// authenticate instead of sharing the anonymous api.github.com limit. GH_TOKEN
// wins over GITHUB_TOKEN, matching the launcher's own precedence.
func TestWithGitHubTokenAddsTheTokenWhenPresent(t *testing.T) {
	for _, tc := range []struct {
		name      string
		inherited []string
		wantName  string
		wantValue string
	}{
		{"gh-token", []string{"GH_TOKEN=ghp_x"}, "GH_TOKEN", "ghp_x"},
		{"github-token", []string{"GITHUB_TOKEN=ghp_y"}, "GITHUB_TOKEN", "ghp_y"},
		{"gh-token-wins", []string{"GH_TOKEN=ghp_x", "GITHUB_TOKEN=ghp_y"}, "GH_TOKEN", "ghp_x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := withGitHubToken([]string{"PATH=/usr/bin"}, tc.inherited)
			if value := envValue(got, tc.wantName); value != tc.wantValue {
				t.Errorf("%s = %q, want %q", tc.wantName, value, tc.wantValue)
			}
		})
	}
}

// TestWithGitHubTokenLeavesEnvUnchangedWhenAbsent pins that no token variable is
// injected when the environment has none, so an anonymous host stays anonymous.
func TestWithGitHubTokenLeavesEnvUnchangedWhenAbsent(t *testing.T) {
	env := []string{"PATH=/usr/bin"}
	got := withGitHubToken(env, []string{"PATH=/usr/bin"})
	if !slices.Equal(got, env) {
		t.Errorf("withGitHubToken = %v, want the env unchanged (%v)", got, env)
	}
}
