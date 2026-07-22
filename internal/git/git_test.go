package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	writeFile(t, dir, "README.md", "hello")
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "initial")
	return dir
}

func TestBranchSlugReplacesSlashes(t *testing.T) {
	got := BranchSlug("feature/foo/bar")
	if got != "feature-foo-bar" {
		t.Errorf("expected 'feature-foo-bar', got %q", got)
	}
}

func TestBranchSlugNoChange(t *testing.T) {
	got := BranchSlug("main")
	if got != "main" {
		t.Errorf("expected 'main', got %q", got)
	}
}

func TestWorktreePathConstruction(t *testing.T) {
	got := WorktreePath("/tmp/state", "p-abc123", "main")
	expected := filepath.Join("/tmp/state", "worktrees", "p-abc123", "main")
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestWorktreePathWithBranchSlug(t *testing.T) {
	got := WorktreePath("/tmp/state", "p-abc", BranchSlug("feat/x"))
	expected := filepath.Join("/tmp/state", "worktrees", "p-abc", "feat-x")
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestBranchAtReturnsCurrentBranch(t *testing.T) {
	repo := initRepo(t)
	branch, err := BranchAt(repo)
	if err != nil {
		t.Fatalf("BranchAt: %v", err)
	}
	if branch != "main" {
		t.Errorf("expected branch 'main', got %q", branch)
	}
}

func TestBranchAtFailsOutsideGitRepo(t *testing.T) {
	dir := t.TempDir()
	_, err := BranchAt(dir)
	if err == nil {
		t.Error("expected error outside git repo, got nil")
	}
}

func TestIsWorktreeForBranchRootCheckout(t *testing.T) {
	repo := initRepo(t)
	if !IsWorktreeForBranch(repo, "main") {
		t.Error("expected root checkout to be worktree for main")
	}
	if IsWorktreeForBranch(repo, "other") {
		t.Error("expected root checkout not to be worktree for other")
	}
}

func TestIsWorktreeForBranchLinkedWorktree(t *testing.T) {
	repo := initRepo(t)
	runGit(t, repo, "branch", "feature")
	wt := filepath.Join(t.TempDir(), "feature")
	runGit(t, repo, "worktree", "add", wt, "feature")
	if !IsWorktreeForBranch(wt, "feature") {
		t.Error("expected linked worktree to be for feature")
	}
	if IsWorktreeForBranch(wt, "main") {
		t.Error("expected linked worktree not to be for main")
	}
}

func TestIsWorktreeForBranchOutsideRepo(t *testing.T) {
	dir := t.TempDir()
	if IsWorktreeForBranch(dir, "main") {
		t.Error("expected non-repo directory not to be a worktree")
	}
}

func TestFindManagedWorktreeMissing(t *testing.T) {
	stateDir := t.TempDir()
	path, ok, err := FindManagedWorktree(stateDir, "p-test", "main")
	if err != nil {
		t.Fatalf("FindManagedWorktree: %v", err)
	}
	if ok {
		t.Error("expected ok=false for missing worktree")
	}
	expected := filepath.Join(stateDir, "worktrees", "p-test", "main")
	if path != expected {
		t.Errorf("expected path %q, got %q", expected, path)
	}
}

func TestFindManagedWorktreeValid(t *testing.T) {
	repo := initRepo(t)
	runGit(t, repo, "branch", "feature")
	stateDir := t.TempDir()
	wt, created, err := EnsureWorktree(repo, stateDir, "p-test", "feature")
	if err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}
	if !created {
		t.Error("expected created=true")
	}
	path, ok, err := FindManagedWorktree(stateDir, "p-test", "feature")
	if err != nil {
		t.Fatalf("FindManagedWorktree: %v", err)
	}
	if !ok {
		t.Error("expected ok=true for valid worktree")
	}
	if path != wt {
		t.Errorf("expected path %q, got %q", wt, path)
	}
}

func TestFindManagedWorktreeInvalidRemoved(t *testing.T) {
	stateDir := t.TempDir()
	target := WorktreePath(stateDir, "p-test", BranchSlug("main"))
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path, ok, err := FindManagedWorktree(stateDir, "p-test", "main")
	if err != nil {
		t.Fatalf("FindManagedWorktree: %v", err)
	}
	if ok {
		t.Error("expected ok=false after removing invalid worktree")
	}
	if path != target {
		t.Errorf("expected path %q, got %q", target, path)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Error("expected invalid worktree directory to be removed")
	}
}

func TestFindManagedWorktreeWrongBranchRemoved(t *testing.T) {
	repo := initRepo(t)
	runGit(t, repo, "branch", "feature")
	stateDir := t.TempDir()
	target := WorktreePath(stateDir, "p-test", BranchSlug("other"))
	runGit(t, repo, "worktree", "add", target, "feature")

	path, ok, err := FindManagedWorktree(stateDir, "p-test", "other")
	if err != nil {
		t.Fatalf("FindManagedWorktree: %v", err)
	}
	if ok {
		t.Error("expected ok=false for wrong-branch worktree")
	}
	if path != target {
		t.Errorf("expected path %q, got %q", target, path)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Error("expected wrong-branch worktree directory to be removed")
	}
}

func TestEnsureWorktreeCreatesFromExistingBranch(t *testing.T) {
	repo := initRepo(t)
	runGit(t, repo, "checkout", "-b", "feature")
	runGit(t, repo, "commit", "-m", "feature commit", "--allow-empty")
	runGit(t, repo, "checkout", "main")
	stateDir := t.TempDir()
	wt, created, err := EnsureWorktree(repo, stateDir, "p-test", "feature")
	if err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}
	if !created {
		t.Error("expected created=true")
	}
	branch, err := BranchAt(wt)
	if err != nil {
		t.Fatalf("BranchAt: %v", err)
	}
	if branch != "feature" {
		t.Errorf("expected branch 'feature', got %q", branch)
	}
}

