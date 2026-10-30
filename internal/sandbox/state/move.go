package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// SlugDir returns the state directory holding everything one project slug owns:
// the per-agent state files, the per-project claims, and the per-agent client
// leases. It is the agent-agnostic parent of KeyDir.
func SlugDir(slug string) string {
	return slugDir(slug)
}

// StatefulAgentNames returns the agent names that have a state file under slug,
// i.e. the agents whose state a slug-directory move will carry. It is how the
// identity adoption learns every agent it must tear down before moving the whole
// directory (#144): migrating one agent moves all of their state, so every moved
// agent's legacy VM has to be destroyed, not only the one the current command
// names. A missing slug directory, or a subdirectory without a state file, is
// not an agent and is skipped.
func StatefulAgentNames(slug string) ([]string, error) {
	dir := SlugDir(slug)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read state dir %s: %w", dir, err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, entry.Name(), "state.yaml")); err == nil {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}

// MoveSlugDir moves the entries of SlugDir(fromSlug) into SlugDir(toSlug) and
// returns the source-relative path of every entry it left behind. It is the
// state-moving half of the per-checkout identity adoption (#144).
//
// The move is a recursive merge that never overwrites an existing file. A
// destination name that is free is renamed; where both sides are directories the
// merge descends and merges them; anything else — a file, a dangling symlink, or
// a file/directory type mismatch — is left in place and reported. Every rename
// therefore targets a name that did not exist, and an existing destination
// directory keeps its own permissions and contents.
//
// Refusing more coarsely is not enough. The adoption trigger deliberately
// tolerates a stray lock file or clients/ directory an aborted run left behind,
// and the collision can be the <agent>/ directory itself; a one-level refusal
// would leave the legacy <agent>/state.yaml behind, re-fire the trigger on every
// run, and let the run proceed under the checkout identity with no state, a
// freshly provisioned home volume, and the agent's auth silently gone. Merging
// guarantees that whenever the trigger held, <agent>/state.yaml reaches the
// destination. A skipped entry is not an error; the caller warns and continues.
//
// A missing source directory is a no-op. Source directories the merge empties
// are removed best-effort. Both directories live under the one state root, so
// os.Rename cannot cross a filesystem and no copy fallback is needed.
func MoveSlugDir(fromSlug, toSlug string) ([]string, error) {
	if fromSlug == toSlug {
		return nil, nil
	}
	return mergeDir(SlugDir(fromSlug), SlugDir(toSlug), "")
}

// mergeDir moves every entry of srcDir into dstDir, recursing where both sides
// are directories, and removes srcDir once the merge has emptied it. rel is
// srcDir's path relative to the slug directory being moved, and prefixes every
// entry reported as skipped so the caller can name the file that stayed behind.
func mergeDir(srcDir, dstDir, rel string) ([]string, error) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read state dir %s: %w", srcDir, err)
	}

	var skipped []string
	for _, entry := range entries {
		name := entry.Name()
		srcPath := filepath.Join(srcDir, name)
		dstPath := filepath.Join(dstDir, name)
		childRel := filepath.Join(rel, name)

		dstInfo, statErr := os.Lstat(dstPath)
		switch {
		case statErr == nil && entry.IsDir() && dstInfo.IsDir():
			childSkipped, err := mergeDir(srcPath, dstPath, childRel)
			skipped = append(skipped, childSkipped...)
			if err != nil {
				return skipped, err
			}
		case statErr == nil:
			skipped = append(skipped, childRel)
		case !os.IsNotExist(statErr):
			return skipped, fmt.Errorf("stat %s: %w", dstPath, statErr)
		default:
			if err := os.MkdirAll(dstDir, 0o700); err != nil {
				return skipped, fmt.Errorf("create state dir %s: %w", dstDir, err)
			}
			if err := os.Rename(srcPath, dstPath); err != nil {
				return skipped, fmt.Errorf("move %s to %s: %w", srcPath, dstPath, err)
			}
		}
	}

	if err := os.Remove(srcDir); err != nil && !os.IsNotExist(err) && !errors.Is(err, syscall.ENOTEMPTY) {
		return skipped, fmt.Errorf("remove state dir %s: %w", srcDir, err)
	}
	return skipped, nil
}
