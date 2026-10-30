package vm

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func outsideWorktreeIdentity(recordedSharedGitDir string) git.Identity {
	return git.Identity{
		BindRoot:             "/srv/worktrees/wt",
		CheckoutRoot:         "/srv/repo",
		WorktreeRoot:         "/srv/worktrees/wt",
		OutsideWorktree:      true,
		SharedGitDir:         "/srv/repo/.git",
		RecordedSharedGitDir: recordedSharedGitDir,
		Bare:                 false,
		RepoSlug:             "repo-abc",
		CheckoutSlug:         "wt-def",
		LegacyRepoSlugs:      nil,
	}
}

func TestWithSharedGitDirMountLeavesNonOutsideIdentityAlone(t *testing.T) {
	ui := termio.NewTestMock(t)
	configured := mounts.Mounts{"/data": {Source: "/host/data", Readonly: true}}
	identity := outsideWorktreeIdentity("/srv/repo/.git")
	identity.OutsideWorktree = false

	got := withSharedGitDirMount(configured, identity, &ui)

	require.Equal(t, configured, got)
	require.Empty(t, ui.WarnCalls)
}

// TestWithSharedGitDirMountMountsAtRecordedPath pins the #426 fix: the shared
// git dir is bound read-write at the path the worktree's .git file records, so
// git inside the VM resolves the repository through /workspace/.git.
func TestWithSharedGitDirMountMountsAtRecordedPath(t *testing.T) {
	ui := termio.NewTestMock(t)
	configured := mounts.Mounts{"/data": {Source: "/host/data", Readonly: true}}

	got := withSharedGitDirMount(configured, outsideWorktreeIdentity("/srv/link/.git"), &ui)

	require.Equal(t, mounts.Mounts{
		"/data":          {Source: "/host/data", Readonly: true},
		"/srv/link/.git": {Source: "/srv/repo/.git", Readonly: false},
	}, got)
	require.Len(t, configured, 1, "the configured mounts must not be mutated")
	require.Empty(t, ui.WarnCalls)
}

func TestWithSharedGitDirMountWarnsOnRelativeGitdir(t *testing.T) {
	ui := termio.NewTestMock(t)
	configured := mounts.Mounts{"/data": {Source: "/host/data", Readonly: true}}

	got := withSharedGitDirMount(configured, outsideWorktreeIdentity("../repo/.git"), &ui)

	require.Equal(t, configured, got)
	require.Len(t, ui.WarnCalls, 1)
	require.Contains(t, ui.WarnCalls[0], "relative")
	require.Contains(t, ui.WarnCalls[0], "/srv/worktrees/wt")
}

func TestWithSharedGitDirMountWarnsOnManagedTargetConflict(t *testing.T) {
	ui := termio.NewTestMock(t)

	configured := mounts.Mounts{"/data": {Source: "/host/data", Readonly: true}}

	got := withSharedGitDirMount(configured, outsideWorktreeIdentity("/tmp/repo/.git"), &ui)

	require.Equal(t, configured, got)
	require.Len(t, ui.WarnCalls, 1)
	require.Contains(t, ui.WarnCalls[0], "/tmp/repo/.git")
	require.Contains(t, ui.WarnCalls[0], "git may not resolve")
}

func TestWithSharedGitDirMountKeepsConfiguredMountAtSameTarget(t *testing.T) {
	ui := termio.NewTestMock(t)
	configured := mounts.Mounts{"/srv/repo/.git": {Source: "/elsewhere", Readonly: true}}

	got := withSharedGitDirMount(configured, outsideWorktreeIdentity("/srv/repo/.git"), &ui)

	require.Equal(t, configured, got)
	require.Empty(t, ui.WarnCalls)
}

// TestWithSharedGitDirMountWithoutConfiguredMounts covers a run with no mounts
// configured: the nil map must still receive the shared git dir mount.
func TestWithSharedGitDirMountWithoutConfiguredMounts(t *testing.T) {
	ui := termio.NewTestMock(t)

	got := withSharedGitDirMount(nil, outsideWorktreeIdentity("/srv/repo/.git"), &ui)

	require.Equal(t, mounts.Mounts{"/srv/repo/.git": {Source: "/srv/repo/.git", Readonly: false}}, got)
}
