// Package runtimemigration derives and, in later stages, performs the opt-in
// copy of legacy microsandbox home volumes into the private runtime home.
package runtimemigration

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/runtime"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// legacyVolumeNames returns the basenames of agents-sandbox home volumes under
// <legacyHome>/volumes. A volumes directory that is missing, not a directory, or
// unreadable (permission denied) yields no names and no error: there is simply
// nothing usable to copy, so necessity is false rather than a failure. This is
// the pending-state tolerance; an open campaign uses the strict variant below.
func legacyVolumeNames(legacyHome string) ([]string, error) {
	return homeVolumeNames(legacyHome, true)
}

// campaignLegacyVolumeNames returns the legacy home volume names for an open
// copying campaign. Only a missing volumes directory counts as empty; a volumes
// directory that exists but cannot be listed (permission denied, not-a-directory,
// or an I/O error) is returned as an error, so the campaign fails closed (stays
// copying) instead of finalizing completed on an under-migrated home.
func campaignLegacyVolumeNames(legacyHome string) ([]string, error) {
	return homeVolumeNames(legacyHome, false)
}

// presentPrivateVolumeNames returns the basenames of agents-sandbox home volumes
// present under <privateHome>/volumes, with the same missing/unreadable
// tolerance as legacyVolumeNames. Presence alone is not completion: whether a
// present volume counts as copied is a status question the caller (see
// necessaryVolumes) answers from the migration registry, not from the volume.
func presentPrivateVolumeNames(privateHome string) ([]string, error) {
	return homeVolumeNames(privateHome, true)
}

// necessaryVolumes returns the legacy home volume names that still need copying,
// given the current migration status. The migration is the source of truth for
// what "necessary" means:
//   - completed/dismissed/acknowledged: the campaign is over; nothing is ever
//     necessary.
//   - copying: legacy names minus those already accounted for (Copied, Kept, or
//     Excluded). The private listing is not consulted, so a present volume not
//     recorded as accounted for is an unambiguous partial and stays necessary
//     (to be re-copied).
//   - pending (zero status): legacy names absent from the private home.
func necessaryVolumes(legacyHome, privateHome string, m state.Migration) ([]string, error) {
	switch m.Status {
	// The acknowledged case is defensive only: acknowledged is written solely to
	// the legacy_runtime_notice entry and never to runtime_home.
	case state.MigrationCompleted, state.MigrationDismissed, state.MigrationAcknowledged:
		return nil, nil
	case state.MigrationCopying:
		return copyingNecessaryVolumes(legacyHome, m)
	default:
		return pendingNecessaryVolumes(legacyHome, privateHome)
	}
}

// unclearVolumes returns the present private volumes under a legacy target name
// that the migration has not accounted for: neither copied (fully migrated), kept
// (dismissed at the user's choice), nor excluded (dropped by a failure). Such a
// volume is either a partial from an interrupted copy or, under the pending
// status, a copy whose registry entry was lost — in both cases its contents
// cannot be trusted as a complete home.
func unclearVolumes(m state.Migration, presentTargets []string) []string {
	accounted := accountedSet(m)
	var unclear []string
	for _, name := range presentTargets {
		if !accounted[name] {
			unclear = append(unclear, name)
		}
	}
	return unclear
}

// accountedSet returns the legacy names the migration has already resolved, in
// any of the three disjoint outcomes.
func accountedSet(m state.Migration) map[string]bool {
	accounted := make(map[string]bool, len(m.Copied)+len(m.Kept)+len(m.Excluded))
	for _, names := range [][]string{m.Copied, m.Kept, m.Excluded} {
		for _, name := range names {
			accounted[name] = true
		}
	}
	return accounted
}

// pendingNecessaryVolumes returns the legacy volume names absent from the
// private home. No in-flight progress is recorded yet, so presence is the only
// signal.
func pendingNecessaryVolumes(legacyHome, privateHome string) ([]string, error) {
	legacy, err := legacyVolumeNames(legacyHome)
	if err != nil {
		return nil, err
	}
	private, err := presentPrivateVolumeNames(privateHome)
	if err != nil {
		return nil, err
	}

	present := make(map[string]bool, len(private))
	for _, name := range private {
		present[name] = true
	}

	necessary := make([]string, 0, len(legacy))
	for _, name := range legacy {
		if !present[name] {
			necessary = append(necessary, name)
		}
	}
	return necessary, nil
}

// copyingNecessaryVolumes returns the legacy volume names not yet accounted for
// by an open campaign: those neither Copied, Kept, nor Excluded. It deliberately
// ignores the private listing, because during a campaign a present-but-unrecorded
// private volume is a partial that must be re-copied.
func copyingNecessaryVolumes(legacyHome string, m state.Migration) ([]string, error) {
	legacy, err := campaignLegacyVolumeNames(legacyHome)
	if err != nil {
		return nil, err
	}

	accounted := accountedSet(m)

	necessary := make([]string, 0, len(legacy))
	for _, name := range legacy {
		if !accounted[name] {
			necessary = append(necessary, name)
		}
	}
	return necessary, nil
}

