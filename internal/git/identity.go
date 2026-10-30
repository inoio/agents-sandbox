package git

import (
	"fmt"
	"path/filepath"
)

// Identity is the host-side identity of the project a directory belongs to:
// where to bind /workspace, the enclosing checkout, and the two slugs that key
// the per-project artifacts.
//
// The repo slug derives from the origin remote URL, so every clone of one remote
// shares it; it keys the per-slug user config dir and the sandbox grouping
// label. The checkout slug derives from the resolved git common dir for a
// checkout and its inside-worktrees, so they share it while two clones do not;
// it keys state, the VM name, the home volume, locks and the runner image tag. A
// linked worktree outside its checkout cannot share that VM — its /workspace
// bind can only be one of the two — so it is scoped as its own checkout by
// hashing its worktree root instead.
type Identity struct {
	BindRoot        string // host dir to bind at /workspace
	CheckoutRoot    string // enclosing checkout root; "" when cwd is not in a repo
	WorktreeRoot    string // nearest dir holding a .git entry; "" when cwd is not in a repo
	OutsideWorktree bool   // cwd is a linked worktree outside CheckoutRoot; gets its own checkout slug
	// SharedGitDir is the resolved host path of an outside worktree's shared git
	// dir, which lies outside BindRoot and must be mounted separately; "" unless
	// OutsideWorktree.
	SharedGitDir string
	// RecordedSharedGitDir is SharedGitDir as the worktree's .git file names it
	// (unresolved, and relative under worktree.useRelativePaths). Git inside the
	// VM follows this text, so it is the guest path to mount at; "" unless
	// OutsideWorktree.
	RecordedSharedGitDir string
	Bare                 bool   // the shared git dir is a bare repository
	RepoSlug             string // origin-derived; shared by every clone of a remote
	CheckoutSlug         string // common-dir-derived, or worktree-root-derived when outside
	// LegacyRepoSlugs lists additional origin-keyed identities whose project state
	// and per-slug config predate this one and must still be adopted. It is only
	// non-empty for the bare root itself: a bare repository has no worktree, so
	// the old ProjectSlug fell back to the cwd path there while every worktree
	// inside it kept the origin-derived slug. Adopting both keeps a bare-root
	// user's saved home and config instead of stranding them.
	LegacyRepoSlugs []string
}

// ResolveIdentity derives the workspace layout and both project slugs for cwd in
// a single pass. A directory outside a repository, or one whose .git entry is
// malformed, resolves to the non-repo identity (both slugs derived from the
// resolved path). A linked worktree outside its checkout is reported through
// OutsideWorktree and scoped as its own checkout (its checkout slug hashes the
// worktree root), so it simply runs as its own project and nothing refuses on
// the flag.
//
// It returns an error when cwd or the checkout's git dir cannot be resolved,
// instead of hashing a fallback path: a transient symlink or permission failure
// would otherwise yield a different checkout slug — a new state file, VM and
// home volume — for an existing project, with no diagnostic (#354).
//
// Symlinks are resolved internally so every entry point (run, stop, kill, volume
// ops, build) hashes the same physical path — otherwise /tmp and /private/tmp on
// macOS would produce different checkout slugs. Derivation is plain-file
// inspection; no git binary is invoked.
func ResolveIdentity(cwd string) (Identity, error) {
	resolved, err := resolvePath(cwd)
	if err != nil {
		return Identity{}, fmt.Errorf("resolve project directory: %w", err)
	}
	worktreeRoot := findWorktreeRoot(resolved)
	if worktreeRoot == "" {
		return nonRepoIdentity(resolved), nil
	}
	commonGitDir := resolveGitDir(worktreeRoot)
	if commonGitDir == "" {
		return nonRepoIdentity(resolved), nil
	}
	// Resolve the common dir too: git writes a linked worktree's gitdir path as
	// given, so an unresolved temp path on macOS would not match the path the
	// main checkout hashes, and the worktree would not share its checkout slug.
	commonGitDir, err = resolvePath(commonGitDir)
	if err != nil {
		return Identity{}, fmt.Errorf("resolve git directory: %w", err)
	}

	checkoutRoot, bindRoot, outsideWorktree := bindLayout(worktreeRoot, commonGitDir)
	// An outside worktree hashes its own root: hashing the shared common dir
	// would give it its checkout's slug and therefore its checkout's VM, whose
	// /workspace bind can only be one of the two.
	checkoutHashInput := commonGitDir
	if outsideWorktree {
		checkoutHashInput = worktreeRoot
	}
	repoSlug, checkoutSlug := identitySlugs(worktreeRoot, bindRoot, checkoutHashInput)
	bare := isBare(commonGitDir)
	identity := Identity{
		BindRoot:             bindRoot,
		CheckoutRoot:         checkoutRoot,
		WorktreeRoot:         worktreeRoot,
		OutsideWorktree:      outsideWorktree,
		SharedGitDir:         "",
		RecordedSharedGitDir: "",
		Bare:                 bare,
		RepoSlug:             repoSlug,
		CheckoutSlug:         checkoutSlug,
		LegacyRepoSlugs:      nil,
	}
	if outsideWorktree {
		identity.SharedGitDir = commonGitDir
		identity.RecordedSharedGitDir = recordedSharedGitDir(worktreeRoot)
	}
	// The bare root is not a linked worktree, so bindRoot is the worktree root
	// itself: this is the cwd that predates the split with a path-derived slug.
	// A bare root named "<name>.git" was keyed under that same slug as a
	// non-repo folder (#433). Its checkout slug can equal it, which needs no
	// state move, but the per-slug config is read through the repo slug and
	// these legacy slugs, so it is still listed unless it is the repo slug.
	if bare && bindRoot == worktreeRoot {
		if legacy := projectSlug(
			sanitizeFolderName(filepath.Base(worktreeRoot)),
			worktreeRoot,
		); legacy != repoSlug {
			identity.LegacyRepoSlugs = []string{legacy}
		}
	}
	return identity, nil
}

