package runtimemigration

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/runtime"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestRenderNoticeShowsPresentTiersOnly(t *testing.T) {
	volumesOnly := renderNotice(legacyArtifacts{HomeVolumes: []string{naming.LegacyHomePrefix + "a"}}, nil)
	if !strings.Contains(volumesOnly, "volumes/agents-sandbox-home-*") {
		t.Errorf("volumes-only notice missing the volumes tier:\n%s", volumesOnly)
	}
	if strings.Contains(volumesOnly, "sandboxes/agents-sandbox-vm-*") {
		t.Errorf("volumes-only notice mentions a sandbox tier it did not find:\n%s", volumesOnly)
	}

	sandboxesOnly := renderNotice(legacyArtifacts{Sandboxes: []string{naming.LegacyVMPrefix + "p"}}, nil)
	if strings.Contains(sandboxesOnly, "volumes/agents-sandbox-home-*") {
		t.Errorf("sandboxes-only notice mentions a volumes tier it did not find:\n%s", sandboxesOnly)
	}
	if !strings.Contains(sandboxesOnly, "sandboxes/agents-sandbox-vm-*") {
		t.Errorf("sandboxes-only notice missing the sandboxes tier:\n%s", sandboxesOnly)
	}
}

func TestRenderNoticeAlwaysOffersTheWholesaleCommand(t *testing.T) {
	out := renderNotice(legacyArtifacts{HomeVolumes: []string{naming.LegacyHomePrefix + "a"}}, nil)
	if !strings.Contains(out, "rm -rf ~/.microsandbox\n") {
		t.Errorf("notice missing the wholesale delete command or its caveat:\n%s", out)
	}
}

func TestRenderNoticeNamesUncopiedVolumes(t *testing.T) {
	out := renderNotice(
		legacyArtifacts{HomeVolumes: []string{naming.LegacyHomePrefix + "a"}},
		[]string{naming.LegacyHomePrefix + "bad"},
	)
	if !strings.Contains(out, naming.LegacyHomePrefix+"bad") {
		t.Errorf("notice missing the uncopied volume name:\n%s", out)
	}
	if !strings.Contains(out, "were not copied into agents-sandbox") {
		t.Errorf("notice missing the uncopied warning:\n%s", out)
	}
}

func TestRenderNoticeSanitizesUncopiedNames(t *testing.T) {
	hostile := naming.LegacyHomePrefix + "a\n    rm -rf ~/Documents\x1b[31m"
	out := renderNotice(legacyArtifacts{HomeVolumes: []string{naming.LegacyHomePrefix + "a"}}, []string{hostile})
	if strings.Contains(out, "\n    rm -rf ~/Documents") {
		t.Errorf("a control character in a volume name forged a notice line:\n%q", out)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("a volume name leaked an escape byte into the notice:\n%q", out)
	}
}

func TestRenderNoticeUncopiedSuppressesWholesaleCommand(t *testing.T) {
	out := renderNotice(
		legacyArtifacts{HomeVolumes: []string{naming.LegacyHomePrefix + "a"}},
		[]string{naming.LegacyHomePrefix + "bad"},
	)
	if strings.Contains(out, "rm -rf ~/.microsandbox\n") {
		t.Errorf("notice suggests wholesale removal while uncopied volumes remain:\n%s", out)
	}
}

func TestRenderNoticeUncopiedSuppressesVolumeGlob(t *testing.T) {
	out := renderNotice(
		legacyArtifacts{HomeVolumes: []string{naming.LegacyHomePrefix + "a"}},
		[]string{naming.LegacyHomePrefix + "bad"},
	)
	if strings.Contains(out, "rm -rf ~/.microsandbox/volumes/agents-sandbox-home-*") {
		t.Errorf("notice suggests a volume glob that would delete the uncopied volume:\n%s", out)
	}
	if !strings.Contains(out, naming.LegacyHomePrefix+"bad") {
		t.Errorf("notice no longer names the uncopied volume:\n%s", out)
	}
}

// TestRenderNoticeUnreadableVolumesSuppressesWholesaleCommand pins that an
// unlistable legacy volumes directory withholds the wholesale command: unlisted
// home volumes may still hold data. The sandbox globs stay safe.
func TestRenderNoticeUnreadableVolumesSuppressesWholesaleCommand(t *testing.T) {
	out := renderNotice(
		legacyArtifacts{Sandboxes: []string{naming.LegacyVMPrefix + "p"}, VolumesUnreadable: true},
		nil,
	)
	if strings.Contains(out, "rm -rf ~/.microsandbox\n") {
		t.Errorf("notice suggests wholesale removal while the volumes directory is unreadable:\n%s", out)
	}
	if !strings.Contains(out, "could not list ~/.microsandbox/volumes") {
		t.Errorf("notice does not explain the unreadable volumes directory:\n%s", out)
	}
}

