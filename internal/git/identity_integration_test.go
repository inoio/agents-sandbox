//go:build integration

package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestResolveIdentityWorktreeSharesCheckoutSlug(t *testing.T) {
	const origin = "https://example.com/acme/widget.git"
	repo := mustResolvePath(t, testutil.InitRepo(t))
	testutil.RunGit(t, repo, "remote", "add", "origin", origin)
	main := mustResolveIdentity(t, repo)

	nested := filepath.Join(repo, "nested", "wt")
	require.NoError(t, os.MkdirAll(filepath.Dir(nested), 0o755))
	testutil.RunGit(t, repo, "worktree", "add", nested)
	got := mustResolveIdentity(t, nested)

	require.Equal(t, main.RepoSlug, got.RepoSlug)
	require.Equal(t, main.CheckoutSlug, got.CheckoutSlug)
	require.Equal(t, repo, got.BindRoot)
	require.Equal(t, repo, got.CheckoutRoot)
	require.False(t, got.OutsideWorktree)
}

// TestResolveIdentityNoOriginWorktreeSharesCheckoutSlugs pins both slugs'
// derivation when there is no origin: folder and hash come from the checkout,
// not the worktree, so an inside-worktree shares its checkout's checkout slug
// and repo slug (#434).
func TestResolveIdentityNoOriginWorktreeSharesCheckoutSlugs(t *testing.T) {
	t.Run("plain checkout", func(t *testing.T) {
		repo := mustResolvePath(t, testutil.InitRepo(t))
		main := mustResolveIdentity(t, repo)

		nested := filepath.Join(repo, "nested", "wt")
		require.NoError(t, os.MkdirAll(filepath.Dir(nested), 0o755))
		testutil.RunGit(t, repo, "worktree", "add", nested)
		got := mustResolveIdentity(t, nested)

		require.Equal(t, main.CheckoutSlug, got.CheckoutSlug)
		require.False(t, got.OutsideWorktree)
		require.Equal(t, repo, got.BindRoot)
		require.Equal(t, repo, got.CheckoutRoot)
		require.Equal(t, main.RepoSlug, got.RepoSlug)
		require.Equal(t, projectSlug(sanitizeFolderName(filepath.Base(repo)), repo), got.RepoSlug)
	})

	t.Run("bare root", func(t *testing.T) {
		origin := testutil.InitRepo(t)
		root := mustResolvePath(t, t.TempDir())
		gitDir := filepath.Join(root, ".git")
		testutil.RunGit(t, root, "clone", "--bare", origin, ".git")
		testutil.RunGit(t, gitDir, "remote", "remove", "origin")
		testutil.RunGit(t, gitDir, "worktree", "add", "--detach", filepath.Join(root, "main"))

		main := mustResolveIdentity(t, root)
		got := mustResolveIdentity(t, filepath.Join(root, "main"))

		require.Equal(t, main.CheckoutSlug, got.CheckoutSlug)
		require.Equal(t, main.RepoSlug, got.RepoSlug)
		require.False(t, got.OutsideWorktree)
		require.Equal(t, root, got.BindRoot)
		require.Equal(t, root, got.CheckoutRoot)
	})
}

// TestResolveIdentityResolvesSymlinkedWorktreeGitDir pins that the recorded
// shared git dir is symlink-resolved before it is hashed: a linked worktree
// whose .git names the shared dir through a symlink must still share its
// checkout's slug.
func TestResolveIdentityResolvesSymlinkedWorktreeGitDir(t *testing.T) {
	const origin = "https://example.com/acme/widget.git"
	repo := mustResolvePath(t, testutil.InitRepo(t))
	testutil.RunGit(t, repo, "remote", "add", "origin", origin)
	main := mustResolveIdentity(t, repo)

	nested := filepath.Join(repo, "nested", "wt")
	require.NoError(t, os.MkdirAll(filepath.Dir(nested), 0o755))
	testutil.RunGit(t, repo, "worktree", "add", nested)

	link := filepath.Join(mustResolvePath(t, t.TempDir()), "repo-link")
	require.NoError(t, os.Symlink(repo, link))
	shared := filepath.Join(link, ".git", "worktrees", "wt")
	require.NoError(t, os.WriteFile(filepath.Join(nested, ".git"), []byte("gitdir: "+shared+"\n"), 0o644))

	got := mustResolveIdentity(t, nested)
	require.False(t, got.OutsideWorktree)
	require.Equal(t, main.CheckoutSlug, got.CheckoutSlug)
}

