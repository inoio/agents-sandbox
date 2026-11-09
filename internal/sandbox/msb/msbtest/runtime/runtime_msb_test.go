//go:build msb

package runtime

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/hostlock"
)

// TestSweepWithNoSandboxes pins that a sweep in an isolated home with nothing
// running is inert: it neither errors nor touches the SDK.
func TestSweepWithNoSandboxes(t *testing.T) {
	ready := isolatedRuntimeReady
	t.Cleanup(func() { isolatedRuntimeReady = ready })
	isolatedRuntimeReady = false

	if err := SweepIsolatedSandboxes(); err != nil {
		t.Fatalf("sweep: %v", err)
	}
}

// fakeSweepSandbox is a sweepSandbox that records its destruction.
type fakeSweepSandbox struct {
	name       string
	destroyErr error
	destroyed  bool
}

func (f *fakeSweepSandbox) Name() string { return f.name }

func (f *fakeSweepSandbox) Destroy(context.Context) error {
	f.destroyed = true
	return f.destroyErr
}

// TestSweepSandboxesDestroysEveryPage pins the sweep's page walk: every sandbox
// on every page is destroyed, and each page is requested with the cursor the
// previous page returned.
func TestSweepSandboxesDestroysEveryPage(t *testing.T) {
	first := &fakeSweepSandbox{name: "first"}
	second := &fakeSweepSandbox{name: "second"}
	nextCursor := "page-2"

	pages := []struct {
		sandboxes  []sweepSandbox
		nextCursor *string
	}{
		{sandboxes: []sweepSandbox{first}, nextCursor: &nextCursor},
		{sandboxes: []sweepSandbox{second}, nextCursor: nil},
	}
	var cursors []*string
	pageIndex := 0
	list := func(_ context.Context, cursor *string) ([]sweepSandbox, *string, error) {
		cursors = append(cursors, cursor)
		page := pages[pageIndex]
		pageIndex++
		return page.sandboxes, page.nextCursor, nil
	}

	if err := sweepSandboxes(list); err != nil {
		t.Fatalf("sweepSandboxes() error = %v", err)
	}
	if !first.destroyed || !second.destroyed {
		t.Errorf("destroyed = (%v, %v), want both sandboxes destroyed", first.destroyed, second.destroyed)
	}
	if len(cursors) != 2 || cursors[0] != nil || cursors[1] == nil || *cursors[1] != nextCursor {
		t.Errorf("list cursors = %v, want [nil, %q]", cursors, nextCursor)
	}
}

// TestSweepSandboxesReportsFailedDestroyAndKeepsSweeping pins the error
// aggregation: a sandbox that cannot be destroyed is named in the joined error
// and does not stop the sweep from destroying the sandboxes after it.
func TestSweepSandboxesReportsFailedDestroyAndKeepsSweeping(t *testing.T) {
	destroyErr := errors.New("destroy boom")
	wedged := &fakeSweepSandbox{name: "wedged", destroyErr: destroyErr}
	healthy := &fakeSweepSandbox{name: "healthy"}
	list := func(context.Context, *string) ([]sweepSandbox, *string, error) {
		return []sweepSandbox{wedged, healthy}, nil, nil
	}

	err := sweepSandboxes(list)
	if !errors.Is(err, destroyErr) {
		t.Fatalf("sweepSandboxes() error = %v, want it to wrap %v", err, destroyErr)
	}
	if !strings.Contains(err.Error(), "wedged") {
		t.Errorf("error %q does not name the sandbox it could not destroy", err)
	}
	if !healthy.destroyed {
		t.Error("a failed destroy stopped the sweep; later sandboxes must still be attempted")
	}
}

// TestSweepSandboxesReportsListFailure pins that a list failure aborts the sweep
// and is returned rather than swallowed.
func TestSweepSandboxesReportsListFailure(t *testing.T) {
	listErr := errors.New("list boom")
	list := func(context.Context, *string) ([]sweepSandbox, *string, error) {
		return nil, nil, listErr
	}

	if err := sweepSandboxes(list); !errors.Is(err, listErr) {
		t.Fatalf("sweepSandboxes() error = %v, want it to wrap %v", err, listErr)
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