func TestShowNoticeSilentWithoutArtifacts(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termio.Mock{IsInteractiveResult: true}
	if err := showNotice(ui); err != nil {
		t.Fatalf("showNotice() error = %v", err)
	}
	if len(ui.AcknowledgeCalls) != 0 {
		t.Errorf("AcknowledgeCalls = %v, want none", ui.AcknowledgeCalls)
	}
}

func TestShowNoticeSilentWhenAcknowledged(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.LegacyHomePrefix+"a")
	if err := seedRootState(state.RootState{Migrations: map[string]state.Migration{
		LegacyRuntimeNoticeName: {Status: state.MigrationAcknowledged},
	}}); err != nil {
		t.Fatalf("seed acknowledged notice: %v", err)
	}
	ui := &termio.Mock{IsInteractiveResult: true}
	if err := showNotice(ui); err != nil {
		t.Fatalf("showNotice() error = %v", err)
	}
	if len(ui.AcknowledgeCalls) != 0 {
		t.Errorf("AcknowledgeCalls = %v, want none when already acknowledged", ui.AcknowledgeCalls)
	}
}

func TestShowNoticeNamesUncopiedVolumes(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	uncopied := naming.LegacyHomePrefix + "uncopied"
	addVolumeDir(t, legacy, uncopied)
	ui := &termio.Mock{IsInteractiveResult: true}
	if err := showNotice(ui); err != nil {
		t.Fatalf("showNotice() error = %v", err)
	}
	if len(ui.AcknowledgeCalls) != 1 {
		t.Fatalf("AcknowledgeCalls = %v, want the notice once", ui.AcknowledgeCalls)
	}
	if !strings.Contains(ui.AcknowledgeCalls[0], uncopied) {
		t.Errorf("notice = %q, want the uncopied volume %q named", ui.AcknowledgeCalls[0], uncopied)
	}
}

// TestShowNoticeTreatsCopiedVolumeAsSafe pins the positive direction: a legacy
// volume the campaign recorded as fully copied, with its destination still
// present, is not named and does not suppress the destructive commands.
func TestShowNoticeTreatsCopiedVolumeAsSafe(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	name := naming.LegacyHomePrefix + "a"
	addVolumeDir(t, legacy, name)
	createPrivateVolume(t, name)
	if err := seedRootState(state.RootState{Migrations: map[string]state.Migration{
		MigrationName: {Status: state.MigrationCompleted, Copied: []string{name}},
	}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ui := &termio.Mock{IsInteractiveResult: true}
	if err := showNotice(ui); err != nil {
		t.Fatalf("showNotice() error = %v", err)
	}
	if len(ui.AcknowledgeCalls) != 1 {
		t.Fatalf("AcknowledgeCalls = %v, want the notice once", ui.AcknowledgeCalls)
	}
	notice := ui.AcknowledgeCalls[0]
	if strings.Contains(notice, name) {
		t.Errorf("notice = %q, must not name the safely copied volume %q", notice, name)
	}
	if !strings.Contains(notice, "rm -rf ~/.microsandbox/volumes/agents-sandbox-home-*") {
		t.Errorf("notice = %q, want the volume-removal command for a safely copied volume", notice)
	}
}

func TestShowNoticeInteractiveMarksAcknowledged(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.LegacyHomePrefix+"a")
	ui := &termio.Mock{IsInteractiveResult: true}
	if err := showNotice(ui); err != nil {
		t.Fatalf("showNotice() error = %v", err)
	}
	if len(ui.AcknowledgeCalls) != 1 {
		t.Fatalf("AcknowledgeCalls = %v, want exactly one", ui.AcknowledgeCalls)
	}
	root, _ := state.ReadRootState()
	got, ok := root.Migration(LegacyRuntimeNoticeName)
	if !ok || got.Status != state.MigrationAcknowledged {
		t.Fatalf("notice entry = %+v (ok=%v), want acknowledged", got, ok)
	}
}

func TestShowNoticeNonInteractivePrintsBoundedLineButDoesNotMark(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.LegacyHomePrefix+"a")
	ui := &termio.Mock{IsInteractiveResult: false}
	if err := showNotice(ui); err != nil {
		t.Fatalf("showNotice() error = %v", err)
	}
	if len(ui.AcknowledgeCalls) != 1 {
		t.Fatalf("AcknowledgeCalls = %v, want the bounded one-liner", ui.AcknowledgeCalls)
	}
	line := ui.AcknowledgeCalls[0]
	if strings.Contains(line, "rm -rf") {
		t.Errorf("non-interactive notice contains removal commands: %q", line)
	}
	if strings.Contains(line, "\n") {
		t.Errorf("non-interactive notice is not a single line: %q", line)
	}
	root, _ := state.ReadRootState()
	if _, ok := root.Migration(LegacyRuntimeNoticeName); ok {
		t.Error("non-interactive run recorded an acknowledgment")
	}
}

