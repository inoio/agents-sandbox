package opencodemsb

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func ProjectSlug() string {
	commonDir, err := gitCommonDir(".")
	if err != nil || commonDir == "" {
		cwd, _ := filepath.Abs(".")
		warn("not inside a git repo; using CWD hash as project slug.")
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

func EnsureWorktree(repoRoot, stateDir, projectSlug, branch string) (string, error) {
	target := WorktreePath(stateDir, projectSlug, BranchSlug(branch))
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		if isGitWorktree(target) {
			return target, nil
		}
		os.RemoveAll(target)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", fmt.Errorf("create worktree parent dir: %w", err)
	}
	cmd := exec.Command("git", "worktree", "add", target, branch)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git worktree add failed: %w: %s", err, string(out))
	}
	return target, nil
}

func RemoveWorktree(path string) error {
	cmd := exec.Command("git", "worktree", "remove", "--force", path)
	return cmd.Run()
}
