package git

import (
	"crypto/sha256"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/config"
)

// Configuration for slug names/hashes.
// Names and hashes are shortened to fit the limits (image name/tag: 255/128, sandbox name: 128).
const (
	// Docker requires lowercase letters, therefore base36 was chosen to achieve max entropy per hash length.
	base = 36
	// Length of 14 -> ~72 bits entropy, collision probability is below 1e-9 for millions of inputs. Enough for naming.
	hashIDLen        = 14
	maxFolderNameLen = 20
)

func sanitizeFolderName(name string) string {
	name = strings.ToLower(name)
	fields := strings.FieldsFunc(name, func(r rune) bool {
		return r < '0' || (r > '9' && r < 'a') || r > 'z'
	})
	s := strings.Join(fields, "-")
	// gomutants:disable-next-line CONDITIONALS_BOUNDARY reason="at len(s)==maxFolderNameLen both > and >= slice to the same unchanged string, and below it neither truncates"
	if len(s) > maxFolderNameLen {
		s = s[:maxFolderNameLen]
	}
	return strings.Trim(s, "-")
}

func HashID(input string) string {
	sum := sha256.Sum256([]byte(input))
	num := new(big.Int).SetBytes(sum[:])
	encoded := num.Text(base)
	// A base36 rendering of a SHA-256 value is never exactly hashIDLen chars
	// (it is 1 for the all-zero hash and >=49 otherwise), so >= and > agree.
	// gomutants:disable-next-line CONDITIONALS_BOUNDARY reason="the base36 of a 256-bit value is never 14 chars long, so the boundary value is unreachable and > and >= agree for every input"
	if len(encoded) > hashIDLen {
		encoded = encoded[:hashIDLen]
	}
	return encoded
}

// originURL returns the origin remote URL for the checkout rooted at root, or
// "" if there is none. For a normal checkout it reads the repo's own config;
// for a linked worktree (whose .git is a file pointing into a shared gitdir)
// go-git does not load the shared config, so the config file is parsed
// directly with go-git's config.ReadConfig.
func originURL(root string) string {
	gitDir := resolveGitDir(root)
	if gitDir == "" {
		return ""
	}
	cfg, err := readRepoConfig(gitDir)
	if err != nil {
		return ""
	}
	if remote, ok := cfg.Remotes["origin"]; ok && len(remote.URLs) > 0 {
		return remote.URLs[0]
	}
	return ""
}