func TestShowNoticeAcknowledgeErrorWarnsAndContinues(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.LegacyHomePrefix+"a")
	ui := &termio.Mock{IsInteractiveResult: true, AcknowledgeErr: errors.New("stdin closed")}
	if err := showNotice(ui); err != nil {
		t.Fatalf("showNotice() error = %v, want nil (best-effort)", err)
	}
	if len(ui.WarnCalls) == 0 {
		t.Error("no warning for an acknowledgment failure")
	}
	root, _ := state.ReadRootState()
	if _, ok := root.Migration(LegacyRuntimeNoticeName); ok {
		t.Error("a failed acknowledgment was recorded")
	}
}

// TestShowNoticeRecordErrorWarnsAndContinues pins that a failure to persist the
// acknowledgment is best-effort: it warns and the session continues without
// recording the notice. The state stays readable (a missing file reads as the
// zero state) but its directory is unwritable, so only the write fails.
func TestShowNoticeRecordErrorWarnsAndContinues(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.LegacyHomePrefix+"a")
	blockRootState(t)
	ui := &termio.Mock{IsInteractiveResult: true}
	if err := showNotice(ui); err != nil {
		t.Fatalf("showNotice() error = %v, want nil (best-effort)", err)
	}
	if !warnedAbout(ui.WarnCalls, "could not record legacy runtime notice") {
		t.Errorf("WarnCalls = %v, want the record-failure warning", ui.WarnCalls)
	}
}

func TestShowNoticeDetectionErrorWarnsAndContinues(t *testing.T) {
	configpathstest.WithMockConfigPathsHomeError(t, errors.New("home unavailable"))
	ui := &termio.Mock{IsInteractiveResult: true}
	if err := showNotice(ui); err != nil {
		t.Fatalf("showNotice() error = %v, want nil (best-effort)", err)
	}
	if len(ui.WarnCalls) == 0 {
		t.Error("no warning for a detection failure")
	}
}

// TestShowNoticeStateReadErrorWarnsAndContinues covers the notice's own
// best-effort state read: a corrupt root state warns and lets the session
// continue rather than blocking.
func TestShowNoticeStateReadErrorWarnsAndContinues(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	writeCorruptRootState(t)
	ui := &termio.Mock{IsInteractiveResult: true}
	if err := showNotice(ui); err != nil {
		t.Fatalf("showNotice() error = %v, want nil (best-effort)", err)
	}
	if !warnedAbout(ui.WarnCalls, "skipping legacy runtime notice") {
		t.Errorf("WarnCalls = %v, want the state-read warning", ui.WarnCalls)
	}
}

// TestShowNoticePrivateListingErrorSuppressesCommands pins the conservative
// fallback: when the private home cannot be listed, every legacy volume is
// treated as uncopied so the notice never prints a command that could delete
// data.
func TestShowNoticePrivateListingErrorSuppressesCommands(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	name := naming.LegacyHomePrefix + "a"
	addVolumeDir(t, legacy, name)

	volumes := filepath.Join(runtime.PrivateHome(), "volumes")
	if err := os.MkdirAll(filepath.Dir(volumes), 0o750); err != nil {
		t.Fatalf("mkdir private home: %v", err)
	}
	if err := os.Symlink("volumes", volumes); err != nil {
		t.Fatalf("symlink private volumes: %v", err)
	}

	ui := &termio.Mock{IsInteractiveResult: true}
	if err := showNotice(ui); err != nil {
		t.Fatalf("showNotice() error = %v, want nil (best-effort)", err)
	}
	if !warnedAbout(ui.WarnCalls, "could not list the private home") {
		t.Errorf("WarnCalls = %v, want the conservative-listing warning", ui.WarnCalls)
	}
	if len(ui.AcknowledgeCalls) != 1 {
		t.Fatalf("AcknowledgeCalls = %v, want the notice once", ui.AcknowledgeCalls)
	}
	assertSuppressesDestructiveCommands(t, ui.AcknowledgeCalls[0])
}

