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
	addVolumeDir(t, home, naming.LegacyHomePrefix+"hidden")

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
	want := []string{naming.LegacyHomePrefix + "alpha", naming.LegacyHomePrefix + "beta"}
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
	name := naming.LegacyHomePrefix + "orphan"
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
	name := naming.LegacyHomePrefix + "shared"
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
	present := naming.LegacyHomePrefix + "present"
	missing := naming.LegacyHomePrefix + "missing"
	addVolumeDir(t, legacy, present)
	addVolumeDir(t, legacy, missing)
	addVolumeDir(t, private, present)
	addVolumeDir(t, private, naming.LegacyHomePrefix+"private-only")

	got, err := necessaryVolumes(legacy, private, state.Migration{})
	if err != nil {
		t.Fatalf("necessaryVolumes() error = %v", err)
	}
	assertSameSet(t, got, []string{missing})
}

func TestNecessaryVolumesTerminalStatusIsNeverNecessary(t *testing.T) {
	for _, status := range []state.MigrationStatus{state.MigrationCompleted, state.MigrationDismissed, state.MigrationAcknowledged} {
		legacy := newHome(t)
		addVolumeDir(t, legacy, naming.LegacyHomePrefix+"orphan")
		got, err := necessaryVolumes(legacy, newHome(t), state.Migration{Status: status})
		if err != nil || len(got) != 0 {
			t.Fatalf("status %q: got %v, %v; want empty", status, got, err)
		}
	}
}

