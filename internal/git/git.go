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

func WorktreePath(stateDir, projectSlug, branch string) string {
	return filepath.Join(stateDir, "isolated-workspaces", projectSlug, branch)
}

func isGitRepo(path string) bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = path
	return cmd.Run() == nil
}

func IsRepoForBranch(path, branch string) bool {
	if !isGitRepo(path) {
		return false
	}
	b, err := BranchAt(path)
	if err != nil {
		return false
	}
	return b == branch
}

func FindManagedRepo(stateDir, projectSlug, branch string) (string, bool, error) {
	target := WorktreePath(stateDir, projectSlug, BranchSlug(branch))
	info, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return target, false, nil
		}
		return target, false, err
	}
	if !info.IsDir() {
		if err := os.Remove(target); err != nil {
			return target, false, err
		}
		return target, false, nil
	}
	if IsRepoForBranch(target, branch) {
		return target, true, nil
	}
	if err := os.RemoveAll(target); err != nil {
		return target, false, err
	}
	return target, false, nil
}

func BranchExists(repoRoot, branch string) bool {
	//nolint:gosec // G204: exec.Command doesn't use a shell; branch is a git ref, not injectable
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = repoRoot
	return cmd.Run() == nil
}

// EnsureManagedRepoFromRef creates or reuses an independent git clone for the
// given branch. If the branch does not exist, it is created from baseRef.
func EnsureManagedRepoFromRef(
	repoRoot, stateDir, projectSlug, branch, baseRef string,
) (string, bool, error) {
	target, ok, err := FindManagedRepo(stateDir, projectSlug, branch)
	if err != nil {
		return "", false, err
	}
	if ok {
		return target, false, nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return "", false, fmt.Errorf("create managed repo parent dir: %w", err)
	}

	if BranchExists(repoRoot, branch) {
		cmd := exec.Command("git", "clone", "--branch", branch, repoRoot, target)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", false, fmt.Errorf("git clone failed: %w: %s", err, string(out))
		}
		return target, true, nil
	}

	if baseRef == "" {
		baseRef = "HEAD"
	}
	cmd := exec.Command("git", "clone", repoRoot, target)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", false, fmt.Errorf("git clone failed: %w: %s", err, string(out))
	}
	if err := checkoutNewBranch(target, branch, baseRef); err != nil {
		return "", false, err
	}
	return target, true, nil
}

func checkoutNewBranch(cwd, branch, baseRef string) error {
	cmd := exec.Command("git", "checkout", "-b", branch, baseRef)
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout -b %s %s failed: %w: %s", branch, baseRef, err, string(out))
	}
	return nil
}

func EnsureManagedRepo(repoRoot, stateDir, projectSlug, branch string) (string, bool, error) {
	return EnsureManagedRepoFromRef(repoRoot, stateDir, projectSlug, branch, "HEAD")
}

func HasUncommittedChanges(path string) (bool, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git status failed in %s: %w", path, err)
	}
	return len(strings.TrimSpace(string(out))) > 0, nil
}

var ErrNothingToCommit = errors.New("nothing to commit")

func CommitAll(path, message string) error {
	cmd := exec.Command("git", "commit", "-a", "-m", message)
	cmd.Dir = path
	if out, err := cmd.CombinedOutput(); err != nil {
		output := string(out)
		if strings.Contains(output, "nothing to commit") || strings.Contains(output, "no changes added to commit") {
			return fmt.Errorf("%w: %s", ErrNothingToCommit, strings.TrimSpace(output))
		}
		return fmt.Errorf("git commit failed in %s: %w: %s", path, err, output)
	}
	return nil
}

func DiscardAll(path string) error {
	cmd := exec.Command("git", "reset", "--hard")
	cmd.Dir = path
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git reset --hard failed in %s: %w: %s", path, err, string(out))
	}
	return nil
}

func checkoutBranch(cwd, branch string) error {
	cmd := exec.Command("git", "checkout", branch)
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout %s failed: %w: %s", branch, err, string(out))
	}
	return nil
}

func RemoveManagedRepo(path string, force bool) error {
	// force is kept for API compatibility; a directory removal does not need
	// git's --force.
	_ = force
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove managed repo %s: %w", path, err)
	}
	return nil
}

func MergeBranchInto(cwd, clonePath, sourceBranch, targetBranch string) error {
	originalBranch, err := BranchAt(cwd)
	if err != nil {
		return fmt.Errorf("unable to determine current branch before merge: %w", err)
	}

	if err := checkoutBranch(cwd, targetBranch); err != nil {
		if restoreErr := checkoutBranch(cwd, originalBranch); restoreErr != nil {
			return errors.Join(err, restoreErr)
		}
		return err
	}

	cmd := exec.Command("git", "pull", clonePath, sourceBranch)
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = AbortMerge(cwd)
		if restoreErr := checkoutBranch(cwd, originalBranch); restoreErr != nil {
			return errors.Join(
				fmt.Errorf(
					"git pull %s %s into %s failed: %w: %s",
					clonePath,
					sourceBranch,
					targetBranch,
					err,
					string(out),
				),
				restoreErr,
			)
		}
		return fmt.Errorf(
			"git pull %s %s into %s failed: %w: %s",
			clonePath,
			sourceBranch,
			targetBranch,
			err,
			string(out),
		)
	}

	if err := checkoutBranch(cwd, originalBranch); err != nil {
		return fmt.Errorf(
			"git pull %s %s into %s succeeded but restore to %s failed: %w",
			clonePath,
			sourceBranch,
			targetBranch,
			originalBranch,
			err,
		)
	}
	return nil
}

func AbortMerge(cwd string) error {
	cmd := exec.Command("git", "merge", "--abort")
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git merge --abort failed in %s: %w: %s", cwd, err, string(out))
	}
	return nil
}

func PruneWorktrees(cwd string) error {
	cmd := exec.Command("git", "worktree", "prune")
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree prune failed in %s: %w: %s", cwd, err, out)
	}
	return nil
}