// TestResolveIdentityGitDirFileUnderWorktreesAncestorIsNotOutside pins the
// pre-refactor order: the recorded gitdir is classified and stripped while still
// relative, before it is joined to the worktree root. A "worktrees" component in
// the ancestor path must not turn a submodule or --separate-git-dir checkout into
// a linked worktree, which would bind an unrelated ancestor at /workspace.
func TestResolveIdentityGitDirFileUnderWorktreesAncestorIsNotOutside(t *testing.T) {
	t.Run("submodule", func(t *testing.T) {
		root := newSubmoduleUnderWorktreesAncestor(t)

		id := mustResolveIdentity(t, root)

		require.False(t, id.OutsideWorktree)
		require.Equal(t, root, id.CheckoutRoot)
		require.Equal(t, root, id.BindRoot)
	})

	t.Run("relative separate git dir", func(t *testing.T) {
		// git records --separate-git-dir as an absolute path, so the relative
		// form is hand-written to exercise the same strip-before-join order.
		root := filepath.Join(mustResolvePath(t, t.TempDir()), "worktrees", "proj", "sub")
		require.NoError(t, os.MkdirAll(filepath.Join(root, "..", "shared.git"), 0o755))
		writeGitdirFile(t, root, "../shared.git")

		id := mustResolveIdentity(t, root)

		require.False(t, id.OutsideWorktree)
		require.Equal(t, root, id.CheckoutRoot)
		require.Equal(t, root, id.BindRoot)
	})

	t.Run("absolute separate git dir", func(t *testing.T) {
		// An absolute gitdir carries its ancestors in the recorded text, so a
		// "worktrees" component there must not look like a worktree registration
		// (it would strip to the ancestor and bind the wrong root).
		root := filepath.Join(mustResolvePath(t, t.TempDir()), "proj")
		shared := filepath.Join(mustResolvePath(t, t.TempDir()), "worktrees", "proj", "shared.git")
		require.NoError(t, os.MkdirAll(shared, 0o755))
		writeGitdirFile(t, root, shared)

		id := mustResolveIdentity(t, root)

		require.False(t, id.OutsideWorktree)
		require.Equal(t, root, id.CheckoutRoot)
		require.Equal(t, root, id.BindRoot)
	})
}

// TestResolveIdentityWorktreeUnderWorktreesAncestor keeps the linked-worktree
// strip correct when the shared git dir itself sits under a "worktrees" path
// component: the registration is the last /worktrees/ segment, not the ancestor.
func TestResolveIdentityWorktreeUnderWorktreesAncestor(t *testing.T) {
	repo := newRepoUnderWorktreesAncestor(t)
	wt := filepath.Join(repo, "wt")
	testutil.RunGit(t, repo, "worktree", "add", wt)

	id := mustResolveIdentity(t, wt)

	require.False(t, id.OutsideWorktree)
	require.Equal(t, repo, id.CheckoutRoot)
	require.Equal(t, repo, id.BindRoot)
}

// TestResolveIdentitySeparateGitDirShapedLikeRegistrationIsNotOutside pins the
// discriminator: a --separate-git-dir checkout whose git dir is
// "<ancestor>/worktrees/<name>.git" matches a registration's shape but has no
// commondir marker, so it is a plain checkout and must not be stripped or bound
// to the ancestor.
func TestResolveIdentitySeparateGitDirShapedLikeRegistrationIsNotOutside(t *testing.T) {
	root, gitdir := newSeparateGitDirUnderWorktreesAncestor(t)

	require.Equal(t, gitdir, resolveGitDir(root))
	require.False(t, isLinkedWorktree(root))

	id := mustResolveIdentity(t, root)

	require.False(t, id.OutsideWorktree)
	require.Equal(t, root, id.CheckoutRoot)
	require.Equal(t, root, id.BindRoot)
}

