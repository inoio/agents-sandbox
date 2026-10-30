package vm

import (
	"maps"
	"path/filepath"

	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// withSharedGitDirMount returns the configured mounts plus a read-write bind of
// an outside worktree's shared git dir (#426). Only the worktree is bound at
// /workspace, and its .git file points at the shared git dir outside that bind,
// so the shared git dir is mounted at the very path the .git file records.
//
// When no such mount can make git resolve — the recorded path is relative, or
// it collides with a managed guest mount — it warns and leaves the mounts
// unchanged. A configured mount at the same target wins.
func withSharedGitDirMount(configured mounts.Mounts, identity git.Identity, ui termio.UI) mounts.Mounts {
	if !identity.OutsideWorktree {
		return configured
	}
	recorded := identity.RecordedSharedGitDir
	if !filepath.IsAbs(recorded) {
		ui.Warnf(
			"worktree %q records its git dir as the relative path %q, which cannot resolve inside the VM; "+
				"git may not work there — run `git worktree repair` with worktree.useRelativePaths=false",
			identity.WorktreeRoot,
			recorded,
		)
		return configured
	}
	target, err := mounts.ResolveMountTarget(recorded)
	if err != nil {
		ui.Warnf(
			"cannot mount the shared git dir %q of worktree %q: %v; git may not resolve inside the VM",
			recorded,
			identity.WorktreeRoot,
			err,
		)
		return configured
	}
	if _, exists := configured[target]; exists {
		return configured
	}
	withGitDir := maps.Clone(configured)
	if withGitDir == nil {
		withGitDir = mounts.Mounts{}
	}
	withGitDir[target] = mounts.BindMount{Source: identity.SharedGitDir, Readonly: false}
	return withGitDir
}
