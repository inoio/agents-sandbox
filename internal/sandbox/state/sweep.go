package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/inoio/agents-sandbox/internal/hostlock"
)

// claimSuffix is the file-name suffix every claim token is stored under.
const claimSuffix = ".claim"

// mkdirSweepClaimDir and openSweepClaimFile are seams so a test can pin the
// claim dir and file modes without depending on the process umask.
var (
	mkdirSweepClaimDir = os.MkdirAll
	openSweepClaimFile = os.OpenFile
)

// ProjectSlugs returns the sorted directory names directly under the state
// root, one per project slug, excluding the never-deleted locks directory and
// any non-directory entry (the launcher-global root state file and its
// write-side temp files). A missing state root yields an empty list.
func ProjectSlugs() ([]string, error) {
	entries, err := os.ReadDir(stateRoot())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read state root %s: %w", stateRoot(), err)
	}
	slugs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Name() == filepath.Base(locksDir()) || !entry.IsDir() {
			continue
		}
		slugs = append(slugs, entry.Name())
	}
	return slugs, nil
}

// StateFileModTime returns the modification time of k's state file. A missing
// file returns ErrStateNotFound, so a caller can treat absence as "no state"
// distinctly from a stat failure.
func StateFileModTime(k Key) (time.Time, error) {
	path := stateFile(k)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return time.Time{}, ErrStateNotFound
		}
		return time.Time{}, fmt.Errorf("stat state file %s: %w", path, err)
	}
	return info.ModTime(), nil
}

// ClaimTokens returns the settled token names of the *.claim files under a
// slug's claims dir. Directories are skipped; a missing claims dir yields an
// empty list.
func ClaimTokens(slug string) ([]string, error) {
	dir := filepath.Join(slugDir(slug), "claims")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read claims dir %s: %w", dir, err)
	}
	tokens := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), claimSuffix) {
			continue
		}
		tokens = append(tokens, strings.TrimSuffix(entry.Name(), claimSuffix))
	}
	return tokens, nil
}

// RemoveUnheldClaim removes a claim token's file only when no live holder owns
// it, mirroring AcquireClaim's open-then-flock discipline: the file is opened
// with O_CREATE|O_RDWR and its flock is probed non-blocking. A held claim
// leaves the file in place and reports removed=false; a free claim is unlinked
// while the probe lock is held, so no concurrent acquirer can take the token
// between the decision and the removal. removed=true means the claim file is
// gone.
//
// The sameFile guard and retry loop are AcquireClaim's: a claim released
// between our open and our lock may leave the path naming a different,
// possibly held, claim, and removing it would split that holder's flock.
func RemoveUnheldClaim(slug, token string) (bool, error) {
	dir := filepath.Join(slugDir(slug), "claims")
	if err := mkdirSweepClaimDir(dir, 0o700); err != nil {
		return false, fmt.Errorf("create claim dir: %w", err)
	}
	path := filepath.Join(dir, token+claimSuffix)
	for range 5 {
		f, err := openSweepClaimFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return false, fmt.Errorf("open claim %s: %w", path, err)
		}
		if claimOpened != nil {
			claimOpened(f, path)
		}
		if err := hostlock.ExclusiveNonBlocking(f); err != nil {
			_ = f.Close()
			if hostlock.IsContention(err) {
				return false, nil
			}
			return false, fmt.Errorf("flock claim %s: %w", path, err)
		}
		if !sameFile(f, path) {
			// The token was released (and its file removed) between our open
			// and our lock; the path now names a different file, so retry on
			// the current one.
			_ = f.Close()
			continue
		}
		removeErr := os.Remove(path)
		_ = f.Close()
		if removeErr != nil && !os.IsNotExist(removeErr) {
			return false, fmt.Errorf("remove claim %s: %w", path, removeErr)
		}
		return true, nil
	}
	return false, nil
}

// SweepClientLeases unlinks free, non-fresh client leases for a key — a crashed
// client's leftovers — and returns how many it removed. Live (held) leases and
// fresh unlocked ones (a client between creating its file and taking its flock)
// are kept, the exact rule CountActiveClients applies; the walk is shared so
// the two cannot drift.
func SweepClientLeases(k Key) int {
	_, removed := sweepClientLeases(k)
	return removed
}

// RemoveSlugDirIfEmpty removes a slug's state directory when its whole subtree
// holds no files, reclaiming the empty child directories a sweep leaves behind
// (an emptied <agent>/clients/ lease dir and the <agent>/ state dir) so the slug
// is not rediscovered by project discovery. It descends bottom-up and keeps any
// directory that still has an entry — a file, a symlink, or a non-empty child.
// A missing directory is a no-op.
func RemoveSlugDirIfEmpty(slug string) error {
	_, err := removeEmptyTree(SlugDir(slug))
	return err
}

// removeEmptyTree removes path and every directory beneath it when the whole
// subtree holds no entries, deepest first, and reports whether it removed path.
// A directory that still contains anything is kept, as is a path that is not a
// directory.
func removeEmptyTree(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read state dir %s: %w", path, err)
	}
	empty := true
	for _, entry := range entries {
		if !entry.IsDir() {
			empty = false
			continue
		}
		removed, err := removeEmptyTree(filepath.Join(path, entry.Name()))
		if err != nil {
			return false, err
		}
		if !removed {
			empty = false
		}
	}
	if !empty {
		return false, nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("remove state dir %s: %w", path, err)
	}
	return true, nil
}