func TestEnsureWorktreeCreatesNewBranchFromHEAD(t *testing.T) {
	repo := initRepo(t)
	stateDir := t.TempDir()
	wt, created, err := EnsureWorktree(repo, stateDir, "p-test", "new-branch")
	if err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}
	if !created {
		t.Error("expected created=true")
	}
	branch, err := BranchAt(wt)
	if err != nil {
		t.Fatalf("BranchAt: %v", err)
	}
	if branch != "new-branch" {
		t.Errorf("expected branch 'new-branch', got %q", branch)
	}
}

func TestEnsureWorktreeReusesExisting(t *testing.T) {
	repo := initRepo(t)
	runGit(t, repo, "branch", "feature")
	stateDir := t.TempDir()
	wt1, created1, err := EnsureWorktree(repo, stateDir, "p-test", "feature")
	if err != nil {
		t.Fatalf("EnsureWorktree first: %v", err)
	}
	if !created1 {
		t.Error("expected created=true on first call")
	}
	wt2, created2, err := EnsureWorktree(repo, stateDir, "p-test", "feature")
	if err != nil {
		t.Fatalf("EnsureWorktree second: %v", err)
	}
	if created2 {
		t.Error("expected created=false on second call")
	}
	if wt1 != wt2 {
		t.Errorf("expected same path, got %q and %q", wt1, wt2)
	}
}

func TestEnsureWorktreeRecreatesOnWrongBranch(t *testing.T) {
	repo := initRepo(t)
	runGit(t, repo, "branch", "feature")
	runGit(t, repo, "branch", "other")
	stateDir := t.TempDir()
	target := WorktreePath(stateDir, "p-test", BranchSlug("other"))
	runGit(t, repo, "worktree", "add", target, "feature")

	wt, created, err := EnsureWorktree(repo, stateDir, "p-test", "other")
	if err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}
	if !created {
		t.Error("expected created=true when replacing wrong-branch worktree")
	}
	if wt != target {
		t.Errorf("expected path %q, got %q", target, wt)
	}
	branch, err := BranchAt(wt)
	if err != nil {
		t.Fatalf("BranchAt: %v", err)
	}
	if branch != "other" {
		t.Errorf("expected branch 'other', got %q", branch)
	}
}

func TestHasUncommittedChangesClean(t *testing.T) {
	repo := initRepo(t)
	has, err := HasUncommittedChanges(repo)
	if err != nil {
		t.Fatalf("HasUncommittedChanges: %v", err)
	}
	if has {
		t.Error("expected no uncommitted changes in clean repo")
	}
}

func TestHasUncommittedChangesUnstaged(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, repo, "README.md", "changed")
	has, err := HasUncommittedChanges(repo)
	if err != nil {
		t.Fatalf("HasUncommittedChanges: %v", err)
	}
	if !has {
		t.Error("expected uncommitted changes for modified file")
	}
}

func TestHasUncommittedChangesStaged(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, repo, "NEW.md", "new")
	runGit(t, repo, "add", "NEW.md")
	has, err := HasUncommittedChanges(repo)
	if err != nil {
		t.Fatalf("HasUncommittedChanges: %v", err)
	}
	if !has {
		t.Error("expected uncommitted changes for staged file")
	}
}

