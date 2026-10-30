package git

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/testutil"
)

// mustResolvePath is the test-side wrapper for the now-fallible resolvePath:
// every fixture path exists, so an error is a broken test rather than an input.
func mustResolvePath(t testing.TB, path string) string {
	t.Helper()
	resolved, err := resolvePath(path)
	require.NoError(t, err)
	return resolved
}

// mustResolveIdentity is the test-side wrapper for the fallible ResolveIdentity:
// fixtures point at real paths, so a resolution error is a broken test.
func mustResolveIdentity(t testing.TB, cwd string) Identity {
	t.Helper()
	identity, err := ResolveIdentity(cwd)
	require.NoError(t, err)
	return identity
}

// newRepo builds a minimal repo layout in a fresh temp dir: a .git directory
// holding a config with the given origin remote (omitted when origin is empty).
// It returns the symlink-resolved path, so assertions compare against exactly
// what ResolveIdentity derives — on macOS the temp dir sits behind the
// /var -> /private/var symlink.
func newRepo(t *testing.T, origin string) string {
	t.Helper()
	root := mustResolvePath(t, t.TempDir())
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	if origin != "" {
		writeOriginConfig(t, root, origin)
	}
	return root
}

func writeOriginConfig(t *testing.T, root, origin string) {
	t.Helper()
	config := "[remote \"origin\"]\n\turl = " + origin + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, ".git", "config"), []byte(config), 0o644))
}

// writeGitdirFile writes dir/.git as a file whose gitdir points at gitdir: the
// shape git uses for a linked worktree (a worktrees/ target) and for a
// --separate-git-dir checkout or submodule (any other target).
func writeGitdirFile(t *testing.T, dir, gitdir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644))
}

func TestResolveIdentityRepoWithOrigin(t *testing.T) {
	root := newRepo(t, "https://example.com/acme/widget.git")

	id := mustResolveIdentity(t, root)

	// The repo slug is byte-identical to the pre-change value: the per-slug user
	// config dir must not move for existing users.
	wantRepo := projectSlug(sanitizeFolderName("widget"), "https://example.com/acme/widget.git")
	require.Equal(t, wantRepo, id.RepoSlug)
	require.Equal(t, projectSlug(sanitizeFolderName("widget"), resolveGitDir(root)), id.CheckoutSlug)
	require.False(t, id.OutsideWorktree)
	require.Equal(t, root, id.BindRoot)
	require.Equal(t, root, id.CheckoutRoot)
	// A non-bare checkout never records a legacy (path-derived) slug.
	require.Empty(t, id.LegacyRepoSlugs)
}

func TestResolveIdentityClonesShareRepoSlug(t *testing.T) {
	const origin = "https://example.com/acme/widget.git"

	first := mustResolveIdentity(t, newRepo(t, origin))
	second := mustResolveIdentity(t, newRepo(t, origin))

	require.Equal(t, first.RepoSlug, second.RepoSlug)
	require.NotEqual(t, first.CheckoutSlug, second.CheckoutSlug)
}

func TestResolveIdentityDifferentOriginsDiffer(t *testing.T) {
	first := mustResolveIdentity(t, newRepo(t, "https://example.com/acme/widget.git"))
	second := mustResolveIdentity(t, newRepo(t, "https://example.com/other/widget.git"))

	require.NotEqual(t, first.RepoSlug, second.RepoSlug)
}

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

func TestResolveIdentitySubdirectoryMatchesCheckoutRoot(t *testing.T) {
	root := newRepo(t, "https://example.com/acme/widget.git")
	main := mustResolveIdentity(t, root)

	sub := filepath.Join(root, "internal", "foo")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	got := mustResolveIdentity(t, sub)

	require.Equal(t, main.RepoSlug, got.RepoSlug)
	require.Equal(t, main.CheckoutSlug, got.CheckoutSlug)
	require.Equal(t, root, got.BindRoot)
	require.Equal(t, root, got.CheckoutRoot)
	require.False(t, got.OutsideWorktree)
}

// TestResolveIdentityIgnoresCwdForNonRepo pins the empty-worktree-root
// short-circuit: a cwd that happens to hold a ".git" entry must not leak into
// the identity of an unrelated directory that has none.
func TestResolveIdentityIgnoresCwdForNonRepo(t *testing.T) {
	cwdWithGit := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(cwdWithGit, ".git"), 0o755))
	t.Chdir(cwdWithGit)

	dir := mustResolvePath(t, t.TempDir())
	got := mustResolveIdentity(t, dir)
	want := projectSlug(sanitizeFolderName(filepath.Base(dir)), dir)
	require.Equal(t, want, got.RepoSlug)
	require.Equal(t, want, got.CheckoutSlug)
	require.Equal(t, dir, got.BindRoot)
	require.Empty(t, got.CheckoutRoot)
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

