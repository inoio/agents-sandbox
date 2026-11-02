package runtimemigration

import (
	"regexp"
	"slices"
	"strings"

	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// LegacyRuntimeNoticeName is the root-state migrations registry key for the
// one-time "system microsandbox is now optional" notice.
const LegacyRuntimeNoticeName = "legacy_runtime_notice"

// showNotice presents the post-migration notice once. It is best-effort: any
// failure warns and returns nil so the session proceeds. It never fires for a
// fresh install or a user's own unrelated microsandbox (detection is
// prefix-based), and never deletes anything. An interactive session renders the
// full notice, waits for the user, and records the acknowledgment; a
// non-interactive session prints only a bounded one-line form and records
// nothing, so the one-time moment is not silently consumed.
//
// It reads the persisted state itself rather than taking a snapshot from Offer:
// a campaign may have just reached its terminal word in this session, and a
// stale snapshot would miss the freshly recorded Copied volumes (over-warning).
// The uncopied volumes it warns about are derived from the two home listings and
// that record, so the warning survives the dismiss path and later sessions.
func showNotice(ui termio.UI) error {
	rootState, err := state.ReadRootState()
	if err != nil {
		ui.Warnf("skipping legacy runtime notice: %v", err)
		return nil
	}
	if _, acknowledged := rootState.Migration(LegacyRuntimeNoticeName); acknowledged {
		return nil
	}
	migration, _ := rootState.Migration(MigrationName)

	artifacts, err := detectLegacyArtifactsFor()
	if err != nil {
		ui.Warnf("skipping legacy runtime notice: %v", err)
		return nil
	}
	if !artifacts.present() {
		return nil
	}
	if !ui.IsInteractive() {
		_ = ui.Acknowledge(noticeSummaryLine())
		return nil
	}
	uncopied, err := uncopiedLegacyVolumes(migration, artifacts.HomeVolumes)
	if err != nil {
		// Conservative: without the private listing, treat every legacy volume as
		// uncopied so the notice never prints a command that could delete data.
		ui.Warnf("could not list the private home; assuming no legacy volumes were copied: %v", err)
		uncopied = artifacts.HomeVolumes
	}
	if err := ui.Acknowledge(renderNotice(artifacts, uncopied)); err != nil {
		ui.Warnf("could not show legacy runtime notice: %v", err)
		return nil
	}
	if err := acknowledgeNotice(); err != nil {
		ui.Warnf("could not record legacy runtime notice: %v", err)
	}
	return nil
}

// noticeSummaryLine is the bounded one-line form for a non-interactive session:
// the fact and where to read more, with no removal commands.
func noticeSummaryLine() string {
	return "agents-sandbox now runs its own microsandbox runtime; your system-wide microsandbox is optional. " +
		"See the 'Upgrading to 0.6' docs to clean up."
}

// acknowledgeNotice records the terminal acknowledgment under the root lock,
// re-reading first so a concurrent session's write is never clobbered.
func acknowledgeNotice() error {
	_, err := state.WithRootState(true, func(tx *state.RootTxn) error {
		if _, ok := tx.State().Migration(LegacyRuntimeNoticeName); ok {
			return nil
		}
		tx.State().SetMigration(LegacyRuntimeNoticeName, state.Migration{Status: state.MigrationAcknowledged})
		return tx.Commit()
	})
	return err
}

// renderNotice builds the notice text. Only the tiers actually present are
// shown. When uncopied volumes exist the notice leads with a "do not remove"
// warning naming them, and suppresses every command that would delete them: the
// wholesale rm -rf ~/.microsandbox and the selective
// ~/.microsandbox/volumes/agents-sandbox-home-* glob both match an uncopied
// volume. The safe sandbox globs are still offered, and the volumes glob is
// replaced by one command per copied volume. An unreadable
// legacy volumes directory is treated the same way: the wholesale command is
// withheld and replaced by a note to inspect it, because unlisted home volumes
// may still hold data.
func renderNotice(a legacyArtifacts, uncopied []string) string {
	var b strings.Builder
	b.WriteString("agents-sandbox now runs its own microsandbox runtime\n\n")
	b.WriteString("Nothing broke — agents-sandbox keeps its runtime in its own private home now,\n")
	b.WriteString("so the system-wide microsandbox is no longer needed to run it.\n\n")
	b.WriteString("Your ~/.microsandbox was left untouched. It still holds what earlier\n")
	b.WriteString("agents-sandbox versions stored there:\n\n")
	hasUncopied := len(uncopied) > 0
	if hasUncopied {
		writeUncopiedWarning(&b, uncopied)
	}
	writeRemovableLeftovers(&b, a, uncopied)
	switch {
	case hasUncopied:
		// The leading warning already covers it; no wholesale command.
	case a.VolumesUnreadable:
		b.WriteString("  We could not list ~/.microsandbox/volumes, so the volumes there were not\n")
		b.WriteString("  copied or listed. Inspect that directory; do not remove the folder wholesale.\n\n")
	default:
		b.WriteString("  Everything else is your own microsandbox data; we never touch it. Remove the\n")
		b.WriteString("  whole folder only if microsandbox was installed just for agents-sandbox:\n")
		b.WriteString("    rm -rf ~/.microsandbox\n\n")
	}
	b.WriteString("Nothing was deleted for you.")
	return b.String()
}

// writeUncopiedWarning writes the leading "do not remove" paragraph naming the
// volumes that were not copied into agents-sandbox, before any removal command.
// It reads true both for a failed copy and for a volume the user chose not to
// migrate.
func writeUncopiedWarning(b *strings.Builder, uncopied []string) {
	b.WriteString("  These old home volumes were not copied into agents-sandbox and still hold data:\n")
	for _, name := range uncopied {
		b.WriteString("    " + sanitizeNoticeName(name) + "\n")
	}
	b.WriteString("  They were left in place; do not remove them.\n\n")
}

// sanitizeNoticeName strips control characters — including newlines and the
// escape byte — from a volume name so a hostile or odd legacy-home directory
// name cannot forge notice lines or emit terminal escapes.
func sanitizeNoticeName(name string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
}

// writeRemovableLeftovers writes the tier of old-runtime leftovers that are safe
// to remove, as one removal command per line, if any are present.
func writeRemovableLeftovers(b *strings.Builder, a legacyArtifacts, uncopied []string) {
	commands := removalCommands(a, uncopied)
	if len(commands) == 0 {
		return
	}
	b.WriteString("  Leftovers from earlier agents-sandbox versions, safe to remove:\n")
	for _, command := range commands {
		b.WriteString("    " + command + "\n")
	}
	b.WriteString("\n")
}

// removalCommands returns the commands removing our old-runtime leftovers,
// never one that would delete an uncopied volume.
func removalCommands(a legacyArtifacts, uncopied []string) []string {
	commands := volumeRemovalCommands(a.HomeVolumes, uncopied)
	if len(a.Sandboxes) > 0 {
		commands = append(commands,
			"rm -rf ~/.microsandbox/sandboxes/agents-sandbox-vm-*",
			"rm -rf ~/.microsandbox/sandboxes/agents-sandbox-task-*",
		)
	}
	return commands
}

// volumeRemovalCommands returns one glob command when every legacy home volume
// was copied. Otherwise the glob would match an uncopied volume, so it returns
// one command per copied volume instead.
func volumeRemovalCommands(homeVolumes, uncopied []string) []string {
	if len(homeVolumes) == 0 {
		return nil
	}
	if len(uncopied) == 0 {
		return []string{"rm -rf ~/.microsandbox/volumes/agents-sandbox-home-*"}
	}
	var commands []string
	for _, name := range homeVolumes {
		if slices.Contains(uncopied, name) {
			continue
		}
		commands = append(commands, "rm -rf ~/.microsandbox/volumes/"+shellWord(sanitizeNoticeName(name)))
	}
	return commands
}

var shellSafeWord = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// shellWord returns name as a single POSIX shell word: unchanged when it holds
// only safe characters, single-quoted otherwise. Legacy volumes are matched by
// prefix only, so a name may hold shell metacharacters.
func shellWord(name string) string {
	if shellSafeWord.MatchString(name) {
		return name
	}
	return "'" + strings.ReplaceAll(name, "'", `'\''`) + "'"
}