func TestRepoSlugOutsideWorktree(t *testing.T) {
	const origin = "https://example.com/acme/widget.git"
	repo := mustResolvePath(t, testutil.InitRepo(t))
	testutil.RunGit(t, repo, "remote", "add", "origin", origin)
	outside := filepath.Join(mustResolvePath(t, t.TempDir()), "wt")
	testutil.RunGit(t, repo, "worktree", "add", outside)
	t.Chdir(outside)

	require.Equal(t, projectSlug(sanitizeFolderName("widget"), origin), MustRepoSlug())
}

// TestCheckoutSlugOutsideWorktreeOwnsItsIdentity pins the outside-worktree rule:
// such a worktree cannot share its checkout's VM (its /workspace bind can only be
// one of the two), so it is scoped as its own checkout by hashing its worktree
// root. It keeps the origin-derived repo slug, so it still shares the per-slug
// user config dir with its checkout.
func TestCheckoutSlugOutsideWorktreeOwnsItsIdentity(t *testing.T) {
	const origin = "https://example.com/acme/widget.git"
	repo := mustResolvePath(t, testutil.InitRepo(t))
	testutil.RunGit(t, repo, "remote", "add", "origin", origin)
	checkout := mustResolveIdentity(t, repo)

	base := mustResolvePath(t, t.TempDir())
	worktree := filepath.Join(base, "wt")
	sibling := filepath.Join(base, "sibling")
	testutil.RunGit(t, repo, "worktree", "add", worktree)
	testutil.RunGit(t, repo, "worktree", "add", sibling)
	t.Chdir(worktree)

	identity := mustResolveIdentity(t, worktree)
	// Built from the hash primitive directly, not from projectSlug: a bug in that
	// helper must not move the expectation along with the implementation.
	wantSlug := sanitizeFolderName("widget") + "-" + HashID(worktree)

	require.True(t, identity.OutsideWorktree)
	require.Equal(t, worktree, identity.BindRoot)
	require.Equal(t, wantSlug, identity.CheckoutSlug)
	require.Equal(t, sanitizeFolderName("widget")+"-"+HashID(origin), MustRepoSlug())
	require.Equal(t, wantSlug, MustCheckoutSlug())
	// Collision guard: an outside worktree must share neither its checkout's slug
	// (one VM, wrong /workspace) nor a sibling worktree's.
	require.NotEqual(t, checkout.CheckoutSlug, MustCheckoutSlug())
	require.NotEqual(t, mustResolveIdentity(t, sibling).CheckoutSlug, MustCheckoutSlug())
}

// TestCheckoutSlugOutsideWorktreeSubdirectoryUsesWorktreeRoot pins that the bind
// root does not follow the cwd: a run from inside the worktree is scoped to the
// worktree root (keeping the repository visible above the agent) and the slug
// hashes the worktree root, not the subdirectory.
func TestCheckoutSlugOutsideWorktreeSubdirectoryUsesWorktreeRoot(t *testing.T) {
	const origin = "https://example.com/acme/widget.git"
	repo := mustResolvePath(t, testutil.InitRepo(t))
	testutil.RunGit(t, repo, "remote", "add", "origin", origin)
	checkout := mustResolveIdentity(t, repo)

	worktree := filepath.Join(mustResolvePath(t, t.TempDir()), "wt")
	testutil.RunGit(t, repo, "worktree", "add", worktree)
	sub := filepath.Join(worktree, "sub", "dir")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	t.Chdir(sub)

	identity := mustResolveIdentity(t, sub)
	wantSlug := sanitizeFolderName("widget") + "-" + HashID(worktree)

	require.True(t, identity.OutsideWorktree)
	require.Equal(t, worktree, identity.BindRoot)
	require.Equal(t, wantSlug, MustCheckoutSlug())
	require.NotEqual(t, checkout.CheckoutSlug, MustCheckoutSlug())
}