func TestResolveIdentityNonRepoDirectory(t *testing.T) {
	dir := mustResolvePath(t, t.TempDir())

	id := mustResolveIdentity(t, dir)

	want := projectSlug(sanitizeFolderName(filepath.Base(dir)), dir)
	require.Equal(t, want, id.RepoSlug)
	require.Equal(t, want, id.CheckoutSlug)
	require.False(t, id.OutsideWorktree)
	require.Equal(t, dir, id.BindRoot)
	require.Empty(t, id.CheckoutRoot)
}

func TestResolveIdentityResolvesSymlinks(t *testing.T) {
	physical := newRepo(t, "https://example.com/acme/widget.git")
	link := filepath.Join(mustResolvePath(t, t.TempDir()), "link")
	require.NoError(t, os.Symlink(physical, link))

	fromPhysical := mustResolveIdentity(t, physical)
	fromLink := mustResolveIdentity(t, link)

	require.Equal(t, fromPhysical.RepoSlug, fromLink.RepoSlug)
	require.Equal(t, fromPhysical.CheckoutSlug, fromLink.CheckoutSlug)
	require.Equal(t, physical, fromLink.BindRoot)
	require.Equal(t, physical, fromLink.CheckoutRoot)
}

// TestResolveIdentitySeparateGitDirIsNotOutside pins the narrowing from #145:
// a .git file whose gitdir contains no worktrees/ segment is a
// --separate-git-dir checkout or a submodule, not a linked worktree, so it
// keeps its own worktree as the bind root and hashes the common dir like any
// checkout.
func TestResolveIdentitySeparateGitDirIsNotOutside(t *testing.T) {
	t.Run("separate git dir", func(t *testing.T) {
		root := mustResolvePath(t, t.TempDir())
		gitdir := filepath.Join(mustResolvePath(t, t.TempDir()), "repo.git")
		require.NoError(t, os.MkdirAll(gitdir, 0o755))
		writeGitdirFile(t, root, gitdir)

		id, err := ResolveIdentity(root)
		require.NoError(t, err)

		require.False(t, id.OutsideWorktree)
		require.Equal(t, root, id.CheckoutRoot)
		require.Equal(t, root, id.BindRoot)
	})

	t.Run("submodule", func(t *testing.T) {
		super := mustResolvePath(t, t.TempDir())
		gitdir := filepath.Join(super, ".git", "modules", "sub")
		require.NoError(t, os.MkdirAll(gitdir, 0o755))
		root := filepath.Join(super, "sub")
		writeGitdirFile(t, root, "../.git/modules/sub")

		id, err := ResolveIdentity(root)
		require.NoError(t, err)

		require.False(t, id.OutsideWorktree)
		require.Equal(t, root, id.CheckoutRoot)
		require.Equal(t, root, id.BindRoot)
	})
}

// newSubmoduleUnderWorktreesAncestor creates a real submodule whose superproject
// lives under a path component literally named "worktrees", and returns the
// submodule's working-tree root. git records the submodule gitdir relative
// (../.git/modules/sub), so this is the shape a joined-then-stripped gitdir
// misclassifies: the "worktrees" ancestor leaks into the joined path.
func newSubmoduleUnderWorktreesAncestor(t *testing.T) string {
	t.Helper()
	origin := testutil.InitRepo(t)
	super := filepath.Join(mustResolvePath(t, t.TempDir()), "worktrees", "super")
	require.NoError(t, os.MkdirAll(super, 0o755))
	testutil.RunGit(t, super, "init")
	testutil.ConfigureRepo(t, super)
	testutil.WriteFile(t, super, "s", "s")
	testutil.RunGit(t, super, "add", ".")
	testutil.RunGit(t, super, "commit", "-m", "init")
	testutil.RunGit(t, super, "-c", "protocol.file.allow=always", "submodule", "add", origin, "sub")
	return filepath.Join(super, "sub")
}

