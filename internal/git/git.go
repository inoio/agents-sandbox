package git

import (
	"crypto/sha256"
	"fmt"
	"math/big"
	"os/exec"
	"path/filepath"
	"strings"

	"gitlab.inoio.de/inoio/opencode-msb/internal/output"
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

func ProjectSlug(logger *output.Printer) string {
	commonDir, err := gitCommonDir(".")
	if err != nil || commonDir == "" {
		cwd, _ := filepath.Abs(".")
		logger.Warnf("not inside a git repo; using CWD hash as project slug.")
		return sanitizeFolderName(filepath.Base(cwd)) + "-" + HashID(cwd)
	}
	abs, _ := filepath.Abs(commonDir)
	folderName := sanitizeFolderName(filepath.Base(filepath.Dir(abs)))
	return folderName + "-" + HashID(abs)
}

func BranchSlug(branch string) string {
	slug := strings.ReplaceAll(branch, "-", "--")
	slug = strings.ReplaceAll(slug, "/", "---")
	return slug
}

func gitCommonDir(cwd string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--git-common-dir")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(string(out))
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	return filepath.Abs(p)
}

func BranchAt(path string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("unable to determine current git branch from %s: %w", path, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func BranchName(cwd string) (string, error) {
	return BranchAt(cwd)
}

func PruneWorktrees(cwd string) error {
	cmd := exec.Command("git", "worktree", "prune")
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree prune failed in %s: %w: %s", cwd, err, out)
	}
	return nil
}