// TestResolveIdentityBareRootRecordsLegacySlug pins the extra legacy identity a
// bare root carries. Before the identity split a bare repository (which has no
// worktree) fell back to a cwd-path-derived slug, while every worktree inside it
// kept the origin-derived one; the bare root must therefore adopt from both so a
// bare-root user's saved home and per-slug config are not stranded.
func TestResolveIdentityBareRootRecordsLegacySlug(t *testing.T) {
	origin := testutil.InitRepo(t)
	root := mustResolvePath(t, t.TempDir())
	testutil.RunGit(t, root, "clone", "--bare", origin, ".git")
	testutil.RunGit(t, filepath.Join(root, ".git"), "worktree", "add", filepath.Join(root, "main"))

	bare := mustResolveIdentity(t, root)
	legacy := projectSlug(sanitizeFolderName(filepath.Base(root)), root)

	require.Equal(t, []string{legacy}, bare.LegacyRepoSlugs)
	require.NotEqual(t, legacy, bare.RepoSlug)
	require.NotEqual(t, legacy, bare.CheckoutSlug)

	// A worktree inside the bare root has no extra legacy source: its pre-split
	// slug was already the origin-derived one.
	require.Empty(t, mustResolveIdentity(t, filepath.Join(root, "main")).LegacyRepoSlugs)
}

// TestResolveIdentityBareRootWithoutOriginHasNoExtraLegacySlug keeps the common
// no-origin case from double-counting: there the repo slug already is the
// path-derived one, so there is nothing extra to adopt.
func TestResolveIdentityBareRootWithoutOriginHasNoExtraLegacySlug(t *testing.T) {
	origin := testutil.InitRepo(t)
	root := mustResolvePath(t, t.TempDir())
	testutil.RunGit(t, root, "clone", "--bare", origin, ".git")
	testutil.RunGit(t, filepath.Join(root, ".git"), "remote", "remove", "origin")

	id := mustResolveIdentity(t, root)

	require.Empty(t, id.LegacyRepoSlugs)
}

// TestResolveIdentityNamedBareRootMatchesItsWorktrees pins #433: a bare
// repository cloned as "<name>.git" is a repository, not a non-repo folder, so
// its root shares the origin-derived repo slug and the checkout slug of the
// worktrees inside it, and still adopts the path-derived slug it was keyed
// under before.
func TestResolveIdentityNamedBareRootMatchesItsWorktrees(t *testing.T) {
	bare := newNamedBareClone(t, "widget.git")
	worktree := filepath.Join(bare, "main")
	testutil.RunGit(t, bare, "worktree", "add", worktree)

	got := mustResolveIdentity(t, bare)
	worktreeIdentity := mustResolveIdentity(t, worktree)

	require.True(t, got.Bare)
	require.False(t, got.OutsideWorktree)
	require.Equal(t, bare, got.BindRoot)
	require.Equal(t, bare, got.CheckoutRoot)
	require.Equal(t, bare, got.WorktreeRoot)
	require.Equal(t, worktreeIdentity.RepoSlug, got.RepoSlug)
	require.Equal(t, worktreeIdentity.CheckoutSlug, got.CheckoutSlug)
	require.Equal(t, []string{nonRepoIdentity(bare).CheckoutSlug}, got.LegacyRepoSlugs)
}

func TestResolveIdentityNamedBareRootSubdirectoryMatchesRoot(t *testing.T) {
	bare := newNamedBareClone(t, "widget.git")

	got := mustResolveIdentity(t, filepath.Join(bare, "refs", "heads"))

	require.Equal(t, mustResolveIdentity(t, bare), got)
}

// TestResolveIdentityNamedBareRootWithoutOriginKeepsPathSlug: without an
// origin both slugs already are the path-derived ones the bare root was keyed
// under, so there is nothing extra to adopt.
func TestResolveIdentityNamedBareRootWithoutOriginKeepsPathSlug(t *testing.T) {
	bare := newNamedBareClone(t, "widget.git")
	testutil.RunGit(t, bare, "remote", "remove", "origin")

	got := mustResolveIdentity(t, bare)

	pathSlug := nonRepoIdentity(bare).CheckoutSlug
	require.True(t, got.Bare)
	require.Equal(t, pathSlug, got.RepoSlug)
	require.Equal(t, pathSlug, got.CheckoutSlug)
	require.Empty(t, got.LegacyRepoSlugs)
}

// TestResolveIdentityInsideDotGitBareDirBindsItsParent keeps the documented
// layout intact: a bare ".git" dir belongs to the project around it.
func TestResolveIdentityInsideDotGitBareDirBindsItsParent(t *testing.T) {
	origin := testutil.InitRepo(t)
	root := mustResolvePath(t, t.TempDir())
	testutil.RunGit(t, root, "clone", "--bare", origin, ".git")

	got := mustResolveIdentity(t, filepath.Join(root, ".git"))

	require.Equal(t, mustResolveIdentity(t, root), got)
}

