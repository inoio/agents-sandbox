package channel

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/inoio/agents-sandbox/internal/atomicwrite"
	"github.com/inoio/agents-sandbox/internal/configpaths"
)

// learnStateDirName is the directory under the user state dir that holds the
// learn-mode artefacts, one per checkout.
const learnStateDirName = "learn"

// artefactDirMode is the mode of the directory tree the artefacts live in: the
// captures are the user's private state, so nothing outside the owner reads it.
const artefactDirMode = 0o700

// Save writes the scrubbed artefact for one session under
// <UserStateDir>/learn/<checkoutSlug>/<sessionStartUnix>.json (mode 0600) and
// returns its path. Only the value-free Observation is written; no raw capture
// is ever persisted, so a header value, request body, or query can never reach
// disk through this call (hub invariant 3).
//
// The write is atomic and replaces any artefact a previous save of the same
// session left at that path. A user state dir that is not absolute is refused,
// so a capture is never written relative to the process's working directory.
//
//deadcode:ignore reason="the artefact store's write path; S7 persists the observer's artefact after teardown"
func Save(checkoutSlug string, startedUnix int64, obs Observation) (string, error) {
	stateDir := configpaths.Get().UserStateDir()
	if !filepath.IsAbs(stateDir) {
		return "", fmt.Errorf("learn artefact state dir %q is not absolute", stateDir)
	}
	dir := filepath.Join(stateDir, learnStateDirName, checkoutSlug)
	if err := os.MkdirAll(dir, artefactDirMode); err != nil {
		return "", fmt.Errorf("create learn artefact directory: %w", err)
	}

	data, err := json.Marshal(obs)
	if err != nil {
		return "", fmt.Errorf("encode observation: %w", err)
	}

	path := filepath.Join(dir, strconv.FormatInt(startedUnix, 10)+".json")
	if err := atomicwrite.WriteFileMode(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write observation: %w", err)
	}
	return path, nil
}

// Load reads a saved artefact back. A missing or malformed file returns an
// error, so a caller cannot mistake an absent capture for an empty session.
//
//deadcode:ignore reason="the artefact store's read path; S6's after-run analyser loads the artefact"
func Load(path string) (Observation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Observation{}, fmt.Errorf("read observation: %w", err)
	}

	var obs Observation
	if err := json.Unmarshal(data, &obs); err != nil {
		return Observation{}, fmt.Errorf("decode observation: %w", err)
	}
	return obs, nil
}

// Prune removes every artefact under <UserStateDir>/learn/ whose modification
// time is before cutoff, and the slug directory once its last artefact is gone,
// returning the number of artefacts removed. A learn tree that does not exist
// yet is not an error: nothing to prune is the same outcome as having pruned
// everything. The after-run deletes its own artefact by default; this sweep
// catches what a crashed or abandoned run left behind.
//
//deadcode:ignore reason="the artefact store's sweep; the learn-mode cleanup path (S6/S7) prunes expired artefacts"
func Prune(cutoff time.Time) (int, error) {
	stateDir := configpaths.Get().UserStateDir()
	if !filepath.IsAbs(stateDir) {
		return 0, fmt.Errorf("learn artefact state dir %q is not absolute", stateDir)
	}
	learnDir := filepath.Join(stateDir, learnStateDirName)

	slugDirs, err := os.ReadDir(learnDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("read learn directory: %w", err)
	}

	removed := 0
	for _, slugDir := range slugDirs {
		if !slugDir.IsDir() {
			continue
		}
		dir := filepath.Join(learnDir, slugDir.Name())
		count, err := pruneSlugDir(dir, cutoff)
		if err != nil {
			return removed, err
		}
		removed += count
		removeEmptyDir(dir)
	}
	return removed, nil
}

// pruneSlugDir removes the artefact files in one slug directory modified before
// cutoff and returns how many it removed.
//
//deadcode:ignore reason="reached through Prune, whose production caller is the learn-mode cleanup path (S6/S7)"
func pruneSlugDir(dir string, cutoff time.Time) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("read slug directory: %w", err)
	}

	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			// gomutants:disable-next-line RETURN_ZERO reason="a stat failure aborts the directory on its first entry, so removed is zero here for every reachable input"
			return removed, fmt.Errorf("stat artefact: %w", err)
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			// gomutants:disable-next-line RETURN_ZERO reason="a remove failure aborts the directory on its first entry, so removed is zero here for every reachable input"
			return removed, fmt.Errorf("remove artefact: %w", err)
		}
		removed++
	}
	return removed, nil
}

// removeEmptyDir drops a slug directory once its last artefact is gone, so a
// completed sweep leaves no empty tree behind. The directory may hold a file a
// concurrent run is writing, in which case the removal fails and the tree stays;
// that is not an error worth reporting to the sweep's caller.
//
//deadcode:ignore reason="reached through Prune, whose production caller is the learn-mode cleanup path (S6/S7)"
func removeEmptyDir(dir string) {
	// ENOTEMPTY (a concurrent save) and EACCES both mean "leave it alone".
	_ = os.Remove(dir)
}
