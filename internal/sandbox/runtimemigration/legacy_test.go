package runtimemigration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

func TestLegacyVolumeNamesEmpty(t *testing.T) {
	home := newHome(t)
	got, err := legacyVolumeNames(home)
	if err != nil {
		t.Fatalf("legacyVolumeNames() error = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("legacyVolumeNames() = %v, want empty", got)
	}
}

func TestLegacyVolumeNamesMissingVolumesDir(t *testing.T) {
	home := t.TempDir()
	got, err := legacyVolumeNames(home)
	if err != nil {
		t.Fatalf("legacyVolumeNames() error = %v, want nil (missing dir is not an error)", err)
	}
	if len(got) != 0 {
		t.Errorf("legacyVolumeNames() = %v, want empty", got)
	}
}

func TestLegacyVolumeNamesVolumesPathNotADirectory(t *testing.T) {
	home := t.TempDir()
	addRegularFile(t, home, "volumes")

	got, err := legacyVolumeNames(home)
	if err != nil {
		t.Fatalf("legacyVolumeNames() error = %v, want nil (unreadable dir is not an error)", err)
	}
	if len(got) != 0 {
		t.Errorf("legacyVolumeNames() = %v, want empty", got)
	}
}

func TestLegacyVolumeNamesUnreadableVolumesDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 0 does not block directory reads")
	}
	home := newHome(t)
	addVolumeDir(t, home, naming.HomePrefix+"hidden")

	volumesDir := filepath.Join(home, "volumes")
	if err := os.Chmod(volumesDir, 0o000); err != nil {
		t.Fatalf("chmod volumes dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(volumesDir, 0o750) })

	got, err := legacyVolumeNames(home)
	if err != nil {
		t.Fatalf("legacyVolumeNames() error = %v, want nil (permission denied is not an error)", err)
	}
	if len(got) != 0 {
		t.Errorf("legacyVolumeNames() = %v, want empty", got)
	}

	necessary, err := necessaryVolumes(home, newHome(t), state.Migration{})
	if err != nil {
		t.Fatalf("necessaryVolumes() error = %v, want nil (permission denied is not an error)", err)
	}
	if len(necessary) != 0 {
		t.Errorf("necessaryVolumes() = %v, want empty (not necessary)", necessary)
	}
}

func TestLegacyVolumeNamesFiltersByPrefix(t *testing.T) {
	home := newHome(t)
	want := []string{naming.HomePrefix + "alpha", naming.HomePrefix + "beta"}
	addVolumeDir(t, home, want[0])
	addVolumeDir(t, home, want[1])
	addVolumeDir(t, home, "some-other-volume")
	addRegularFile(t, filepath.Join(home, "volumes"), "agents-sandbox-home-file")

	got, err := legacyVolumeNames(home)
	if err != nil {
		t.Fatalf("legacyVolumeNames() error = %v", err)
	}
	assertSameSet(t, got, want)
}

func TestNecessaryVolumesEmptyLegacy(t *testing.T) {
	legacy := newHome(t)
	private := newHome(t)

	got, err := necessaryVolumes(legacy, private, state.Migration{})
	if err != nil {
		t.Fatalf("necessaryVolumes() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("necessaryVolumes() = %v, want empty", got)
	}
}

func TestNecessaryVolumesLegacyOnlyIsNecessary(t *testing.T) {
	legacy := newHome(t)
	private := newHome(t)
	name := naming.HomePrefix + "orphan"
	addVolumeDir(t, legacy, name)

	got, err := necessaryVolumes(legacy, private, state.Migration{})
	if err != nil {
		t.Fatalf("necessaryVolumes() error = %v", err)
	}
	assertSameSet(t, got, []string{name})
}

func TestNecessaryVolumesPresentInBothIsNotNecessary(t *testing.T) {
	legacy := newHome(t)
	private := newHome(t)
	name := naming.HomePrefix + "shared"
	addVolumeDir(t, legacy, name)
	addVolumeDir(t, private, name)

	got, err := necessaryVolumes(legacy, private, state.Migration{})
	if err != nil {
		t.Fatalf("necessaryVolumes() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("necessaryVolumes() = %v, want empty (already in private home)", got)
	}
}

func TestNecessaryVolumesMissingLegacyVolumesDirIsNotNecessary(t *testing.T) {
	legacy := t.TempDir()
	private := newHome(t)

	got, err := necessaryVolumes(legacy, private, state.Migration{})
	if err != nil {
		t.Fatalf("necessaryVolumes() error = %v, want nil (missing dir is not an error)", err)
	}
	if len(got) != 0 {
		t.Errorf("necessaryVolumes() = %v, want empty", got)
	}
}

func TestNecessaryVolumesReportsOnlyMissingLegacyNames(t *testing.T) {
	legacy := newHome(t)
	private := newHome(t)
	present := naming.HomePrefix + "present"
	missing := naming.HomePrefix + "missing"
	addVolumeDir(t, legacy, present)
	addVolumeDir(t, legacy, missing)
	addVolumeDir(t, private, present)
	addVolumeDir(t, private, naming.HomePrefix+"private-only")

	got, err := necessaryVolumes(legacy, private, state.Migration{})
	if err != nil {
		t.Fatalf("necessaryVolumes() error = %v", err)
	}
	assertSameSet(t, got, []string{missing})
}

func TestNecessaryVolumesTerminalStatusIsNeverNecessary(t *testing.T) {
	for _, status := range []state.MigrationStatus{state.MigrationCompleted, state.MigrationDismissed} {
		legacy := newHome(t)
		addVolumeDir(t, legacy, naming.HomePrefix+"orphan")
		got, err := necessaryVolumes(legacy, newHome(t), state.Migration{Status: status})
		if err != nil || len(got) != 0 {
			t.Fatalf("status %q: got %v, %v; want empty", status, got, err)
		}
	}
}

func TestNecessaryVolumesCopyingSubtractsCopiedAndExcluded(t *testing.T) {
	legacy := newHome(t)
	private := newHome(t)
	copied := naming.HomePrefix + "copied"
	excluded := naming.HomePrefix + "excluded"
	pending := naming.HomePrefix + "pending"
	addVolumeDir(t, legacy, copied)
	addVolumeDir(t, legacy, excluded)
	addVolumeDir(t, legacy, pending)
	m := state.Migration{Status: state.MigrationCopying, Copied: []string{copied}, Excluded: []string{excluded}}
	got, err := necessaryVolumes(legacy, private, m)
	if err != nil {
		t.Fatalf("necessaryVolumes: %v", err)
	}
	assertSameSet(t, got, []string{pending})
}

func TestNecessaryVolumesCopyingIgnoresPrivatePresence(t *testing.T) {
	legacy := newHome(t)
	private := newHome(t)
	name := naming.HomePrefix + "partial"
	addVolumeDir(t, legacy, name)
	addVolumeDir(t, private, name) // present but NOT in Copied => partial, still necessary
	got, err := necessaryVolumes(legacy, private, state.Migration{Status: state.MigrationCopying})
	if err != nil {
		t.Fatalf("necessaryVolumes: %v", err)
	}
	assertSameSet(t, got, []string{name})
}

// TestNecessaryVolumesCopyingFailsClosedOnUnlistableLegacyHome pins the review
// finding: during a copying campaign an existing-but-unlistable legacy volumes
// directory must be a campaign-scoped error so the campaign never finalizes
// completed on an under-migrated home. The pending state keeps the tolerant
// behavior and treats the same directory as empty.
func TestNecessaryVolumesCopyingFailsClosedOnUnlistableLegacyHome(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 0 does not block directory reads")
	}
	legacy := newHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"hidden")

	volumesDir := filepath.Join(legacy, "volumes")
	if err := os.Chmod(volumesDir, 0o000); err != nil {
		t.Fatalf("chmod volumes dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(volumesDir, 0o750) })

	got, err := necessaryVolumes(legacy, newHome(t), state.Migration{})
	if err != nil || len(got) != 0 {
		t.Fatalf("pending: got %v, %v; want empty, nil (unreadable dir tolerated)", got, err)
	}

	if got, err := necessaryVolumes(legacy, newHome(t), state.Migration{Status: state.MigrationCopying}); err == nil {
		t.Fatalf("copying: got %v, nil; want an error (campaign fails closed)", got)
	}
}

// blockRootLock occupies the shared root-state lock path with a directory so
// lockRootState's open fails. It pins that a campaign and the dismissal paths
// surface a lock-acquisition failure rather than a silent no-op.
func blockRootLock(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(configpaths.Get().UserStateDir(), "locks", "root.lock"), 0o700); err != nil {
		t.Fatalf("block root lock: %v", err)
	}
}

// installLegacyHome points runtime.LegacyHome() at a fresh legacy home under the
// mocked config-paths home, whose .microsandbox is created with an empty volumes
// directory. It returns the resolved legacy home.
func installLegacyHome(t *testing.T) string {
	t.Helper()
	home, err := configpaths.Get().UserHomeDir()
	if err != nil {
		t.Fatalf("resolve mocked home: %v", err)
	}
	legacy := filepath.Join(home, ".microsandbox")
	if err := os.MkdirAll(filepath.Join(legacy, "volumes"), 0o750); err != nil {
		t.Fatalf("create legacy volumes dir: %v", err)
	}
	return legacy
}

func newHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "volumes"), 0o750); err != nil {
		t.Fatalf("create volumes dir: %v", err)
	}
	return home
}

