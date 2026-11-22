package driver

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/inoio/agents-sandbox/internal/hostlock"
)

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
	if !RootFitsSandboxSocket(root) {
		t.Errorf(
			"the default suite root %q derives a %d-byte sandbox control-socket path, at or above the %d-byte sun_path limit; keep smokeRootBaseDefault, any %s override, and smokeRootPrefix short",
			root,
			len(MsbHomeUnderRoot(root))+sandboxSocketPathSuffix,
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
	// mkdtemp (the ResolveSuiteRoot seam) with the short smokeRootBaseDefault,
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
// SmokeRootReuseEnv reuses (and creates) that directory, while an unset one
// creates a fresh temp root, and a reused path whose derived socket would exceed
// the budget is rejected.
func TestResolveSuiteRootReusesEnv(t *testing.T) {
	shortRoot := shortTempDir(t)

	t.Setenv(SmokeRootReuseEnv, shortRoot)
	root, reused, release, err := ResolveSuiteRoot()
	if err != nil {
		t.Fatalf("ResolveSuiteRoot: %v", err)
	}
	if !reused || root != shortRoot {
		t.Errorf("ResolveSuiteRoot = (%q, %v), want (%q, true)", root, reused, shortRoot)
	}
	if release == nil {
		t.Error("ResolveSuiteRoot returned no lock release for a reused root")
	} else {
		t.Cleanup(release)
	}

	t.Setenv(SmokeRootReuseEnv, filepath.Join(t.TempDir(), strings.Repeat("x", sunPathLimit)))
	if _, reused, _, err := ResolveSuiteRoot(); err == nil {
		t.Error("ResolveSuiteRoot = nil error, want a too-long reuse path rejected")
	} else if reused {
		t.Error("ResolveSuiteRoot reported a rejected reuse path as reused")
	}

	t.Setenv(SmokeRootReuseEnv, "")
	root, reused, release, err = ResolveSuiteRoot()
	if err != nil {
		t.Fatalf("ResolveSuiteRoot: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if release != nil {
		t.Error("ResolveSuiteRoot returned a lock release for a fresh root, want nil")
	}
	if reused || root == "" {
		t.Errorf("ResolveSuiteRoot = (%q, %v), want a fresh, non-reused root", root, reused)
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
	t.Setenv(SmokeRootReuseEnv, "")
	t.Setenv(smokeRootBaseEnv, base)

	root, reused, release, err := ResolveSuiteRoot()
	if err != nil {
		t.Fatalf("ResolveSuiteRoot: %v", err)
	}
	if release != nil {
		t.Error("ResolveSuiteRoot returned a lock release for a fresh root, want nil")
	}
	if reused {
		t.Error("ResolveSuiteRoot reused a root with the reuse env unset")
	}
	if gotBase != base {
		t.Errorf("ResolveSuiteRoot created under base %q, want the override %q", gotBase, base)
	}
	if gotPrefix != smokeRootPrefix {
		t.Errorf("ResolveSuiteRoot created with prefix %q, want %q", gotPrefix, smokeRootPrefix)
	}
	if want := filepath.Join(base, "root"); root != want {
		t.Errorf("ResolveSuiteRoot root = %q, want %q", root, want)
	}
}

// TestSmokeRootBaseOverrideRejectsLongBase pins that a base which cannot fit the
// socket budget is reported at resolution, not left to fail a harness later.
func TestSmokeRootBaseOverrideRejectsLongBase(t *testing.T) {
	longBase := filepath.Join(t.TempDir(), strings.Repeat("x", sunPathLimit))
	if err := os.MkdirAll(longBase, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv(SmokeRootReuseEnv, "")
	t.Setenv(smokeRootBaseEnv, longBase)

	root, reused, release, err := ResolveSuiteRoot()
	if err == nil {
		t.Fatal("ResolveSuiteRoot = nil error, want a too-long base rejected")
	}
	if !strings.Contains(err.Error(), smokeRootBaseEnv) {
		t.Errorf("ResolveSuiteRoot error %q, want it to name %s", err, smokeRootBaseEnv)
	}
	if root != "" || reused || release != nil {
		t.Errorf("ResolveSuiteRoot = (%q, %v, release %v), want zero values on failure", root, reused, release != nil)
	}
}

// TestResolveSuiteRootMakesTheReuseRootAbsolute pins that a relative reuse root
// is resolved before use: the image prebuild runs the launcher with the project
// directory as its working directory, so a relative smokeBinary path would look
// for the binary beneath the project dir instead of where it was built.
func TestResolveSuiteRootMakesTheReuseRootAbsolute(t *testing.T) {
	workDir := shortTempDir(t)
	t.Chdir(workDir)

	t.Setenv(SmokeRootReuseEnv, "r")
	root, reused, release, err := ResolveSuiteRoot()
	if err != nil {
		t.Fatalf("ResolveSuiteRoot: %v", err)
	}
	t.Cleanup(release)
	if !reused {
		t.Fatal("ResolveSuiteRoot did not reuse the configured root")
	}
	if !filepath.IsAbs(root) {
		t.Errorf("ResolveSuiteRoot root = %q, want an absolute path", root)
	}
	if want := filepath.Join(workDir, "r"); root != want {
		t.Errorf("ResolveSuiteRoot root = %q, want %q", root, want)
	}
}

// TestResolveSuiteRootLocksTheReuseRoot pins that a reused root is held under an
// exclusive lock for the run's lifetime: two runs sharing a root serialize there
// instead of one run's sandbox sweep destroying the other's live VMs.
func TestResolveSuiteRootLocksTheReuseRoot(t *testing.T) {
	shortRoot := shortTempDir(t)
	t.Setenv(SmokeRootReuseEnv, shortRoot)

	_, reused, release, err := ResolveSuiteRoot()
	if err != nil {
		t.Fatalf("ResolveSuiteRoot: %v", err)
	}
	if !reused {
		t.Fatal("ResolveSuiteRoot did not reuse the configured root")
	}
	if release == nil {
		t.Fatal("ResolveSuiteRoot returned no lock release for a reused root")
	}

	if !lockContended(t, ReuseLockPath(shortRoot)) {
		t.Error("the reuse lock is free while the run holds it, want it held")
	}
	release()
	if lockContended(t, ReuseLockPath(shortRoot)) {
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

	t.Setenv(SmokeRootReuseEnv, "r")
	_, reused, release, err := ResolveSuiteRoot()
	if err == nil {
		t.Fatal("ResolveSuiteRoot = nil error, want the resolution failure reported")
	}
	if !errors.Is(err, syscall.ENOENT) {
		t.Errorf("ResolveSuiteRoot error %q, want it to wrap the filesystem cause ENOENT", err)
	}
	if !strings.Contains(err.Error(), "resolve reuse root") {
		t.Errorf("ResolveSuiteRoot error %q, want it to name the reuse-root resolution", err)
	}
	if reused || release != nil {
		t.Errorf("ResolveSuiteRoot = (reused %v, release %v), want neither on failure", reused, release != nil)
	}
}

// TestResolveSuiteRootReportsTheLockFailure pins that a reuse root whose lock
// cannot be taken is reported, not returned as a locked reuse root with a nil
// release.
func TestResolveSuiteRootReportsTheLockFailure(t *testing.T) {
	shortRoot := shortTempDir(t)
	// A directory where the lock file belongs makes the lock open fail.
	lockPath := ReuseLockPath(shortRoot)
	if err := os.MkdirAll(lockPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(lockPath) })

	t.Setenv(SmokeRootReuseEnv, shortRoot)
	_, reused, release, err := ResolveSuiteRoot()
	if err == nil {
		t.Fatal("ResolveSuiteRoot = nil error, want the lock failure reported")
	}
	if !errors.Is(err, syscall.EISDIR) {
		t.Errorf("ResolveSuiteRoot error %q, want it to wrap the filesystem cause EISDIR", err)
	}
	if !strings.Contains(err.Error(), "lock reuse root") {
		t.Errorf("ResolveSuiteRoot error %q, want it to name the reuse-root lock", err)
	}
	if reused || release != nil {
		t.Errorf("ResolveSuiteRoot = (reused %v, release %v), want neither on failure", reused, release != nil)
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

		t.Setenv(SmokeRootReuseEnv, filepath.Join(blocker, "child"))
		_, reused, release, err := ResolveSuiteRoot()
		if err == nil {
			t.Fatal("ResolveSuiteRoot = nil error, want the create failure reported")
		}
		if !errors.Is(err, syscall.ENOTDIR) {
			t.Errorf("ResolveSuiteRoot error %q, want it to wrap the filesystem cause ENOTDIR", err)
		}
		if !strings.Contains(err.Error(), "create reuse root") {
			t.Errorf("ResolveSuiteRoot error %q, want it to name the reuse-root creation", err)
		}
		if reused || release != nil {
			t.Errorf("ResolveSuiteRoot = (reused %v, release %v), want neither on failure", reused, release != nil)
		}
	})

	t.Run("temp root cannot be created", func(t *testing.T) {
		originalMkdtemp := mkdtemp
		t.Cleanup(func() { mkdtemp = originalMkdtemp })
		mkdtemp = func(string, string) (string, error) { return "", syscall.ENOSPC }

		t.Setenv(SmokeRootReuseEnv, "")
		_, reused, release, err := ResolveSuiteRoot()
		if err == nil {
			t.Fatal("ResolveSuiteRoot = nil error, want the temp-root failure reported")
		}
		if !errors.Is(err, syscall.ENOSPC) {
			t.Errorf("ResolveSuiteRoot error %q, want it to wrap the filesystem cause ENOSPC", err)
		}
		if reused || release != nil {
			t.Errorf("ResolveSuiteRoot = (reused %v, release %v), want neither on failure", reused, release != nil)
		}
	})
}

// TestRootFitsSandboxSocketBoundary pins the strict boundary of the sun_path
// budget predicate: a root whose derived control-socket path is one byte short
// of the limit fits, and one whose path reaches the limit does not.
func TestRootFitsSandboxSocketBoundary(t *testing.T) {
	// MsbHomeUnderRoot appends a fixed suffix to root, and the derived socket
	// path adds sandboxSocketPathSuffix; `len(appended)` isolates the suffix
	// length for the boundary computation.
	appended := len(MsbHomeUnderRoot("x")) - len("x")
	fitsLen := sunPathLimit - sandboxSocketPathSuffix - appended - 1
	rootOfLength := func(n int) string { return "/" + strings.Repeat("a", n-1) }

	if !RootFitsSandboxSocket(rootOfLength(fitsLen)) {
		t.Errorf("RootFitsSandboxSocket rejected a %d-byte root, want the tightest fitting root accepted", fitsLen)
	}
	if RootFitsSandboxSocket(rootOfLength(fitsLen + 1)) {
		t.Errorf("RootFitsSandboxSocket accepted a %d-byte root, want one reaching the limit rejected", fitsLen+1)
	}
}

// TestHarnessConcurrencyForFreeMemory pins the memory-derived parallel-harness
// count: as many smoke VMs as fit after the host reserve, floored, and never
// fewer than one.
func TestHarnessConcurrencyForFreeMemory(t *testing.T) {
	perVM := SmokeWorkingSetMiB
	tests := []struct {
		name    string
		freeMiB int
		want    int
	}{
		{"plenty of headroom", 32 * 1024, (32*1024 - HarnessHostReserveMiB) / perVM},
		{"exactly the reserve", HarnessHostReserveMiB, 1},
		{"below the reserve", 512, 1},
		{"one smoke VM fits", HarnessHostReserveMiB + perVM, 1},
		{"a partial VM does not round up", HarnessHostReserveMiB + 3*perVM + perVM - 1, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := HarnessConcurrencyFor(tc.freeMiB); got != tc.want {
				t.Errorf("HarnessConcurrencyFor(%d) = %d, want %d", tc.freeMiB, got, tc.want)
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
	got := HarnessRunArgs("opencode", "none")
	want := []string{
		"run",
		"--agent",
		"opencode",
		"--network",
		"none",
		"--memory",
		SmokeMemorySpec(),
		"--yes",
		"--no-rebuild",
	}
	if !slices.Equal(got, want) {
		t.Errorf("HarnessRunArgs = %v, want %v", got, want)
	}
}

// TestDockerStorageShellArgsReuseThePrebuiltImage pins that the docker-storage
// sibling's `shell` reuses the suite's prebuilt image too: it drives a
// docker-enabled opencode VM under the given network with --no-rebuild, so it
// makes no release-resolution request.
func TestDockerStorageShellArgsReuseThePrebuiltImage(t *testing.T) {
	got := DockerStorageShellArgs("none")
	want := []string{"shell", "--agent", "opencode", "--docker", "--network", "none", "--no-rebuild"}
	if !slices.Equal(got, want) {
		t.Errorf("DockerStorageShellArgs = %v, want %v", got, want)
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
	if got := WithVMCPUs(append([]string(nil), base...), 0); !slices.Equal(got, base) {
		t.Errorf("WithVMCPUs(cpus 0) = %v, want %v", got, base)
	}
	got := WithVMCPUs(append([]string(nil), base...), 2)
	want := []string{"run", "--agent", "opencode", "--cpus", "2"}
	if !slices.Equal(got, want) {
		t.Errorf("WithVMCPUs(cpus 2) = %v, want %v", got, want)
	}
}
