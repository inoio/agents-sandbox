package runtimemigration

import (
	"context"
	"errors"
	"fmt"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/runtime"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// Offer choices. "copy" copies every necessary legacy volume at once; "fresh"
// records a permanent opt-out — labelled "Start fresh" when there is nothing to
// clean up, or "Dismiss" when unclear private volumes must be removed; "quit"
// aborts the session without a decision, so the offer returns next time. There
// is deliberately no "decide later": the session cannot proceed while the
// decision is unmade, because resolving the home volume would create a fresh one
// and overwrite the project's recorded reference before the old data could be
// copied.
const (
	choiceCopy  = "c"
	choiceFresh = "s"
	choiceQuit  = "q"
)

// MigrationName is the root-state migrations registry key for the one-time
// legacy home-volume copy.
const MigrationName = "runtime_home"

// ErrMigrationPending reports that the one-time legacy home-volume decision has
// not been made. A non-interactive session (including --yes) cannot make it, so
// it returns this error before touching the home volume rather than silently
// orphaning the legacy data. An interactive session returns it only when the
// user quits the prompt or the prompt fails.
var ErrMigrationPending = errors.New("legacy home-volume migration pending")

// Offer presents the one-time, opt-in copy of legacy microsandbox home volumes
// into the private runtime home, at the start of an interactive session.
//
// The offer is global: one acceptance copies every necessary volume. It is shown
// while the migration is undismissed and either has volumes to copy or has
// present private volumes of unclear status. The latter is surfaced so the user
// decides explicitly instead of the session silently adopting a possibly
// incomplete home. The copy option's wording follows the status: while a campaign
// is already copying it is a resume that discards and re-copies the interrupted
// partials it names; in the pending ambiguous state it copies only the missing
// volumes and leaves the present ones recordable as kept.
//
// A non-interactive session is never prompted and never copies: because
// proceeding would overwrite the project's recorded home-volume reference before
// the copy can happen, it returns ErrMigrationPending with guidance instead. A
// failure to read the persisted state is best-effort: it warns and skips rather
// than blocking the session. A failure to list the homes while a campaign is open
// is campaign-scoped: it explains the situation, then blocks (the user must fix
// it manually and run the migration again). A failed copy aborts the session
// (ErrMigrationPending): continuing would orphan the old data, while the campaign
// already removed each partial so necessity stays true and the offer returns next
// session.
func Offer(ctx context.Context, client msb.Client, ui termio.UI) error {
	rootState, err := state.ReadRootState()
	if err != nil {
		// The copy is a best-effort convenience: a corrupt or unreadable
		// launcher-global state file must not block the agent from running.
		ui.Warnf("skipping legacy home-volume copy offer: %v", err)
		return nil
	}
	migration, _ := rootState.Migration(MigrationName)
	if migration.Status == state.MigrationCompleted || migration.Status == state.MigrationDismissed {
		return showNotice(ui)
	}

	names, err := necessaryVolumesFor(migration)
	if err != nil {
		return handleOfferListingError(migration, ui, err)
	}
	presentTargets, err := presentLegacyTargetsFor()
	if err != nil {
		return handleOfferListingError(migration, ui, err)
	}
	unclear := unclearVolumes(migration, presentTargets)

	if len(names) == 0 && len(unclear) == 0 {
		if migration.Status == state.MigrationCopying {
			err = finalizeCompletedCampaign()
			if err != nil {
				return err
			}
		}
		return showNotice(ui)
	}

	if !ui.IsInteractive() {
		return fmt.Errorf(
			"%w: %d legacy microsandbox home volume(s) can be copied into agents-sandbox "+
				"and %d present private volume(s) have unclear status; "+
				"run an interactive `run`/`shell` session to decide, "+
				"or use `agents-sandbox volume remove` to clean up and dismiss",
			ErrMigrationPending, len(names), len(unclear),
		)
	}

	choice, err := ui.Select(
		offerPrompt(migration, len(names), len(unclear)),
		offerChoices(migration, len(unclear)),
		choiceQuit,
	)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrMigrationPending, err)
	}

	switch choice {
	case choiceCopy:
		if err := CopyVolumes(ctx, client, ui); err != nil {
			return fmt.Errorf("copying legacy home volumes: %w", err)
		}
	case choiceFresh:
		if err := dismissMigration(ctx, client); err != nil {
			return err
		}
	default:
		return ErrMigrationPending
	}
	return showNotice(ui)
}

// handleOfferListingError resolves a failure to list the homes while deriving the
// offer. While a campaign is open it is campaign-scoped: the session must not
// proceed (it would reuse an unfinished destination), so it explains the
// situation and blocks. In the pending state the offer is a best-effort
// convenience and warns and skips instead.
func handleOfferListingError(m state.Migration, ui termio.UI, err error) error {
	if m.Status != state.MigrationCopying {
		ui.Warnf("skipping legacy home-volume copy offer: %v", err)
		return nil
	}
	ui.Warnf(
		"the legacy home-volume copy is in progress but cannot continue: the legacy microsandbox home "+
			"cannot be read (%v). Fix this manually (for example, restore read access to "+
			"~/.microsandbox/volumes) and run the migration again.",
		err,
	)
	return fmt.Errorf("cannot resume legacy home-volume migration: %w", err)
}

// presentLegacyTargetsFor resolves both homes and returns the present private
// volumes under a legacy target name.
func presentLegacyTargetsFor() ([]string, error) {
	legacyHome, err := runtime.LegacyHome()
	if err != nil {
		return nil, err
	}
	return presentLegacyTargets(legacyHome, runtime.PrivateHome())
}