func addVolumeDir(t *testing.T, home, name string) {
	t.Helper()
	path := filepath.Join(home, "volumes", name)
	if err := os.MkdirAll(path, 0o750); err != nil {
		t.Fatalf("create volume dir %q: %v", name, err)
	}
}

func addRegularFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
		t.Fatalf("create file %q: %v", name, err)
	}
}

// loopedVolumeHome returns a home whose volumes entry is a symlink to itself,
// so os.ReadDir on it fails with ELOOP: an unexpected error that
// homeVolumeNames must propagate rather than tolerate.
func loopedVolumeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.Symlink("volumes", filepath.Join(home, "volumes")); err != nil {
		t.Fatalf("symlink volumes: %v", err)
	}
	return home
}

func TestPresentLegacyTargets(t *testing.T) {
	legacy := newHome(t)
	private := newHome(t)
	present := naming.HomePrefix + "present"
	addVolumeDir(t, legacy, present)
	addVolumeDir(t, legacy, naming.HomePrefix+"missing")
	addVolumeDir(t, private, present)
	addVolumeDir(t, private, naming.HomePrefix+"unrelated")

	got, err := presentLegacyTargets(legacy, private)
	if err != nil {
		t.Fatalf("presentLegacyTargets: %v", err)
	}
	assertSameSet(t, got, []string{present})
}