// readRepoConfig parses the config of the repository whose shared git directory
// is gitDir. go-git does not load a linked worktree's shared config through the
// repository object, so the config file is read directly. Callers treat a
// missing or unparseable config as "unknown" rather than failing.
func readRepoConfig(gitDir string) (*config.Config, error) {
	f, err := os.Open(filepath.Join(gitDir, "config"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return config.ReadConfig(f)
}

// isBare reports whether gitDir (a repository's shared git directory) is a bare
// repository, read from its config's core.bare. A missing or unreadable config
// means "not bare": that is today's behavior for an unrecognized repo.
func isBare(gitDir string) bool {
	cfg, err := readRepoConfig(gitDir)
	if err != nil {
		return false
	}
	return cfg.Core.IsBare
}

// resolveGitDir returns the absolute git directory for the checkout rooted at
// root. For a normal checkout that is root/.git; for a linked worktree, whose
// root/.git is a file containing "gitdir: <shared>.git/worktrees/<name>", it
// returns the shared <shared>.git directory; for a bare git dir named other
// than ".git" it is root itself. It returns "" if the checkout is not a repo.
func resolveGitDir(root string) string {
	path := filepath.Join(root, ".git")
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return path
	}
	if isNamedBareGitDir(root) {
		return root
	}
	dir, ok := gitdirFile(root)
	if !ok {
		return ""
	}
	// Strip a validated worktree registration before making the path absolute:
	// the raw text still carries its ancestors when git wrote an absolute gitdir,
	// and a "worktrees" component there is not necessarily a registration (see
	// worktreeRegistration).
	if shared, ok := worktreeRegistration(root, dir); ok {
		dir = shared
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return dir
}

// isLinkedWorktree reports whether root is a linked worktree: its .git is a file
// recording a validated worktree registration. A --separate-git-dir checkout and
// a submodule also have a .git file — and can even match a registration's shape —
// but their git dir lacks the registration's commondir marker, so they are not
// linked worktrees and are never treated as outside.
func isLinkedWorktree(root string) bool {
	dir, ok := gitdirFile(root)
	// gomutants:disable-next-line BRANCH_IF reason="with no gitdir the fall-through calls worktreeRegistration(root, \"\"), whose empty-name check returns false, exactly like this branch"
	if !ok {
		return false
	}
	_, ok = worktreeRegistration(root, dir)
	return ok
}

// worktreeRegistration returns the shared git dir recorded in gitdir when gitdir
// names a linked worktree's registration directory, i.e.
// "<shared>/worktrees/<name>": the shape locates a candidate (a single final
// segment, since git never lets a worktree name contain "/"), and the candidate
// must contain a "commondir" marker file, which is what the registration has and
// a --separate-git-dir or submodule git dir does not. The shape alone is not
// enough: "<ancestor>/worktrees/<name>.git" also matches it. The candidate is
// validated before being stripped, and a relative gitdir is validated against
// root.
func worktreeRegistration(root, gitdir string) (string, bool) {
	// strings.CutLast returns an empty name when the separator is absent, so
	// the empty-name check below already rejects a non-registration gitdir.
	shared, name, _ := strings.CutLast(gitdir, "/worktrees/")
	if name == "" || strings.Contains(name, "/") {
		return "", false
	}
	candidate := gitdir
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	if _, err := os.Stat(filepath.Join(candidate, "commondir")); err != nil {
		return "", false
	}
	return shared, true
}

// gitdirFile returns the gitdir path recorded in root/.git when that entry is a
// file (a linked worktree, or a --separate-git-dir or submodule checkout),
// exactly as git wrote it: still relative when git recorded it relative
// (worktree.useRelativePaths). Callers classify and strip on that text before
// making it absolute, so a "worktrees" path component in an ancestor of root
// cannot masquerade as a worktree registration. It reports false when .git is a
// directory, missing, unreadable, or lacks the "gitdir:" prefix.
func gitdirFile(root string) (string, bool) {
	path := filepath.Join(root, ".git")
	// Reading .git directly rejects a directory, a missing entry, and an
	// unreadable file alike, so no separate stat is needed to classify them.
	data, err := os.ReadFile(path)
	// gomutants:disable-next-line BRANCH_IF reason="with no data read, the CutPrefix below finds no gitdir: prefix and returns the same (\"\", false)"
	if err != nil {
		return "", false
	}
	dir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(dir), true
}

// lastPathSegment returns the last path segment of a git remote URL, dropping
// a trailing ".git". It handles both scp-like SSH URLs (git@host:org/repo.git)
// and URLs with an explicit scheme (https://host/org/repo.git).
func lastPathSegment(url string) string {
	s := url
	// gomutants:disable-next-line CONDITIONALS_BOUNDARY,INTEGER_INCREMENT,BRANCH_IF reason="the later LastIndex('/') strip discards the scheme for every input, so dropping or shifting the '://' strip cannot change the final segment"
	if i := strings.Index(s, "://"); i >= 0 {
		// gomutants:disable-next-line STATEMENT_REMOVE,ARITHMETIC_BASE,INTEGER_DECREMENT reason="the later LastIndex('/') strip discards the scheme for every input, so skipping or shifting the strip cannot change the final segment"
		s = s[i+3:]
	}
	// gomutants:disable-next-line INTEGER_DECREMENT reason="i>=-1 is always true and the i+1 slice is a no-op when i is -1"
	if i := strings.Index(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	// gomutants:disable-next-line INTEGER_DECREMENT reason="i>=-1 is always true and the i+1 slice is a no-op when i is -1"
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	// gomutants:disable-next-line INTEGER_DECREMENT reason="i>=-1 is always true and the i+1 slice is a no-op when i is -1"
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSuffix(s, ".git")
}

// projectSlug builds the slug from a sanitized folder name and an input for
// hashing. A sanitized folder name that is empty (e.g., the folder was all
// digits) falls back to "project" so the slug never starts with a bare dash.
func projectSlug(folderName, id string) string {
	if folderName == "" {
		folderName = "project"
	}
	return folderName + "-" + HashID(id)
}
