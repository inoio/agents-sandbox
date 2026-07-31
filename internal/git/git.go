package git

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"

	git "github.com/go-git/go-git/v5"
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

func ProjectSlug(ui stdio.UI) string {
	commonDir, err := gitCommonDir(".")
	if err != nil || commonDir == "" {
		cwd, _ := filepath.Abs(".")
		ui.Warnf("not inside a git repo; using CWD hash as project slug.")
		return sanitizeFolderName(filepath.Base(cwd)) + "-" + HashID(cwd)
	}
	abs, _ := filepath.Abs(commonDir)
	folderName := sanitizeFolderName(filepath.Base(commonDir))
	projectSlug := folderName + "-" + HashID(abs)
	ui.Verbosef("Using project slug '%s'", projectSlug)
	return projectSlug
}

func BranchSlug(branch string) string {
	slug := strings.ReplaceAll(branch, "-", "--")
	slug = strings.ReplaceAll(slug, "/", "---")
	return slug
}

func gitCommonDir(cwd string) (string, error) {
	opts := &git.PlainOpenOptions{ //nolint:exhaustruct // DetectDotGit only
		DetectDotGit: true,
	}
	repo, err := git.PlainOpenWithOptions(cwd, opts)
	if err != nil {
		return "", errors.New("not inside a git repository")
	}
	worktree, err := repo.Worktree()
	if err != nil {
		return "", err
	}
	dir := worktree.Filesystem.Root()
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("not inside a git repository")
		}
		dir = parent
	}
}

func BranchAt(path string) (string, error) {
	repo, err := git.PlainOpen(path)
	if err != nil {
		return "", fmt.Errorf("unable to determine current git branch from %s: %w", path, err)
	}
	head, err := repo.Head()
	if err != nil {
		return "", fmt.Errorf("unable to determine current git branch from %s: %w", path, err)
	}
	return head.Name().Short(), nil
}

func PruneWorktrees(cwd string) error {
	cmd := exec.Command("git", "worktree", "prune")
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree prune failed in %s: %w: %s", cwd, err, out)
	}
	return nil
}