// newRepoUnderWorktreesAncestor creates a real repo whose path contains a
// component literally named "worktrees" (so its .git path does too) and returns
// its working-tree root.
func newRepoUnderWorktreesAncestor(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(mustResolvePath(t, t.TempDir()), "worktrees", "repo")
	require.NoError(t, os.MkdirAll(repo, 0o755))
	testutil.RunGit(t, repo, "init")
	testutil.ConfigureRepo(t, repo)
	testutil.WriteFile(t, repo, "f", "f")
	testutil.RunGit(t, repo, "add", ".")
	testutil.RunGit(t, repo, "commit", "-m", "init")
	return repo
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

func TestRepoSlugWithoutOriginUnchanged(t *testing.T) {
	root := newRepo(t, "")
	folder := sanitizeFolderName(filepath.Base(root))

	id := mustResolveIdentity(t, root)

	require.Equal(t, projectSlug(folder, root), id.RepoSlug)
	require.Equal(t, projectSlug(folder, resolveGitDir(root)), id.CheckoutSlug)
}

func TestRepoSlugUsesOriginRepoName(t *testing.T) {
	root := newRepo(t, "git@github.com:inoio/agents-sandbox.git")
	t.Chdir(root)

	got := MustRepoSlug()

	require.True(
		t,
		strings.HasPrefix(got, "agents-sandbox-"),
		"MustRepoSlug() = %q, want prefix %q",
		got,
		"agents-sandbox-",
	)
	require.Len(t, got, len("agents-sandbox-")+hashIDLen)
}

func TestRepoSlugFallsBackWithoutOrigin(t *testing.T) {
	root := newRepo(t, "")
	t.Chdir(root)

	got := MustRepoSlug()

	folder := sanitizeFolderName(filepath.Base(root))
	require.True(t, strings.HasPrefix(got, folder+"-"), "MustRepoSlug() = %q, want prefix %q", got, folder+"-")
}

func TestRepoSlugFormat(t *testing.T) {
	root := newRepo(t, "")
	t.Chdir(root)

	got := MustRepoSlug()

	// Expected format: <sanitized-folder>-<14 base36 chars>.
	folder := sanitizeFolderName(filepath.Base(root))
	require.True(t, strings.HasPrefix(got, folder+"-"), "MustRepoSlug() = %q, want prefix %q", got, folder+"-")
	hashPart := strings.TrimPrefix(got, folder+"-")
	require.Len(t, hashPart, hashIDLen)
	for _, r := range hashPart {
		isBase36 := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z')
		require.True(t, isBase36, "hash %q contains non-base36 rune %q", hashPart, r)
	}
}

func TestRepoSlugDeterministic(t *testing.T) {
	root := newRepo(t, "")
	t.Chdir(root)

	first := MustRepoSlug()
	second := MustRepoSlug()
	require.Equal(t, first, second)
}

// TestResolvePathErrorsWhenCwdUnreadable pins #354: with the process cwd gone,
// filepath.Abs cannot make a relative path absolute, and resolvePath reports it
// instead of returning the raw input to be hashed into an identity.
func TestResolvePathErrorsWhenCwdUnreadable(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.RemoveAll(dir))

	_, err := resolvePath("relative")
	require.ErrorIs(t, err, fs.ErrNotExist)
}

// TestResolveIdentityErrorsWhenCwdUnresolvable pins that an unresolvable cwd is
// an error, not a fallback-derived slug: otherwise the same physical project
// would get a new state dir, VM and home volume for a transient resolution
// failure (#354).
func TestResolveIdentityErrorsWhenCwdUnresolvable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	_, err := ResolveIdentity(missing)
	require.ErrorIs(t, err, fs.ErrNotExist)
}

// TestResolveIdentityErrorsWhenGitDirUnresolvable pins the common-git-dir half:
// a .git file whose recorded git dir cannot be resolved is an error, not a
// checkout slug hashed from the unresolved recorded path.
func TestResolveIdentityErrorsWhenGitDirUnresolvable(t *testing.T) {
	root := mustResolvePath(t, t.TempDir())
	writeGitdirFile(t, root, filepath.Join(t.TempDir(), "gone.git"))

	_, err := ResolveIdentity(root)
	require.ErrorIs(t, err, fs.ErrNotExist)
}

// TestMustSlugsPanicWhenCwdUnresolvable covers the cwd-derived test accessors:
// they cannot return an error, so an unresolvable cwd is loud rather than a
// silently wrong slug.
func TestMustSlugsPanicWhenCwdUnresolvable(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.Remove(dir))

	require.Panics(t, func() { MustRepoSlug() })
	require.Panics(t, func() { MustCheckoutSlug() })
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

func TestResolveIdentityDirectoryWithStrayBareConfigIsNotARepo(t *testing.T) {
	dir := mustResolvePath(t, t.TempDir())
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config"), []byte("[core]\n\tbare = true\n"), 0o644))

	require.Equal(t, nonRepoIdentity(dir), mustResolveIdentity(t, dir))
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