// presentLegacyTargets returns the legacy home volume names already present in
// the private home. Presence under the pending status means a target was already
// restored, so a campaign starting now records these as Kept (unverified, left in
// place) rather than discarding and re-copying a home the user already has.
func presentLegacyTargets(legacyHome, privateHome string) ([]string, error) {
	targets, err := campaignLegacyVolumeNames(legacyHome)
	if err != nil {
		return nil, err
	}
	present, err := presentPrivateVolumeNames(privateHome)
	if err != nil {
		return nil, err
	}

	presentSet := make(map[string]bool, len(present))
	for _, name := range present {
		presentSet[name] = true
	}

	var restored []string
	for _, name := range targets {
		if presentSet[name] {
			restored = append(restored, name)
		}
	}
	return restored, nil
}

// necessaryVolumesFor resolves both homes and delegates to necessaryVolumes.
func necessaryVolumesFor(m state.Migration) ([]string, error) {
	legacyHome, err := runtime.LegacyHome()
	if err != nil {
		return nil, err
	}
	return necessaryVolumes(legacyHome, runtime.PrivateHome(), m)
}

// matchingDirNames lists the immediate subdirectories of dir whose name matches
// match. A missing directory is always empty. When tolerateUnreadable is true an
// existing-but-unlistable directory (not-a-directory or permission denied) is
// also empty; when false only the missing case is empty and any other read
// failure is returned, so an open campaign fails closed.
func matchingDirNames(dir string, match func(string) bool, tolerateUnreadable bool) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		if tolerateUnreadable && (errors.Is(err, syscall.ENOTDIR) || errors.Is(err, fs.ErrPermission)) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if name := entry.Name(); match(name) {
			names = append(names, name)
		}
	}
	return names, nil
}

// homeVolumeNames lists the agents-sandbox home volumes under <home>/volumes. A
// missing volumes directory is always empty. When tolerateUnreadable is true an
// existing-but-unlistable directory (not-a-directory or permission denied) is
// also empty; when false only the missing case is empty and any other read
// failure is returned, so an open campaign fails closed.
func homeVolumeNames(home string, tolerateUnreadable bool) ([]string, error) {
	return matchingDirNames(filepath.Join(home, "volumes"), naming.IsLegacyHomeVolume, tolerateUnreadable)
}

// uncopiedLegacyVolumes returns the legacy home volume names that still hold the
// only trustworthy copy: those with no counterpart in the private home and those
// present under a legacy target name but not recorded as fully copied. A legacy
// volume is safe to remove only when the campaign recorded it Copied and its
// destination still exists in the private home; presence alone is not proof,
// because a Kept volume is present yet unverified (an unclear copy the user chose
// to leave untouched). Rendered from the two listings and the migration record,
// it survives the dismiss path and later sessions, where the campaign's transient
// Kept/Excluded sets have been dropped.
func uncopiedLegacyVolumes(m state.Migration, legacyVolumes []string) ([]string, error) {
	present, err := presentPrivateVolumeNames(runtime.PrivateHome())
	if err != nil {
		return nil, err
	}
	presentSet := make(map[string]bool, len(present))
	for _, name := range present {
		presentSet[name] = true
	}
	copied := make(map[string]bool, len(m.Copied))
	for _, name := range m.Copied {
		copied[name] = true
	}
	var uncopied []string
	for _, name := range legacyVolumes {
		if presentSet[name] && copied[name] {
			continue
		}
		uncopied = append(uncopied, name)
	}
	return uncopied, nil
}

// legacySandboxNames lists launcher-owned sandbox records under
// <legacyHome>/sandboxes. Tolerance matches the pending-state volume listing.
func legacySandboxNames(legacyHome string) ([]string, error) {
	return matchingDirNames(filepath.Join(legacyHome, "sandboxes"), naming.IsLegacyLauncherSandbox, true)
}

// legacyArtifacts are the launcher-owned leftovers in the legacy home.
// VolumesUnreadable records that <legacyHome>/volumes could not be listed: it is
// empty for firing detection (an unreadable directory counts as absent), but
// command selection must withhold destructive commands because unlisted volumes
// may still hold data.
type legacyArtifacts struct {
	HomeVolumes       []string
	Sandboxes         []string
	VolumesUnreadable bool
}

func (a legacyArtifacts) present() bool {
	return len(a.HomeVolumes) > 0 || len(a.Sandboxes) > 0
}

// detectLegacyArtifacts reports the launcher-owned leftovers under legacyHome.
func detectLegacyArtifacts(legacyHome string) (legacyArtifacts, error) {
	volumes, err := legacyVolumeNames(legacyHome)
	if err != nil {
		return legacyArtifacts{}, err
	}
	// A tolerant listing swallows an unreadable volumes directory as empty.
	// Detection may treat that as absent, but command selection may not: an
	// unlistable directory can still hold uncopied volumes. A strict listing
	// distinguishes "empty" from "could not read".
	volumesUnreadable := false
	if len(volumes) == 0 {
		if _, strictErr := campaignLegacyVolumeNames(legacyHome); strictErr != nil {
			volumesUnreadable = true
		}
	}
	sandboxes, err := legacySandboxNames(legacyHome)
	if err != nil {
		return legacyArtifacts{}, err
	}
	return legacyArtifacts{HomeVolumes: volumes, Sandboxes: sandboxes, VolumesUnreadable: volumesUnreadable}, nil
}

// detectLegacyArtifactsFor resolves the legacy home and delegates.
func detectLegacyArtifactsFor() (legacyArtifacts, error) {
	legacyHome, err := runtime.LegacyHome()
	if err != nil {
		return legacyArtifacts{}, err
	}
	return detectLegacyArtifacts(legacyHome)
}