// finalizeCompletedCampaign closes out an open campaign that has nothing left to
// copy (it crashed after copying everything but before recording completion). It
// re-reads state under the root lock and only writes completed while the entry is
// still copying, so it never clobbers a concurrent terminal write.
func finalizeCompletedCampaign() error {
	_, err := state.WithRootState(true, func(tx *state.RootTxn) error {
		m, _ := tx.State().Migration(MigrationName)
		if m.Status != state.MigrationCopying {
			return nil
		}
		return finalizeMigration(tx, m)
	})
	return err
}

// dismissMigration records the permanent opt-out under the root lock, re-reading
// first so a concurrent campaign's terminal write (completed or dismissed) is not
// overwritten. It first removes every unclear private volume: recording the
// dismissal while a usable partial remains would let the home resolver adopt an
// incomplete home under a permanent opt-out. Volumes that were copied or kept at
// the user's choice are preserved. The Copied record is retained so the
// post-migration notice still knows which legacy volumes were fully migrated. A
// cleanup failure aborts the dismissal.
func dismissMigration(ctx context.Context, client msb.Client) error {
	_, err := state.WithRootState(true, func(tx *state.RootTxn) error {
		m, _ := tx.State().Migration(MigrationName)
		if m.Status == state.MigrationCompleted || m.Status == state.MigrationDismissed {
			return nil
		}
		if err := removeUnclearDestinations(ctx, client, m); err != nil {
			return err
		}
		return saveMigration(tx, state.Migration{Status: state.MigrationDismissed, Copied: m.Copied})
	})
	return err
}

// removeUnclearDestinations removes every present private volume under a legacy
// target name that is neither copied (fully migrated) nor kept (dismissed at the
// user's choice). Such a volume is an interrupted copy's partial — or, in the
// pending state, a copy whose registry entry was lost — and leaving it would let
// the home resolver adopt an incomplete home.
func removeUnclearDestinations(ctx context.Context, client msb.Client, m state.Migration) error {
	presentTargets, err := presentLegacyTargetsFor()
	if err != nil {
		return err
	}
	protected := make(map[string]bool, len(m.Copied)+len(m.Kept))
	for _, names := range [][]string{m.Copied, m.Kept} {
		for _, name := range names {
			protected[name] = true
		}
	}
	for _, name := range presentTargets {
		if protected[name] {
			continue
		}
		if err := removeVolume(ctx, client, name); err != nil {
			return err
		}
	}
	return nil
}

// offerPrompt builds the prompt text, appending the relevant notice when
// present private volumes of unclear status exist. The notice differs by status:
// while a campaign is copying, such a volume is an interrupted partial that
// resuming discards and re-copies, so the prompt says so. In the pending state it
// is a copy whose registry entry was lost, and the copy option leaves it in place
// (recorded as kept), so the prompt explains the ambiguous-state choice and the
// `volume remove --force` cleanup path.
func offerPrompt(m state.Migration, copyCount, unclearCount int) string {
	text := fmt.Sprintf("Copy %d legacy home volume(s) into agents-sandbox?", copyCount)
	if unclearCount == 0 {
		return text
	}
	if m.Status == state.MigrationCopying {
		return text + fmt.Sprintf(
			"\n\n%d private volume(s) were left unfinished by an interrupted copy; resuming discards and "+
				"re-copies them.",
			unclearCount,
		)
	}
	return text + fmt.Sprintf(
		"\n\n%d present private volume(s) have unclear status: a previous copy whose progress was lost, "+
			"so their contents cannot be trusted as complete. Copying leaves them untouched and dismisses them "+
			"from the migration. To migrate one fully, Quit, remove its private copy with "+
			"`agents-sandbox volume remove --force <name>`, and run again; keep the copies you do not want "+
			"migrated.",
		unclearCount,
	)
}

// offerChoices builds the menu. "Start fresh" is used only when the migration is
// pending and there is nothing unclear to clean up; otherwise the opt-out is
// worded "Dismiss" because it removes the unclear private volumes. The copy
// option is a resume while a campaign is already copying (present private
// volumes are interrupted partials that are discarded and re-copied); in the
// pending ambiguous state it copies only the missing volumes and keeps the
// present ones, and the label says so.
func offerChoices(m state.Migration, unclearCount int) []termio.Choice {
	copyLabel := "Copy the volumes"
	copyDescription := "carry your old sandbox home data into agents-sandbox"
	dismissLabel := "Start fresh"
	dismissDescription := "never offer again; leave the old volumes untouched"
	quitDescription := "abort this session; ask again next time"

	switch {
	case m.Status == state.MigrationCopying:
		copyLabel = "Resume the copy"
		copyDescription = "copy the remaining volumes"
		if unclearCount > 0 {
			copyDescription = "discard and re-copy the interrupted volumes, then copy the rest"
		}
	case unclearCount > 0:
		copyLabel = "Copy the missing volumes; dismiss the existing private volumes from the migration"
		copyDescription = "copy the absent volumes; the unclear private volumes are left untouched"
	}
	if m.Status != "" || unclearCount > 0 {
		dismissLabel = "Dismiss"
		dismissDescription = "never offer again; remove the unclear private volumes"
		quitDescription = "abort this session; remove the private copies you want re-migrated with " +
			"`agents-sandbox volume remove`, then run again"
	}

	return []termio.Choice{
		{Label: copyLabel, Key: choiceCopy, Description: copyDescription},
		{Label: dismissLabel, Key: choiceFresh, Description: dismissDescription},
		{Label: "Quit", Key: choiceQuit, Description: quitDescription},
	}
}