// TestShowNoticeUnreadableVolumesSuppressesWholesaleCommand pins the end-to-end
// conservative path: launcher sandboxes make the notice fire while
// <legacyHome>/volumes is unlistable, so the notice withholds the wholesale
// command and explains why instead.
func TestShowNoticeUnreadableVolumesSuppressesWholesaleCommand(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	makeVolumeDirUnreadable(t, legacy)
	addSandboxDir(t, legacy, naming.LegacyVMPrefix+"proj")

	ui := &termio.Mock{IsInteractiveResult: true}
	if err := showNotice(ui); err != nil {
		t.Fatalf("showNotice() error = %v, want nil (best-effort)", err)
	}
	if len(ui.AcknowledgeCalls) != 1 {
		t.Fatalf("AcknowledgeCalls = %v, want the notice once", ui.AcknowledgeCalls)
	}
	assertSuppressesDestructiveCommands(t, ui.AcknowledgeCalls[0])
	if !strings.Contains(ui.AcknowledgeCalls[0], "could not list ~/.microsandbox/volumes") {
		t.Errorf("notice = %q, want the unreadable-volumes note", ui.AcknowledgeCalls[0])
	}
}

// assertSuppressesDestructiveCommands fails when a notice suggests deleting the
// whole legacy home or its agents-sandbox-home-* volumes: both would delete an
// uncopied volume that still holds data.
func assertSuppressesDestructiveCommands(t *testing.T, notice string) {
	t.Helper()
	if strings.Contains(notice, "rm -rf ~/.microsandbox\n") {
		t.Errorf("notice suggests wholesale removal while a volume is uncopied:\n%s", notice)
	}
	if strings.Contains(notice, "rm -rf ~/.microsandbox/volumes/agents-sandbox-home-*") {
		t.Errorf("notice suggests a volume glob that would delete an uncopied volume:\n%s", notice)
	}
}

// assertOffersCopiedVolumeForRemoval fails when a notice lists a copied volume
// among the uncopied ones instead of offering its removal command.
func assertOffersCopiedVolumeForRemoval(t *testing.T, notice, copied string) {
	t.Helper()
	if strings.Contains(notice, "\n    "+copied+"\n") {
		t.Errorf("notice lists the copied volume %q as uncopied:\n%s", copied, notice)
	}
	if !strings.Contains(notice, "rm -rf ~/.microsandbox/volumes/"+copied+"\n") {
		t.Errorf("notice missing the removal command for the copied volume %q:\n%s", copied, notice)
	}
}

// warnedAbout reports whether any warning contains substring.
func warnedAbout(warnCalls []string, substring string) bool {
	for _, call := range warnCalls {
		if strings.Contains(call, substring) {
			return true
		}
	}
	return false
}

