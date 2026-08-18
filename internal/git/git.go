package git

import (
	"crypto/sha256"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/inoio/opencode-sandbox/internal/termio"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
)

// Configuration for slug names/hashes.
// Names and hashes are shortened to fit the limits (image name/tag: 255/128, sandbox name: 128).
const (
	// Lowercase letters are required by docker, therefore base36 was chosen to achieve max entropy per hash length.
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
	if len(s) > maxFolderNameLen {
		s = s[:maxFolderNameLen]
	}
	return strings.Trim(s, "-")
}

func HashID(input string) string {
	sum := sha256.Sum256([]byte(input))
	num := new(big.Int).SetBytes(sum[:])
	encoded := num.Text(base)
	if len(encoded) < hashIDLen {
		encoded = strings.Repeat("0", hashIDLen-len(encoded)) + encoded
	}
	if len(encoded) > hashIDLen {
		encoded = encoded[:hashIDLen]
	}
	return encoded
}

func ProjectSlug(ui termio.UI) string {
	return projectSlugAt(".", ui)
}

// projectSlugAt computes the project slug for the repository at cwd.
//
// The slug base is the human-readable repo name taken from the origin
// remote's URL, and the hash is over the full origin URL, so every clone and
// worktree of the same remote shares one slug (different hosts/forks differ).
// When there is no origin remote it falls back to the checkout folder name
// and a hash of the repository's top-level path.
func projectSlugAt(cwd string, ui termio.UI) string {
	repo, err := git.PlainOpenWithOptions(cwd, &git.PlainOpenOptions{ //nolint:exhaustruct // DetectDotGit only
		DetectDotGit: true,
	})
	if err != nil {
		if abs, absErr := filepath.Abs(cwd); absErr == nil {
			ui.Verbosef("not inside a git repo; using CWD hash as project slug.")
			return projectSlug(sanitizeFolderName(filepath.Base(abs)), abs)
		}
		return projectSlug("", cwd)
	}
	root, err := worktreeRoot(repo)
	if err != nil {
		if abs, absErr := filepath.Abs(cwd); absErr == nil {
			return projectSlug(sanitizeFolderName(filepath.Base(abs)), abs)
		}
		return projectSlug("", cwd)
	}
	if url := originURL(root); url != "" {
		slug := projectSlug(sanitizeFolderName(lastPathSegment(url)), url)
		ui.Verbosef("Using project slug '%s' (origin %s)", slug, url)
		return slug
	}
	slug := projectSlug(sanitizeFolderName(filepath.Base(root)), root)
	ui.Verbosef("Using project slug '%s'", slug)
	return slug
}

// worktreeRoot returns the working-tree root directory of an opened repository.
func worktreeRoot(repo *git.Repository) (string, error) {
	worktree, err := repo.Worktree()
	if err != nil {
		return "", err
	}
	return worktree.Filesystem.Root(), nil
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
	f, err := os.Open(filepath.Join(gitDir, "config"))
	if err != nil {
		return ""
	}
	defer f.Close()
	cfg, err := config.ReadConfig(f)
	if err != nil {
		return ""
	}
	if remote, ok := cfg.Remotes["origin"]; ok && len(remote.URLs) > 0 {
		return remote.URLs[0]
	}
	return ""
}

// resolveGitDir returns the absolute git directory for the checkout rooted at
// root. For a normal checkout that is root/.git; for a linked worktree, whose
// root/.git is a file containing "gitdir: <shared>.git/worktrees/<name>", it
// returns the shared <shared>.git directory. It returns "" if the checkout is
// not a repo.
func resolveGitDir(root string) string {
	path := filepath.Join(root, ".git")
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return path
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(data))
	dir, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return ""
	}
	dir = strings.TrimSpace(dir)
	if i := strings.LastIndex(dir, "/worktrees/"); i >= 0 {
		return dir[:i]
	}
	return dir
}

// lastPathSegment returns the last path segment of a git remote URL, dropping
// a trailing ".git". It handles both scp-like SSH URLs (git@host:org/repo.git)
// and URLs with an explicit scheme (https://host/org/repo.git).
func lastPathSegment(url string) string {
	s := url
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.Index(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSuffix(s, ".git")
}

// projectSlug builds the slug from a sanitized folder name and an input for
// hashing. A sanitized folder name that is empty (e.g. the folder was all
// digits) falls back to "project" so the slug never starts with a bare dash.
func projectSlug(folderName, id string) string {
	if folderName == "" {
		folderName = "project"
	}
	return folderName + "-" + HashID(id)
}