func TestNecessaryVolumesCopyingSubtractsCopiedAndExcluded(t *testing.T) {
	legacy := newHome(t)
	private := newHome(t)
	copied := naming.LegacyHomePrefix + "copied"
	excluded := naming.LegacyHomePrefix + "excluded"
	pending := naming.LegacyHomePrefix + "pending"
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
	name := naming.LegacyHomePrefix + "partial"
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
	addVolumeDir(t, legacy, naming.LegacyHomePrefix+"hidden")

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

// makeVolumeDirUnreadable chmods <home>/volumes to 000 so its listing fails with
// permission denied: the tolerant listing treats that as empty, while the strict
// listing reports it. The mode is restored in cleanup so t.TempDir can recurse
// into the directory when removing it.
func makeVolumeDirUnreadable(t *testing.T, home string) {
	t.Helper()
	volumes := filepath.Join(home, "volumes")
	if err := os.Chmod(volumes, 0o000); err != nil {
		t.Fatalf("chmod volumes 000: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(volumes, 0o750) })
}

func TestPresentLegacyTargets(t *testing.T) {
	legacy := newHome(t)
	private := newHome(t)
	present := naming.LegacyHomePrefix + "present"
	addVolumeDir(t, legacy, present)
	addVolumeDir(t, legacy, naming.LegacyHomePrefix+"missing")
	addVolumeDir(t, private, present)
	addVolumeDir(t, private, naming.LegacyHomePrefix+"unrelated")

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

// addSandboxDir creates <home>/sandboxes/<name>.
func addSandboxDir(t *testing.T, home, name string) {
	t.Helper()
	path := filepath.Join(home, "sandboxes", name)
	if err := os.MkdirAll(path, 0o750); err != nil {
		t.Fatalf("create sandbox record %q: %v", name, err)
	}
}

func TestDetectLegacyArtifactsFindsHomeVolumes(t *testing.T) {
	legacy := newHome(t)
	addVolumeDir(t, legacy, naming.LegacyHomePrefix+"a")
	got, err := detectLegacyArtifacts(legacy)
	if err != nil {
		t.Fatalf("detectLegacyArtifacts: %v", err)
	}
	if len(got.HomeVolumes) != 1 || len(got.Sandboxes) != 0 || !got.present() {
		t.Fatalf("artifacts = %+v, want one home volume and nothing else", got)
	}
}

func TestDetectLegacyArtifactsFindsLauncherSandboxes(t *testing.T) {
	legacy := newHome(t)
	addSandboxDir(t, legacy, naming.LegacyVMPrefix+"proj")
	addSandboxDir(t, legacy, naming.LegacyTaskPrefix+"proj")
	addSandboxDir(t, legacy, "user-own")
	got, err := detectLegacyArtifacts(legacy)
	if err != nil {
		t.Fatalf("detectLegacyArtifacts: %v", err)
	}
	if len(got.Sandboxes) != 2 || len(got.HomeVolumes) != 0 || !got.present() {
		t.Fatalf("artifacts = %+v, want the two launcher sandboxes only", got)
	}
}

// TestLegacyScanMatchesOnlyPrefixedArtifacts pins the foreign-scan boundary: in a
// shared legacy ~/.microsandbox the prefix is the only ownership signal, so a
// prefix-free launcher artifact (or a foreign name) must not be copied.
func TestLegacyScanMatchesOnlyPrefixedArtifacts(t *testing.T) {
	legacy := newHome(t)
	addVolumeDir(t, legacy, naming.LegacyHomePrefix+"proj-1abc1234defg5-opencode-20260812T123456")
	addSandboxDir(t, legacy, naming.LegacyVMPrefix+"proj-1abc1234defg5-opencode")
	addVolumeDir(t, legacy, naming.HomeVolumeName("proj-1abc1234defg5", "opencode", "20260812T123456"))
	addSandboxDir(t, legacy, "proj-1abc1234defg5-opencode")
	addVolumeDir(t, legacy, "theirs")
	addSandboxDir(t, legacy, "theirs")

	got, err := detectLegacyArtifacts(legacy)
	if err != nil {
		t.Fatalf("detectLegacyArtifacts: %v", err)
	}
	if len(got.HomeVolumes) != 1 || len(got.Sandboxes) != 1 {
		t.Fatalf("artifacts = %+v, want only the prefixed home volume and sandbox", got)
	}
	if got.HomeVolumes[0] != naming.LegacyHomePrefix+"proj-1abc1234defg5-opencode-20260812T123456" {
		t.Errorf("HomeVolumes = %v, want the prefixed volume", got.HomeVolumes)
	}
	if got.Sandboxes[0] != naming.LegacyVMPrefix+"proj-1abc1234defg5-opencode" {
		t.Errorf("Sandboxes = %v, want the prefixed sandbox", got.Sandboxes)
	}
}

func TestDetectLegacyArtifactsSilentForUnrelatedHome(t *testing.T) {
	legacy := newHome(t)
	addVolumeDir(t, legacy, "user-own-volume")
	addSandboxDir(t, legacy, "user-own-sandbox")
	got, err := detectLegacyArtifacts(legacy)
	if err != nil {
		t.Fatalf("detectLegacyArtifacts: %v", err)
	}
	if got.present() {
		t.Fatalf("artifacts = %+v, want none", got)
	}
}

func TestDetectLegacyArtifactsToleratesMissingDirs(t *testing.T) {
	got, err := detectLegacyArtifacts(t.TempDir())
	if err != nil {
		t.Fatalf("detectLegacyArtifacts: %v", err)
	}
	if got.present() {
		t.Fatalf("artifacts = %+v, want none for an empty home", got)
	}
}

// TestDetectLegacyArtifactsMarksUnreadableVolumes pins that an unlistable
// <legacyHome>/volumes is surfaced as VolumesUnreadable so command selection can
// withhold destructive commands, even though the tolerant listing reports it as
// empty and an unreadable directory alone must not fire the notice.
func TestDetectLegacyArtifactsMarksUnreadableVolumes(t *testing.T) {
	legacy := newHome(t)
	makeVolumeDirUnreadable(t, legacy)

	got, err := detectLegacyArtifacts(legacy)
	if err != nil {
		t.Fatalf("detectLegacyArtifacts: %v", err)
	}
	if !got.VolumesUnreadable {
		t.Errorf("artifacts = %+v, want VolumesUnreadable", got)
	}
	if len(got.HomeVolumes) != 0 {
		t.Errorf("HomeVolumes = %v, want none from an unreadable directory", got.HomeVolumes)
	}
	if got.present() {
		t.Error("artifacts.present() = true, want false: an unreadable volumes dir alone must not fire")
	}
}

func TestDetectLegacyArtifactsForResolvesLegacyHome(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.LegacyHomePrefix+"a")
	got, err := detectLegacyArtifactsFor()
	if err != nil {
		t.Fatalf("detectLegacyArtifactsFor: %v", err)
	}
	if !got.present() {
		t.Fatal("detectLegacyArtifactsFor() found no artifacts in the installed legacy home")
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

// TestDetectLegacyArtifactsErrorsOnUnlistableVolumes pins that a non-tolerated
// listing failure of <legacyHome>/volumes propagates rather than reading as an
// empty home, so detection never mistakes an I/O error for "no artifacts".
func TestDetectLegacyArtifactsErrorsOnUnlistableVolumes(t *testing.T) {
	legacy := t.TempDir()
	makeLegacyVolumesUnlistable(t, legacy)
	if _, err := detectLegacyArtifacts(legacy); err == nil {
		t.Fatal("detectLegacyArtifacts() error = nil, want the volumes listing failure")
	}
}

// TestDetectLegacyArtifactsErrorsOnUnlistableSandboxes is the sandbox-tier twin:
// the volumes listing succeeds, so the sandbox listing error is the one that
// must propagate.
func TestDetectLegacyArtifactsErrorsOnUnlistableSandboxes(t *testing.T) {
	legacy := t.TempDir()
	if err := os.Symlink("sandboxes", filepath.Join(legacy, "sandboxes")); err != nil {
		t.Fatalf("symlink sandboxes: %v", err)
	}
	if _, err := detectLegacyArtifacts(legacy); err == nil {
		t.Fatal("detectLegacyArtifacts() error = nil, want the sandboxes listing failure")
	}
}