// TestResolveIdentityNamedBareRootKeepsLegacySlugEqualToCheckoutSlug: when the
// bare dir's name matches its origin's, the old path slug equals the checkout
// slug, so no state moves, but it must stay listed: the per-slug config is
// looked up through the repo slug and the legacy slugs only.
func TestResolveIdentityNamedBareRootKeepsLegacySlugEqualToCheckoutSlug(t *testing.T) {
	bare := newNamedBareClone(t, "widget")
	testutil.RunGit(t, bare, "remote", "set-url", "origin", "https://example.com/acme/widget.git")

	got := mustResolveIdentity(t, bare)

	pathSlug := nonRepoIdentity(bare).CheckoutSlug
	require.Equal(t, pathSlug, got.CheckoutSlug)
	require.NotEqual(t, pathSlug, got.RepoSlug)
	require.Equal(t, []string{pathSlug}, got.LegacyRepoSlugs)
}

// TestResolveIdentityOutsideWorktreeReportsSharedGitDir pins what the VM needs
// to mount for an outside worktree (#426): the resolved shared git dir as the
// host source, and the path the worktree's .git file records as the guest
// target, since that is the path git follows inside the VM.
func TestResolveIdentityOutsideWorktreeReportsSharedGitDir(t *testing.T) {
	repo := mustResolvePath(t, testutil.InitRepo(t))
	worktree := filepath.Join(mustResolvePath(t, t.TempDir()), "wt")
	// Pin the absolute form: an ambient worktree.useRelativePaths would record a
	// relative path instead (the case TestResolveIdentityOutsideWorktreeWithRelativeGitdir
	// covers), leaving the recorded path unresolvable as a guest mount target.
	testutil.RunGit(t, repo, "config", "worktree.useRelativePaths", "false")
	testutil.RunGit(t, repo, "worktree", "add", worktree)

	identity := mustResolveIdentity(t, worktree)

	require.True(t, identity.OutsideWorktree)
	require.Equal(t, filepath.Join(repo, ".git"), identity.SharedGitDir)
	require.Equal(t, filepath.Join(repo, ".git"), identity.RecordedSharedGitDir)
}

// TestResolveIdentityOutsideWorktreeKeepsRecordedSymlinkPath covers a .git file
// that names the shared git dir through a symlink: git inside the VM follows
// the recorded text, so the target keeps the symlink while the source resolves.
func TestResolveIdentityOutsideWorktreeKeepsRecordedSymlinkPath(t *testing.T) {
	repo := mustResolvePath(t, testutil.InitRepo(t))
	worktree := filepath.Join(mustResolvePath(t, t.TempDir()), "wt")
	testutil.RunGit(t, repo, "worktree", "add", worktree)
	link := filepath.Join(mustResolvePath(t, t.TempDir()), "repo-link")
	require.NoError(t, os.Symlink(repo, link))
	writeGitdirFile(t, worktree, filepath.Join(link, ".git", "worktrees", "wt"))

	identity := mustResolveIdentity(t, worktree)

	require.True(t, identity.OutsideWorktree)
	require.Equal(t, filepath.Join(repo, ".git"), identity.SharedGitDir)
	require.Equal(t, filepath.Join(link, ".git"), identity.RecordedSharedGitDir)
}

// TestResolveIdentityOutsideWorktreeWithRelativeGitdir covers
// worktree.useRelativePaths: the recorded path stays relative, so the caller
// can tell that no guest mount can make it resolve.
func TestResolveIdentityOutsideWorktreeWithRelativeGitdir(t *testing.T) {
	repo := mustResolvePath(t, testutil.InitRepo(t))
	worktree := filepath.Join(mustResolvePath(t, t.TempDir()), "wt")
	testutil.RunGit(t, repo, "worktree", "add", worktree)
	relativeSharedGitDir, err := filepath.Rel(worktree, filepath.Join(repo, ".git"))
	require.NoError(t, err)
	writeGitdirFile(t, worktree, filepath.Join(relativeSharedGitDir, "worktrees", "wt"))

	identity := mustResolveIdentity(t, worktree)

	require.True(t, identity.OutsideWorktree)
	require.Equal(t, filepath.Join(repo, ".git"), identity.SharedGitDir)
	require.Equal(t, relativeSharedGitDir, identity.RecordedSharedGitDir)
}

