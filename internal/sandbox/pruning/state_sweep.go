package pruning

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// claimsDirName is the per-project claims directory under a slug's state dir,
// mirroring the state package's claims layout.
const claimsDirName = "claims"

// PruneStateArtifacts reclaims the safe-tier internal state under the state
// root: for every project slug it removes unheld claims and stale client
// leases, removes a state file that records no home volume, drops a now-empty
// claims dir, and removes the slug dir once it holds nothing. A state that
// records a home volume — settled or pending recovery — is always kept: the
// safe tier cannot prove the volume is gone, and removing the pointer would
// orphan a surviving volume. It never touches the locks tree, which lives
// outside the removable slug dirs by design.
//
// Every removal is guarded by the same locks the writers use. An error fails
// the sweep closed (a corrupt state is never discarded); Prune joins the error
// into its aggregate so the other phases still complete.
func PruneStateArtifacts(ctx context.Context, ui termio.UI) (StateSweepReport, error) {
	var report StateSweepReport
	slugs, err := state.ProjectSlugs()
	if err != nil {
		return report, err
	}
	for _, slug := range slugs {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		start := report
		if err := report.sweepSlug(slug); err != nil {
			return report, err
		}
		ui.Verbosef(
			"state sweep %s: %d claims, %d leases, %d state files removed, %d kept, %d dirs removed",
			slug,
			report.ClaimsRemoved-start.ClaimsRemoved,
			report.LeasesRemoved-start.LeasesRemoved,
			report.StatesRemoved-start.StatesRemoved,
			report.StaleStatesKept-start.StaleStatesKept,
			report.DirsRemoved-start.DirsRemoved,
		)
	}
	return report, nil
}

// sweepSlug reclaims everything the safe tier may remove for one slug: unheld
// claims, stale client leases, states recording no home volume, and finally the
// slug dir once it holds nothing.
func (r *StateSweepReport) sweepSlug(slug string) error {
	if err := r.sweepClaims(slug); err != nil {
		return err
	}
	if err := r.sweepStatesAndLeases(slug); err != nil {
		return err
	}
	if err := removeEmptyClaimsDir(slug); err != nil {
		return err
	}
	if err := state.RemoveSlugDirIfEmpty(slug); err != nil {
		return err
	}
	if _, err := os.Stat(state.SlugDir(slug)); os.IsNotExist(err) {
		r.DirsRemoved++
	}
	return nil
}

// sweepClaims removes every unheld claim token under a slug. A held claim is
// left in place by RemoveUnheldClaim's probe lock and counts as kept.
func (r *StateSweepReport) sweepClaims(slug string) error {
	tokens, err := state.ClaimTokens(slug)
	if err != nil {
		return err
	}
	for _, token := range tokens {
		removed, err := state.RemoveUnheldClaim(slug, token)
		if err != nil {
			return err
		}
		if removed {
			r.ClaimsRemoved++
		}
	}
	return nil
}

// sweepStatesAndLeases walks a slug's agent dirs, unlinking stale client leases
// and removing a state file that records no home volume. The claims directory
// is not an agent. Both the per-agent keys and the legacy agent-less
// <slug>/state.yaml are swept; a key with no state file is a no-op.
func (r *StateSweepReport) sweepStatesAndLeases(slug string) error {
	entries, err := os.ReadDir(state.SlugDir(slug))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read state dir for %s: %w", slug, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == claimsDirName {
			continue
		}
		key := state.Key{Slug: slug, Agent: entry.Name()}
		r.LeasesRemoved += state.SweepClientLeases(key)
		if err := r.sweepKeyState(key); err != nil {
			return err
		}
	}
	return r.sweepKeyState(state.Key{Slug: slug, Agent: ""})
}

// sweepKeyState removes a key's state file when the current document records no
// home volume and counts the outcome. The removal decision runs under the home
// lock inside RemoveHomeStateIf, so a concurrent writer that re-creates a
// volume-recording state between our pre-read and the lock keeps it; the
// post-removal read confirms the removal before it is counted. A missing state
// counts nothing; an unreadable one fails the sweep closed.
func (r *StateSweepReport) sweepKeyState(key state.Key) error {
	cur, err := state.ReadState(key)
	if err != nil {
		if errors.Is(err, state.ErrStateNotFound) {
			return nil
		}
		return err
	}
	if !stateHasNoHomeVolume(cur) {
		r.StaleStatesKept++
		return nil
	}
	if err := state.RemoveHomeStateIf(key, stateHasNoHomeVolume); err != nil {
		return err
	}
	if _, err := state.ReadState(key); errors.Is(err, state.ErrStateNotFound) {
		r.StatesRemoved++
	}
	return nil
}

// removeEmptyClaimsDir removes a slug's claims dir when it holds no claim
// tokens, so RemoveSlugDirIfEmpty can reclaim the slug dir once everything else
// is gone. RemoveUnheldClaim recreates the claims dir on every sweep (mirroring
// AcquireClaim's MkdirAll), and RemoveSlugDirIfEmpty treats "has entries"
// literally, so without this an empty claims dir would keep the slug dir
// forever. os.Remove has rmdir semantics: a claim landing between our check and
// the unlink fails the removal with ENOTEMPTY and the dir is left in place.
func removeEmptyClaimsDir(slug string) error {
	dir := filepath.Join(state.SlugDir(slug), claimsDirName)
	if err := os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTEMPTY) {
		return fmt.Errorf("remove empty claims dir %s: %w", dir, err)
	}
	return nil
}
