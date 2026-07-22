package git

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gitlab.inoio.de/inoio/opencode-msb/internal/log"
)

func ProjectSlug(logger *log.Logger) string {
	commonDir, err := gitCommonDir(".")
	if err != nil || commonDir == "" {
		cwd, _ := filepath.Abs(".")
		logger.Warn("not inside a git repo; using CWD hash as project slug.")
		h := sha256.Sum256([]byte(cwd))
		return "p-" + hex.EncodeToString(h[:])[:8]
	}
	abs, _ := filepath.Abs(commonDir)
	h := sha256.Sum256([]byte(abs))
	return "p-" + hex.EncodeToString(h[:])[:8]
}

func BranchSlug(branch string) string {
	return strings.ReplaceAll(branch, "/", "-")
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

func BranchName(cwd string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("unable to determine current git branch from %s: %w", cwd, err)
	}
	return strings.TrimSpace(string(out)), nil
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

func CurrentWorktreePath(cwd string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return filepath.Abs(strings.TrimSpace(string(out)))
}

func WorktreePath(stateDir, projectSlug, branch string) string {
	return filepath.Join(stateDir, "worktrees", projectSlug, branch)
}

func isGitWorktree(path string) bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = path
	return cmd.Run() == nil
}

func IsWorktreeForBranch(path, branch string) bool {
	if !isGitWorktree(path) {
		return false
	}
	b, err := BranchAt(path)
	if err != nil {
		return false
	}
	return b == branch
}

func FindManagedWorktree(stateDir, projectSlug, branch string) (path string, ok bool, err error) {
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
	if isGitWorktree(target) {
		return target, true, nil
	}
	if err := os.RemoveAll(target); err != nil {
		return target, false, err
	}
	return target, false, nil
}

func branchExists(repoRoot, branch string) bool {
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = repoRoot
	return cmd.Run() == nil
}

func EnsureWorktree(repoRoot, stateDir, projectSlug, branch string) (path string, created bool, err error) {
	target, ok, err := FindManagedWorktree(stateDir, projectSlug, branch)
	if err != nil {
		return "", false, err
	}
	if ok {
		return target, false, nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", false, fmt.Errorf("create worktree parent dir: %w", err)
	}

	var cmd *exec.Cmd
	if branchExists(repoRoot, branch) {
		cmd = exec.Command("git", "worktree", "add", target, branch)
	} else {
		cmd = exec.Command("git", "worktree", "add", "-b", branch, target, "HEAD")
	}
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", false, fmt.Errorf("git worktree add failed: %w: %s", err, string(out))
	}
	return target, true, nil
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

func CommitAll(path, message string) error {
	cmd := exec.Command("git", "commit", "-a", "-m", message)
	cmd.Dir = path
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit failed in %s: %w: %s", path, err, string(out))
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

func RemoveWorktree(path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, ".")
	cmd := exec.Command("git", args...)
	cmd.Dir = path
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove failed for %s: %w: %s", path, err, string(out))
	}
	return nil
}

func MergeBranchInto(cwd, sourceBranch, targetBranch string) error {
	cmd := exec.Command("git", "checkout", targetBranch)
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout %s failed: %w: %s", targetBranch, err, string(out))
	}
	cmd = exec.Command("git", "merge", sourceBranch)
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git merge %s into %s failed: %w: %s", sourceBranch, targetBranch, err, string(out))
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