// MustRepoSlug returns the repo slug of the current working directory: the value
// shared by every clone of the same origin. It is a cwd-derived convenience for
// tests; production reads the ResolveIdentity fields. An unresolvable cwd panics
// rather than returning a slug derived from a degraded path (#354).
//
//deadcode:ignore reason="cwd-derived convenience for tests; production reads the ResolveIdentity fields"
func MustRepoSlug() string {
	identity, err := ResolveIdentity(".")
	if err != nil {
		panic(fmt.Sprintf("git.MustRepoSlug: %v", err))
	}
	return identity.RepoSlug
}

// MustCheckoutSlug returns the checkout slug of the current working directory.
// It is a cwd-derived convenience for tests; production reads the
// ResolveIdentity fields. An unresolvable cwd panics rather than returning a
// slug derived from a degraded path (#354).
//
//deadcode:ignore reason="cwd-derived convenience for tests; production reads the ResolveIdentity fields"
func MustCheckoutSlug() string {
	identity, err := ResolveIdentity(".")
	if err != nil {
		panic(fmt.Sprintf("git.MustCheckoutSlug: %v", err))
	}
	return identity.CheckoutSlug
}

// resolvePath returns path as an absolute, symlink-resolved path. Both steps
// matter: an absolute result keeps the cwd-derived callers (MustRepoSlug and
// MustCheckoutSlug pass ".") from hashing a relative path, and resolving
// symlinks makes /tmp and /private/tmp hash alike.
//
// It reports an error rather than substituting a fallback. A degraded path —
// the raw input or the unresolved absolute path — would be hashed into a slug
// and silently change a project's identity, so the error is the point (#354).
func resolvePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("make path absolute: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve symlinks: %w", err)
	}
	return resolved, nil
}

// nonRepoIdentity is the identity of a directory that is not in a repository:
// both slugs derive from the resolved path, and the directory itself is bound.
func nonRepoIdentity(resolved string) Identity {
	slug := projectSlug(sanitizeFolderName(filepath.Base(resolved)), resolved)
	return Identity{
		BindRoot:             resolved,
		CheckoutRoot:         "",
		WorktreeRoot:         "",
		OutsideWorktree:      false,
		SharedGitDir:         "",
		RecordedSharedGitDir: "",
		Bare:                 false,
		RepoSlug:             slug,
		CheckoutSlug:         slug,
		LegacyRepoSlugs:      nil,
	}
}

// bindLayout decides which host directory to bind at /workspace and where the
// enclosing checkout is. A normal checkout, a --separate-git-dir checkout and a
// submodule bind their own worktree; a linked worktree inside its checkout binds
// the checkout root so sibling worktrees stay visible; and a linked worktree
// outside its checkout binds its own root, since under checkout identity the
// worktree is the "smallest enclosing checkout that contains the CWD". A
// subdirectory of any of these binds the same root, so the agent starts in the
// subdirectory with the repository visible above it.
func bindLayout(worktreeRoot, commonGitDir string) (string, string, bool) {
	if !isLinkedWorktree(worktreeRoot) {
		return worktreeRoot, worktreeRoot, false
	}
	checkoutRoot := checkoutRootOf(commonGitDir)
	if within(checkoutRoot, worktreeRoot) {
		return checkoutRoot, checkoutRoot, false
	}
	return checkoutRoot, worktreeRoot, true
}

// recordedSharedGitDir returns the shared git dir of the linked worktree at
// worktreeRoot exactly as its .git file records it, with the worktree
// registration stripped. Only called for a validated linked worktree, so the
// registration is known to be present.
func recordedSharedGitDir(worktreeRoot string) string {
	gitdir, _ := gitdirFile(worktreeRoot)
	shared, _ := worktreeRegistration(worktreeRoot, gitdir)
	return shared
}

// checkoutRootOf returns the directory that contains the git dir and any nested
// worktrees: the parent of a ".git" dir, or the dir itself under another name
// (a bare root, or a --separate-git-dir target).
func checkoutRootOf(commonGitDir string) string {
	if filepath.Base(commonGitDir) == ".git" {
		return filepath.Dir(commonGitDir)
	}
	return commonGitDir
}

// identitySlugs builds the repo slug and the checkout slug. With an origin,
// both folders come from the origin's last path segment, so two clones read
// "foo-<hashA>"/"foo-<hashB>", and the repo slug hashes the origin URL. Without
// one, both fall back to bindRoot — the checkout root for a plain checkout or an
// inside-worktree, the worktree root for an outside worktree — so an
// inside-worktree shares its checkout's repo slug (#434). The checkout slug
// hashes checkoutHashInput: the resolved common git dir a checkout and its
// inside-worktrees share, or the worktree root for an outside worktree.
func identitySlugs(worktreeRoot, bindRoot, checkoutHashInput string) (string, string) {
	folder := sanitizeFolderName(filepath.Base(bindRoot))
	repoHash := bindRoot
	if url := originURL(worktreeRoot); url != "" {
		folder = sanitizeFolderName(lastPathSegment(url))
		repoHash = url
	}
	return projectSlug(folder, repoHash), projectSlug(folder, checkoutHashInput)
}