func TestResolveIdentityInsideWorktreeHasNoSharedGitDir(t *testing.T) {
	repo := mustResolvePath(t, testutil.InitRepo(t))
	worktree := filepath.Join(repo, "wt")
	testutil.RunGit(t, repo, "worktree", "add", worktree)

	for _, dir := range []string{repo, worktree} {
		identity := mustResolveIdentity(t, dir)
		require.Empty(t, identity.SharedGitDir, dir)
		require.Empty(t, identity.RecordedSharedGitDir, dir)
	}
}

func TestResolveIdentityNestedWorktree(t *testing.T) {
	repo := mustResolvePath(t, testutil.InitRepo(t))
	wt := filepath.Join(repo, "wt")
	testutil.RunGit(t, repo, "worktree", "add", wt)

	got := mustResolveIdentity(t, wt)

	if got.BindRoot != repo {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, repo)
	}
	if got.CheckoutRoot != repo {
		t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, repo)
	}
	if got.OutsideWorktree {
		t.Errorf("OutsideWorktree = true, want false")
	}
}

func TestResolveIdentityOutsideWorktree(t *testing.T) {
	repo := mustResolvePath(t, testutil.InitRepo(t))
	wt := filepath.Join(mustResolvePath(t, t.TempDir()), "wt")
	testutil.RunGit(t, repo, "worktree", "add", wt)

	got := mustResolveIdentity(t, wt)

	if got.BindRoot != wt {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, wt)
	}
	if got.CheckoutRoot != repo {
		t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, repo)
	}
	if !got.OutsideWorktree {
		t.Errorf("OutsideWorktree = false, want true")
	}
}

// TestResolveIdentityOutsideWorktreeOfBareGitDir builds a bare clone at
// tmp/bare.git and adds a worktree at tmp/wt, a sibling of the bare dir. The
// worktree is a linked worktree placed outside its checkout, so no containment
// is possible: it is reported as outside and scoped as its own checkout rather
// than sharing the bare dir's VM with a different /workspace bind.
func TestResolveIdentityOutsideWorktreeOfBareGitDir(t *testing.T) {
	origin := testutil.InitRepo(t)
	tmp := mustResolvePath(t, t.TempDir())
	bare := filepath.Join(tmp, "bare.git")
	testutil.RunGit(t, tmp, "clone", "--bare", origin, "bare.git")
	wt := filepath.Join(tmp, "wt")
	testutil.RunGit(t, bare, "worktree", "add", wt)

	got := mustResolveIdentity(t, wt)

	if !got.OutsideWorktree {
		t.Errorf("OutsideWorktree = false, want true")
	}
	if got.BindRoot != wt {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, wt)
	}
	if got.CheckoutRoot != bare {
		t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, bare)
	}
}

func TestResolveIdentityBareWorktree(t *testing.T) {
	origin := testutil.InitRepo(t)
	root := mustResolvePath(t, t.TempDir())
	testutil.RunGit(t, root, "clone", "--bare", origin, ".git")
	testutil.RunGit(t, filepath.Join(root, ".git"), "worktree", "add", filepath.Join(root, "main"))

	// The bare root and every worktree inside it share one checkout slug.
	rootIdentity := mustResolveIdentity(t, root)

	t.Run("bare root", func(t *testing.T) {
		got := mustResolveIdentity(t, root)
		if got.BindRoot != root {
			t.Errorf("BindRoot = %q, want %q", got.BindRoot, root)
		}
		if got.CheckoutRoot != root {
			t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, root)
		}
		if got.OutsideWorktree {
			t.Errorf("OutsideWorktree = true, want false")
		}
	})

	t.Run("worktree root", func(t *testing.T) {
		got := mustResolveIdentity(t, filepath.Join(root, "main"))
		if got.BindRoot != root {
			t.Errorf("BindRoot = %q, want %q", got.BindRoot, root)
		}
		if got.CheckoutRoot != root {
			t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, root)
		}
		if got.OutsideWorktree {
			t.Errorf("OutsideWorktree = true, want false")
		}
		if got.CheckoutSlug != rootIdentity.CheckoutSlug {
			t.Errorf("CheckoutSlug = %q, want the bare root's %q", got.CheckoutSlug, rootIdentity.CheckoutSlug)
		}
	})

	t.Run("worktree subdirectory", func(t *testing.T) {
		sub := filepath.Join(root, "main", "internal")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
		got := mustResolveIdentity(t, sub)
		if got.BindRoot != root {
			t.Errorf("BindRoot = %q, want %q", got.BindRoot, root)
		}
		if got.CheckoutRoot != root {
			t.Errorf("CheckoutRoot = %q, want %q", got.CheckoutRoot, root)
		}
		if got.OutsideWorktree {
			t.Errorf("OutsideWorktree = true, want false")
		}
	})
}

