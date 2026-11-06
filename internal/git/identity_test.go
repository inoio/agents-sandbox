package git

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/testutil"
)

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

func TestRepoSlugWithoutOriginUnchanged(t *testing.T) {
	root := newRepo(t, "")
	folder := sanitizeFolderName(filepath.Base(root))

	id := mustResolveIdentity(t, root)

	require.Equal(t, projectSlug(folder, root), id.RepoSlug)
	require.Equal(t, projectSlug(folder, resolveGitDir(root)), id.CheckoutSlug)
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

func TestResolveIdentityDirectoryWithStrayBareConfigIsNotARepo(t *testing.T) {
	dir := mustResolvePath(t, t.TempDir())
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config"), []byte("[core]\n\tbare = true\n"), 0o644))

	require.Equal(t, nonRepoIdentity(dir), mustResolveIdentity(t, dir))
}

func TestResolveIdentityNonRepoLayout(t *testing.T) {
	dir := mustResolvePath(t, t.TempDir())

	got := mustResolveIdentity(t, dir)

	if got.BindRoot != dir {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, dir)
	}
	if got.CheckoutRoot != "" {
		t.Errorf("CheckoutRoot = %q, want empty", got.CheckoutRoot)
	}
	if got.OutsideWorktree {
		t.Errorf("OutsideWorktree = true, want false")
	}
}

func TestResolveIdentityRepoRoot(t *testing.T) {
	repo := mustResolvePath(t, testutil.InitRepo(t))

	got := mustResolveIdentity(t, repo)

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

func TestResolveIdentityMalformedGitFile(t *testing.T) {
	dir := mustResolvePath(t, t.TempDir())
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("this is not a gitdir pointer"), 0o644); err != nil {
		t.Fatalf("write .git: %v", err)
	}

	got := mustResolveIdentity(t, dir)

	if got.BindRoot != dir {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, dir)
	}
	if got.CheckoutRoot != "" {
		t.Errorf("CheckoutRoot = %q, want empty", got.CheckoutRoot)
	}
	if got.OutsideWorktree {
		t.Errorf("OutsideWorktree = true, want false")
	}
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

func TestRepoSlugDeterministic(t *testing.T) {
	root := newRepo(t, "")
	t.Chdir(root)

	first := MustRepoSlug()
	second := MustRepoSlug()
	require.Equal(t, first, second)
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

// TestResolvePathErrorsForMissingPath pins that resolvePath no longer
// substitutes the unresolved absolute path for a path it cannot resolve: a
// missing path is an error, so no caller can hash a degraded path into a
// project identity (#354).
func TestResolvePathErrorsForMissingPath(t *testing.T) {
	dir := mustResolvePath(t, t.TempDir())
	missing := filepath.Join(dir, "does-not-exist")
	_, err := resolvePath(missing)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("resolvePath(%q) = %v, want an error wrapping fs.ErrNotExist", missing, err)
	}
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
