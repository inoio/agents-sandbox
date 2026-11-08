//go:build integration

package prepare

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestPrepareSandboxRecomputesConfigSlugAfterAdoption pins the P1 fix: a bare
// root's per-slug config dir is renamed onto the repo slug during
// resolve-identity, so a ConfigSlug resolved before adoption (the CLI boundary's
// legacy slug) is stale. The later readers must recompute it, or the user's env,
// secrets, home files and agent config are silently skipped on the migration
// run. The per-slug env file is the observable: it feeds the env fingerprint
// persisted on VM creation.
func TestPrepareSandboxRecomputesConfigSlugAfterAdoption(t *testing.T) {
	origin := testutil.InitRepo(t)
	root := t.TempDir()
	testutil.RunGit(t, root, "clone", "--bare", origin, ".git")
	testutil.RunGit(t, filepath.Join(root, ".git"), "worktree", "add", filepath.Join(root, "main"))

	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	configpaths.WithRealConfigPaths(t)
	t.Chdir(root)

	identity := mustIdentity(t, ".")
	if len(identity.LegacyRepoSlugs) == 0 {
		t.Fatal("bare root must record a legacy config slug")
	}
	// The CLI resolves the config slug before adoption, while only the legacy
	// dir holds inputs, so it passes the legacy slug into the run.
	legacySlug := identity.LegacyRepoSlugs[0]
	legacyDir := configpaths.Get().UserSlugConfigDir(legacySlug)
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("mkdir legacy config dir: %v", err)
	}
	testutil.WriteFile(t, legacyDir, configpaths.EnvFileName, "FOO=bar\n")

	installPrepareSandboxDockerMock(t)
	installPrepareSandboxMSBMock(t)

	// A legacy state file makes adoption pending: its state and config dir move
	// onto the checkout/repo identity before the config readers run.
	if err := seedHomeState(state.Key{Slug: legacySlug, Agent: "opencode"}, state.HomeState{
		HomeVolume:   "home-vol",
		ImageDigest:  "sha256:abc123",
		AgentVersion: "1.0.0",
		AgentSource:  state.AgentSourceTool,
	}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(
		context.Background(),
		sandboxRunOptions(t, options.RunOptions{Agent: "opencode", ConfigSlug: legacySlug}),
		false,
		&ui,
	)
	if err != nil {
		t.Fatalf("PrepareSandbox: %v", err)
	}
	defer sess.Cleanup()

	hs, err := state.ReadState(state.Key{Slug: identity.CheckoutSlug, Agent: "opencode"})
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if !slices.Contains(hs.EnvState.Entries, "FOO") {
		t.Errorf(
			"env fingerprint entries = %v, want FOO read from the adopted repo-slug config dir",
			hs.EnvState.Entries,
		)
	}
}

// TestPrepareSandboxOutsideWorktreeUsesItsOwnKey pins the design change: a
// worktree outside its checkout (this repo's own
// ~/.local/share/opencode/worktree/<n>/<name> layout) is not refused; it runs as
// its own checkout by hashing its worktree root, so its state key, VM name and
// image tag carry the worktree-root-derived slug rather than the checkout's.
func TestPrepareSandboxOutsideWorktreeUsesItsOwnKey(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	repo := testutil.InitRepo(t)
	testutil.RunGit(t, repo, "remote", "add", "origin", cloneOrigin)
	outside := filepath.Join(t.TempDir(), "wt")
	testutil.RunGit(t, repo, "worktree", "add", outside)
	recordSharedGitDirOutsideManagedMounts(t, repo, outside)

	identity := mustIdentity(t, outside)
	checkoutIdentity := mustIdentity(t, repo)
	if !identity.OutsideWorktree {
		t.Fatalf("expected %q to resolve as an outside worktree", outside)
	}
	if identity.CheckoutSlug == checkoutIdentity.CheckoutSlug {
		t.Fatalf("outside worktree shares its checkout's slug %q", identity.CheckoutSlug)
	}
	if identity.RepoSlug != checkoutIdentity.RepoSlug {
		t.Errorf("outside worktree repo slug = %q, want the shared %q",
			identity.RepoSlug, checkoutIdentity.RepoSlug)
	}

	inspectedTag := installPrepareSandboxDockerMock(t)
	ui := termio.NewTestMock(t)

	t.Chdir(outside)
	key := state.Key{Slug: identity.CheckoutSlug, Agent: "opencode"}
	seedMatchingHomeState(t, key)
	mock := installPrepareSandboxMSBMock(t)

	session, err := PrepareSandbox(
		context.Background(),
		sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
		false,
		&ui,
	)
	if err != nil {
		t.Fatalf("PrepareSandbox in an outside worktree: %v", err)
	}
	defer session.Cleanup()

	if got := session.Key(); got != key {
		t.Errorf("session key = %+v, want the worktree-root-derived %+v", got, key)
	}
	if got := createdProjectVMNames(mock); len(got) != 1 || got[0] != vm.ProjectVMName(key) {
		t.Errorf("created sandboxes = %v, want the worktree-root-derived %q",
			got, vm.ProjectVMName(key))
	}
	if *inspectedTag != naming.DockerImageRef(identity.CheckoutSlug, "opencode") {
		t.Errorf("image tag = %q, want the worktree-root-derived tag", *inspectedTag)
	}
	// The slug is only half the identity: /workspace must bind the worktree
	// root — not the checkout it shares a common dir with, and not the cwd.
	worktreeRoot, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", outside, err)
	}
	mountConfig := createdProjectVMConfig(t, mock, vm.ProjectVMName(key))
	if got := mountConfig.Volumes[mounts.WorkspaceMountPath].Bind; got != worktreeRoot {
		t.Errorf("%s mount source = %q, want the worktree root %q", mounts.WorkspaceMountPath, got, worktreeRoot)
	}
	assertSharedGitDirMountWired(t, identity, mountConfig.Volumes)
}

// recordSharedGitDirOutsideManagedMounts rewrites the worktree's .git file to
// reach the shared git dir through a symlink under /var/tmp. t.TempDir lies
// under the managed /tmp guest mount on Linux, where the shared git dir cannot
// be mounted, so without this the test could not observe the mount (#426).
func recordSharedGitDirOutsideManagedMounts(t *testing.T, repo, worktree string) {
	t.Helper()
	//nolint:usetesting // t.TempDir lies under the managed /tmp guest mount on Linux
	linkDir, err := os.MkdirTemp("/var/tmp", "agents-sandbox-test-")
	if err != nil {
		t.Skipf("no writable /var/tmp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(linkDir) })
	link := filepath.Join(linkDir, "repo")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatalf("symlink %q: %v", link, err)
	}
	gitdir := filepath.Join(link, ".git", "worktrees", filepath.Base(worktree))
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644); err != nil {
		t.Fatalf("rewrite %s/.git: %v", worktree, err)
	}
}

// assertSharedGitDirMountWired checks that PrepareSandbox binds an outside
// worktree's shared git dir at the path its .git file records (#426).
func assertSharedGitDirMountWired(t *testing.T, identity git.Identity, volumes map[string]msbSdk.MountConfig) {
	t.Helper()
	if got := volumes[identity.RecordedSharedGitDir].Bind; got != identity.SharedGitDir {
		t.Errorf("%s mount source = %q, want the shared git dir %q",
			identity.RecordedSharedGitDir, got, identity.SharedGitDir)
	}
}