func TestCommitAllCommitsChanges(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, repo, "README.md", "updated")
	if err := CommitAll(repo, "update readme"); err != nil {
		t.Fatalf("CommitAll: %v", err)
	}
	has, err := HasUncommittedChanges(repo)
	if err != nil {
		t.Fatalf("HasUncommittedChanges: %v", err)
	}
	if has {
		t.Error("expected clean repo after CommitAll")
	}
	msg := strings.TrimSpace(runGit(t, repo, "log", "-1", "--pretty=%B"))
	if msg != "update readme" {
		t.Errorf("expected commit message %q, got %q", "update readme", msg)
	}
}

func TestCommitAllFailsWhenNothingToCommit(t *testing.T) {
	repo := initRepo(t)
	err := CommitAll(repo, "nothing")
	if err == nil {
		t.Error("expected error when nothing to commit")
	}
}

func TestDiscardAllRevertsUnstagedChanges(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, repo, "README.md", "changed")
	if err := DiscardAll(repo); err != nil {
		t.Fatalf("DiscardAll: %v", err)
	}
	has, err := HasUncommittedChanges(repo)
	if err != nil {
		t.Fatalf("HasUncommittedChanges: %v", err)
	}
	if has {
		t.Error("expected clean repo after DiscardAll")
	}
	content, err := os.ReadFile(filepath.Join(repo, "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	if string(content) != "hello" {
		t.Errorf("expected original content 'hello', got %q", string(content))
	}
}

func TestRemoveWorktreeRemovesLinkedWorktree(t *testing.T) {
	repo := initRepo(t)
	wt := filepath.Join(t.TempDir(), "feature")
	runGit(t, repo, "worktree", "add", wt, "-b", "feature")
	if err := RemoveWorktree(wt, false); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Error("expected worktree directory to be removed")
	}
}

func TestRemoveWorktreeForceWithUncommittedChanges(t *testing.T) {
	repo := initRepo(t)
	wt := filepath.Join(t.TempDir(), "feature")
	runGit(t, repo, "worktree", "add", wt, "-b", "feature")
	writeFile(t, wt, "README.md", "changed")
	if err := RemoveWorktree(wt, false); err == nil {
		t.Error("expected error removing worktree with uncommitted changes without force")
	}
	if err := RemoveWorktree(wt, true); err != nil {
		t.Fatalf("RemoveWorktree force: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Error("expected worktree directory to be removed after force")
	}
}

func TestMergeBranchIntoFastForward(t *testing.T) {
	repo := initRepo(t)
	runGit(t, repo, "checkout", "-b", "feature")
	writeFile(t, repo, "feature.txt", "feature")
	runGit(t, repo, "add", "feature.txt")
	runGit(t, repo, "commit", "-m", "feature commit")
	runGit(t, repo, "checkout", "main")
	if err := MergeBranchInto(repo, "feature", "main"); err != nil {
		t.Fatalf("MergeBranchInto: %v", err)
	}
	has, err := HasUncommittedChanges(repo)
	if err != nil {
		t.Fatalf("HasUncommittedChanges: %v", err)
	}
	if has {
		t.Error("expected clean repo after merge")
	}
	if _, err := os.Stat(filepath.Join(repo, "feature.txt")); err != nil {
		t.Errorf("expected feature.txt to exist after merge: %v", err)
	}
}

func TestMergeBranchIntoConflict(t *testing.T) {
	repo := initRepo(t)
	runGit(t, repo, "checkout", "-b", "feature")
	writeFile(t, repo, "README.md", "feature")
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "feature commit")
	runGit(t, repo, "checkout", "main")
	writeFile(t, repo, "README.md", "main")
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "main commit")
	err := MergeBranchInto(repo, "feature", "main")
	if err == nil {
		t.Fatal("expected merge conflict error")
	}
	if err := AbortMerge(repo); err != nil {
		t.Fatalf("AbortMerge: %v", err)
	}
}

func TestAbortMergeResetsMergeState(t *testing.T) {
	repo := initRepo(t)
	runGit(t, repo, "checkout", "-b", "feature")
	writeFile(t, repo, "README.md", "feature")
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "feature commit")
	runGit(t, repo, "checkout", "main")
	writeFile(t, repo, "README.md", "main")
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "main commit")
	if err := MergeBranchInto(repo, "feature", "main"); err == nil {
		t.Fatal("expected merge conflict")
	}
	if err := AbortMerge(repo); err != nil {
		t.Fatalf("AbortMerge: %v", err)
	}
	has, err := HasUncommittedChanges(repo)
	if err != nil {
		t.Fatalf("HasUncommittedChanges: %v", err)
	}
	if has {
		t.Error("expected clean repo after abort")
	}
}