func TestPresentLegacyTargetsLegacyReadError(t *testing.T) {
	if _, err := presentLegacyTargets(loopedVolumeHome(t), newHome(t)); err == nil {
		t.Fatal("presentLegacyTargets() error = nil, want legacy listing error")
	}
}

func TestPresentLegacyTargetsPrivateReadError(t *testing.T) {
	if _, err := presentLegacyTargets(newHome(t), loopedVolumeHome(t)); err == nil {
		t.Fatal("presentLegacyTargets() error = nil, want private listing error")
	}
}

func TestNecessaryVolumesLegacyReadError(t *testing.T) {
	got, err := necessaryVolumes(loopedVolumeHome(t), newHome(t), state.Migration{})
	if err == nil {
		t.Fatal("necessaryVolumes() error = nil, want legacy listing error")
	}
	if got != nil {
		t.Errorf("necessaryVolumes() = %v, want nil on error", got)
	}
}

func TestNecessaryVolumesPrivateReadError(t *testing.T) {
	got, err := necessaryVolumes(newHome(t), loopedVolumeHome(t), state.Migration{})
	if err == nil {
		t.Fatal("necessaryVolumes() error = nil, want private listing error")
	}
	if got != nil {
		t.Errorf("necessaryVolumes() = %v, want nil on error", got)
	}
}

func assertSameSet(t *testing.T, got, want []string) {
	t.Helper()
	gotSet := make(map[string]bool, len(got))
	for _, name := range got {
		if gotSet[name] {
			t.Errorf("duplicate entry %q in result %v", name, got)
		}
		gotSet[name] = true
	}
	for _, name := range want {
		if !gotSet[name] {
			t.Errorf("result %v missing %q", got, name)
		}
	}
	if len(gotSet) != len(want) {
		t.Errorf("result %v = %d entries, want %d", got, len(gotSet), len(want))
	}
}