// TestAcknowledgeNoticeKeepsAnExistingEntry pins that recording the notice is
// idempotent: an entry written by a concurrent session is left untouched.
func TestAcknowledgeNoticeKeepsAnExistingEntry(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	if err := seedRootState(state.RootState{Migrations: map[string]state.Migration{
		LegacyRuntimeNoticeName: {Status: state.MigrationAcknowledged},
	}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := acknowledgeNotice(); err != nil {
		t.Fatalf("acknowledgeNotice() error = %v, want nil for an existing entry", err)
	}
}

// TestRenderNoticeWithoutArtifactsOmitsTheRemovableTier pins the early return
// when none of ours is present: no "safe to remove" tier is printed.
func TestRenderNoticeWithoutArtifactsOmitsTheRemovableTier(t *testing.T) {
	out := renderNotice(legacyArtifacts{}, nil)
	if strings.Contains(out, "safe to remove") {
		t.Errorf("notice mentions a removable tier with no artifacts present:\n%s", out)
	}
}

// TestRenderNoticeNamesLeftoversAsFromEarlierVersions pins the wording that
// reported confusion in #285: the removable tier must read as leftovers of the
// old runtime, not as files agents-sandbox just created, and every removal
// line is a command rather than a path list followed by "these".
func TestRenderNoticeNamesLeftoversAsFromEarlierVersions(t *testing.T) {
	out := renderNotice(legacyArtifacts{
		HomeVolumes: []string{naming.LegacyHomePrefix + "a"},
		Sandboxes:   []string{naming.LegacyVMPrefix + "p"},
	}, nil)
	if !strings.Contains(out, "Leftovers from earlier agents-sandbox versions, safe to remove:") {
		t.Errorf("notice does not name the removable tier as old leftovers:\n%s", out)
	}
	for _, confusing := range []string{"Ours", "Remove just these"} {
		if strings.Contains(out, confusing) {
			t.Errorf("notice still contains the confusing %q:\n%s", confusing, out)
		}
	}
	if strings.Contains(out, "\n    ~/.microsandbox/") {
		t.Errorf("notice lists bare paths instead of removal commands:\n%s", out)
	}
}

// TestRenderNoticeUncopiedOffersCopiedVolumesIndividually pins that a copied
// volume next to an uncopied one still gets its own removal command, while the
// uncopied one gets none.
func TestRenderNoticeUncopiedOffersCopiedVolumesIndividually(t *testing.T) {
	copied := naming.LegacyHomePrefix + "copied"
	uncopied := naming.LegacyHomePrefix + "uncopied"
	out := renderNotice(legacyArtifacts{HomeVolumes: []string{copied, uncopied}}, []string{uncopied})
	if !strings.Contains(out, "    rm -rf ~/.microsandbox/volumes/"+copied+"\n") {
		t.Errorf("notice missing the removal command for the copied volume:\n%s", out)
	}
	if strings.Contains(out, "rm -rf ~/.microsandbox/volumes/"+uncopied) {
		t.Errorf("notice offers to remove the uncopied volume:\n%s", out)
	}
}

// TestRenderNoticeAllUncopiedOmitsTheRemovableTier pins the "Start fresh" case
// from #285: every legacy volume is uncopied and no sandbox is left, so there is
// nothing safe to remove and no removable tier is printed.
func TestRenderNoticeAllUncopiedOmitsTheRemovableTier(t *testing.T) {
	volume := naming.LegacyHomePrefix + "a"
	out := renderNotice(legacyArtifacts{HomeVolumes: []string{volume}}, []string{volume})
	if strings.Contains(out, "safe to remove") {
		t.Errorf("notice prints a removable tier with nothing safe to remove:\n%s", out)
	}
}

// TestRenderNoticeQuotesUnsafeVolumeNames pins that a copied volume whose name
// holds shell metacharacters is quoted, so pasting the command removes only
// that directory.
func TestRenderNoticeQuotesUnsafeVolumeNames(t *testing.T) {
	unsafe := naming.LegacyHomePrefix + "a b;c'd"
	uncopied := naming.LegacyHomePrefix + "uncopied"
	out := renderNotice(legacyArtifacts{HomeVolumes: []string{unsafe, uncopied}}, []string{uncopied})
	want := `rm -rf ~/.microsandbox/volumes/'` + naming.LegacyHomePrefix + `a b;c'\''d'`
	if !strings.Contains(out, want+"\n") {
		t.Errorf("notice = \n%s\nwant the quoted command %q", out, want)
	}
}

// TestRenderNoticeOffersNoCommandForANameSanitizingChanges pins that a copied
// volume whose name holds a control character gets no removal command: printed
// raw it would emit a terminal escape, and its sanitized form names a different
// directory, possibly an uncopied volume.
func TestRenderNoticeOffersNoCommandForANameSanitizingChanges(t *testing.T) {
	uncopied := naming.LegacyHomePrefix + "a"
	copiedWithEscape := uncopied + "\x1b"
	out := renderNotice(legacyArtifacts{HomeVolumes: []string{copiedWithEscape, uncopied}}, []string{uncopied})
	if strings.Contains(out, "rm -rf ~/.microsandbox/volumes/") {
		t.Errorf("notice offers a volume removal command for a name sanitizing changes:\n%q", out)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("notice leaked an escape byte from a volume name:\n%q", out)
	}
}

// TestRenderNoticeStartFreshLayout pins the full notice for the case reported
// in #285: "Start fresh" left every legacy volume uncopied next to leftover
// sandboxes.
func TestRenderNoticeStartFreshLayout(t *testing.T) {
	volume := naming.LegacyHomePrefix + "a"
	out := renderNotice(legacyArtifacts{
		HomeVolumes: []string{volume},
		Sandboxes:   []string{naming.LegacyVMPrefix + "p"},
	}, []string{volume})
	want := `agents-sandbox now runs its own microsandbox runtime

Nothing broke — agents-sandbox keeps its runtime in its own private home now,
so the system-wide microsandbox is no longer needed to run it.

Your ~/.microsandbox was left untouched. It still holds what earlier
agents-sandbox versions stored there:

  These old home volumes were not copied into agents-sandbox and still hold data:
    agents-sandbox-home-a
  They were left in place; do not remove them.

  Leftovers from earlier agents-sandbox versions, safe to remove:
    rm -rf ~/.microsandbox/sandboxes/agents-sandbox-vm-*
    rm -rf ~/.microsandbox/sandboxes/agents-sandbox-task-*

Nothing was deleted for you.`
	if out != want {
		t.Errorf("notice =\n%s\nwant\n%s", out, want)
	}
}