func TestResolveIdentityRelativeGitDir(t *testing.T) {
	origin := testutil.InitRepo(t)
	root := mustResolvePath(t, t.TempDir())
	testutil.RunGit(t, root, "clone", "--bare", origin, ".git")
	testutil.RunGit(t, filepath.Join(root, ".git"), "config", "worktree.useRelativePaths", "true")
	testutil.RunGit(t, filepath.Join(root, ".git"), "worktree", "add", filepath.Join(root, "main"))

	got := mustResolveIdentity(t, filepath.Join(root, "main"))

	if got.BindRoot != root {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, root)
	}
	if got.OutsideWorktree {
		t.Errorf("OutsideWorktree = true, want false")
	}
}

// TestResolveIdentityBareFromWorktreeConfig pins the resolver-level contract: a
// bare checkout whose core.bare lives only in config.worktree still resolves as
// bare, so project config is read from the bare root rather than the worktree.
func TestResolveIdentityBareFromWorktreeConfig(t *testing.T) {
	origin := testutil.InitRepo(t)
	root := mustResolvePath(t, t.TempDir())
	gitDir := filepath.Join(root, ".git")
	testutil.RunGit(t, root, "clone", "--bare", origin, ".git")
	testutil.RunGit(t, gitDir, "config", "worktree.useRelativePaths", "true")
	testutil.RunGit(t, gitDir, "worktree", "add", filepath.Join(root, "main"))

	// Reproduce the shape git leaves behind when extensions.worktreeConfig is
	// enabled: core.bare is gone from the shared config and lives in the main
	// worktree's config.worktree.
	if err := os.WriteFile(
		filepath.Join(gitDir, "config"),
		[]byte(worktreeConfigOn),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(gitDir, "config.worktree"),
		[]byte("[core]\n\tbare = true\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(config.worktree): %v", err)
	}

	got := mustResolveIdentity(t, filepath.Join(root, "main"))
	if !got.Bare {
		t.Errorf("ResolveIdentity(...).Bare = false, want true with core.bare in config.worktree")
	}
	if got.BindRoot != root {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, root)
	}
}

// newSeparateGitDirUnderWorktreesAncestor creates a real --separate-git-dir
// checkout whose git dir is "<base>/worktrees/<name>.git": the shape of a
// worktree registration but with no commondir marker. It returns the checkout
// root and the recorded git dir.
func newSeparateGitDirUnderWorktreesAncestor(t *testing.T) (string, string) {
	t.Helper()
	base := mustResolvePath(t, t.TempDir())
	root := filepath.Join(base, "checkout")
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(base, "worktrees"), 0o755))
	gitdir := filepath.Join(base, "worktrees", "proj.git")
	testutil.RunGit(t, root, "init", "--separate-git-dir="+gitdir)
	return root, gitdir
}

// newNamedBareClone clones a bare repository into a sibling "<name>.git"
// directory: the git dir is the directory itself, with no ".git" entry (#433).
func newNamedBareClone(t *testing.T, name string) string {
	t.Helper()
	origin := testutil.InitRepo(t)
	tmp := mustResolvePath(t, t.TempDir())
	testutil.RunGit(t, tmp, "clone", "--bare", origin, name)
	return filepath.Join(tmp, name)
}
