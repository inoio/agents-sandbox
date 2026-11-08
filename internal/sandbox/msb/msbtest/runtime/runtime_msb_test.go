//go:build msb

package runtime

import (
	"os"
	"testing"

	"github.com/inoio/agents-sandbox/internal/hostlock"
)

// TestSweepWithNoSandboxes pins that a sweep in an isolated home with nothing
// running is inert: it neither errors nor touches the SDK.
func TestSweepWithNoSandboxes(t *testing.T) {
	if err := SweepIsolatedSandboxes(); err != nil {
		t.Fatalf("sweep: %v", err)
	}
}

// TestInstallIsolatedRuntimeHomeSetsMSBHomeOnReuse pins that the reuse path
// points the SDK at the reused home. Setting only RuntimeConfig{Home: ...}
// would leave MSB_HOME at the real ~/.microsandbox, and sandbox creation — which
// resolves its storage root through MSB_HOME — would fail the "catalog ancestor
// permits path substitution" check on a reused home.
func TestInstallIsolatedRuntimeHomeSetsMSBHomeOnReuse(t *testing.T) {
	reuseDir := t.TempDir()
	t.Setenv(reuseRuntimeHomeEnv, reuseDir)
	// Register MSB_HOME for restoration: it starts at the home TestMain
	// installed, and InstallIsolatedRuntimeHome overwrites it through setMSBHome.
	t.Setenv("MSB_HOME", os.Getenv("MSB_HOME"))

	previousHome := isolatedRuntimeHome
	t.Cleanup(func() { isolatedRuntimeHome = previousHome })
	t.Cleanup(InstallIsolatedRuntimeHome())

	if got := os.Getenv("MSB_HOME"); got != reuseDir {
		t.Errorf("MSB_HOME = %q, want the reused home %q", got, reuseDir)
	}
	if isolatedRuntimeHome != reuseDir {
		t.Errorf("isolatedRuntimeHome = %q, want %q", isolatedRuntimeHome, reuseDir)
	}
}

// TestInstallIsolatedRuntimeHomeLocksReuseHome pins that a reused home is held
// under an exclusive lock for the run: two runs sharing a home serialize there
// instead of one run's sandbox sweep destroying the other's live VMs.
func TestInstallIsolatedRuntimeHomeLocksReuseHome(t *testing.T) {
	reuseDir := t.TempDir()
	t.Setenv(reuseRuntimeHomeEnv, reuseDir)
	t.Setenv("MSB_HOME", os.Getenv("MSB_HOME"))
	previousHome := isolatedRuntimeHome
	t.Cleanup(func() { isolatedRuntimeHome = previousHome })

	release := InstallIsolatedRuntimeHome()
	if !lockContended(t, reuseLockPath(reuseDir)) {
		t.Error("the reuse lock is free while the run holds it, want it held")
	}
	release()
	if lockContended(t, reuseLockPath(reuseDir)) {
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
