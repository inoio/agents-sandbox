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
	configureRepo(t, dir)
	writeFile(t, dir, "README.md", "hello")
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "initial")
	return dir
}

func configureRepo(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
}

func TestBranchSlugReplacesSlashes(t *testing.T) {
	got := BranchSlug("feature/foo/bar")
	if got != "feature---foo---bar" {
		t.Errorf("expected 'feature---foo---bar', got %q", got)
	}
}

func TestBranchSlugEscapesDashes(t *testing.T) {
	got := BranchSlug("feature-foo")
	if got != "feature--foo" {
		t.Errorf("expected 'feature--foo', got %q", got)
	}
}

func TestBranchSlugNoCollision(t *testing.T) {
	a := BranchSlug("feature/foo")
	b := BranchSlug("feature-foo")
	if a == b {
		t.Errorf("expected different slugs, got %q and %q", a, b)
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
	expected := filepath.Join("/tmp/state", "isolated-workspaces", "p-abc123", "main")
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestWorktreePathWithBranchSlug(t *testing.T) {
	got := WorktreePath("/tmp/state", "p-abc", BranchSlug("feat/x"))
	expected := filepath.Join("/tmp/state", "isolated-workspaces", "p-abc", "feat---x")
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

func TestIsRepoForBranchRootCheckout(t *testing.T) {
	repo := initRepo(t)
	if !IsRepoForBranch(repo, "main") {
		t.Error("expected root checkout to be a repo for main")
	}
	if IsRepoForBranch(repo, "other") {
		t.Error("expected root checkout not to be repo for other")
	}
}

func TestIsRepoForBranchClonedRepo(t *testing.T) {
	repo := initRepo(t)
	runGit(t, repo, "checkout", "-b", "feature")
	runGit(t, repo, "commit", "-m", "feature commit", "--allow-empty")
	runGit(t, repo, "checkout", "main")
	clone := filepath.Join(t.TempDir(), "feature")
	runGit(t, repo, "clone", "--branch", "feature", repo, clone)
	if !IsRepoForBranch(clone, "feature") {
		t.Error("expected cloned repo to be for feature")
	}
	if IsRepoForBranch(clone, "main") {
		t.Error("expected cloned repo not to be for main")
	}
}

func TestIsRepoForBranchOutsideRepo(t *testing.T) {
	dir := t.TempDir()
	if IsRepoForBranch(dir, "main") {
		t.Error("expected non-repo directory not to be a repo")
	}
}

func TestFindManagedRepoMissing(t *testing.T) {
	stateDir := t.TempDir()
	path, ok, err := FindManagedRepo(stateDir, "p-test", "main")
	if err != nil {
		t.Fatalf("FindManagedRepo: %v", err)
	}
	if ok {
		t.Error("expected ok=false for missing repo")
	}
	expected := filepath.Join(stateDir, "isolated-workspaces", "p-test", "main")
	if path != expected {
		t.Errorf("expected path %q, got %q", expected, path)
	}
}

func TestFindManagedRepoValid(t *testing.T) {
	repo := initRepo(t)
	runGit(t, repo, "branch", "feature")
	stateDir := t.TempDir()
	wt, created, err := EnsureManagedRepo(repo, stateDir, "p-test", "feature")
	if err != nil {
		t.Fatalf("EnsureManagedRepo: %v", err)
	}
	if !created {
		t.Error("expected created=true")
	}
	path, ok, err := FindManagedRepo(stateDir, "p-test", "feature")
	if err != nil {
		t.Fatalf("FindManagedRepo: %v", err)
	}
	if !ok {
		t.Error("expected ok=true for valid repo")
	}
	if path != wt {
		t.Errorf("expected path %q, got %q", wt, path)
	}
}

func TestFindManagedRepoInvalidRemoved(t *testing.T) {
	stateDir := t.TempDir()
	target := WorktreePath(stateDir, "p-test", BranchSlug("main"))
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path, ok, err := FindManagedRepo(stateDir, "p-test", "main")
	if err != nil {
		t.Fatalf("FindManagedRepo: %v", err)
	}
	if ok {
		t.Error("expected ok=false after removing invalid repo")
	}
	if path != target {
		t.Errorf("expected path %q, got %q", target, path)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Error("expected invalid repo directory to be removed")
	}
}

func TestFindManagedRepoWrongBranchRemoved(t *testing.T) {
	repo := initRepo(t)
	stateDir := t.TempDir()
	target := WorktreePath(stateDir, "p-test", BranchSlug("other"))
	runGit(t, repo, "clone", repo, target)

	path, ok, err := FindManagedRepo(stateDir, "p-test", "other")
	if err != nil {
		t.Fatalf("FindManagedRepo: %v", err)
	}
	if ok {
		t.Error("expected ok=false for wrong-branch repo")
	}
	if path != target {
		t.Errorf("expected path %q, got %q", target, path)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Error("expected wrong-branch repo directory to be removed")
	}
}

func TestEnsureManagedRepoCreatesFromExistingBranch(t *testing.T) {
	repo := initRepo(t)
	runGit(t, repo, "checkout", "-b", "feature")
	runGit(t, repo, "commit", "-m", "feature commit", "--allow-empty")
	runGit(t, repo, "checkout", "main")
	stateDir := t.TempDir()
	wt, created, err := EnsureManagedRepo(repo, stateDir, "p-test", "feature")
	if err != nil {
		t.Fatalf("EnsureManagedRepo: %v", err)
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

func TestEnsureManagedRepoCreatesNewBranchFromHEAD(t *testing.T) {
	repo := initRepo(t)
	stateDir := t.TempDir()
	wt, created, err := EnsureManagedRepo(repo, stateDir, "p-test", "new-branch")
	if err != nil {
		t.Fatalf("EnsureManagedRepo: %v", err)
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

func TestEnsureManagedRepoReusesExisting(t *testing.T) {
	repo := initRepo(t)
	runGit(t, repo, "branch", "feature")
	stateDir := t.TempDir()
	wt1, created1, err := EnsureManagedRepo(repo, stateDir, "p-test", "feature")
	if err != nil {
		t.Fatalf("EnsureManagedRepo first: %v", err)
	}
	if !created1 {
		t.Error("expected created=true on first call")
	}
	wt2, created2, err := EnsureManagedRepo(repo, stateDir, "p-test", "feature")
	if err != nil {
		t.Fatalf("EnsureManagedRepo second: %v", err)
	}
	if created2 {
		t.Error("expected created=false on second call")
	}
	if wt1 != wt2 {
		t.Errorf("expected same path, got %q and %q", wt1, wt2)
	}
}

func TestEnsureManagedRepoRecreatesOnWrongBranch(t *testing.T) {
	repo := initRepo(t)
	stateDir := t.TempDir()
	target := WorktreePath(stateDir, "p-test", BranchSlug("other"))
	runGit(t, repo, "clone", "--branch", "main", repo, target)

	wt, created, err := EnsureManagedRepo(repo, stateDir, "p-test", "other")
	if err != nil {
		t.Fatalf("EnsureManagedRepo: %v", err)
	}
	if !created {
		t.Error("expected created=true when replacing wrong-branch repo")
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

func TestRemoveManagedRepoRemovesClonedRepo(t *testing.T) {
	repo := initRepo(t)
	wt := filepath.Join(t.TempDir(), "feature")
	runGit(t, repo, "clone", repo, wt)
	if err := RemoveManagedRepo(wt, false); err != nil {
		t.Fatalf("RemoveManagedRepo: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Error("expected repo directory to be removed")
	}
}

func TestRemoveManagedRepoRemovesWithUncommittedChanges(t *testing.T) {
	repo := initRepo(t)
	wt := filepath.Join(t.TempDir(), "feature")
	runGit(t, repo, "clone", repo, wt)
	writeFile(t, wt, "README.md", "changed")
	if err := RemoveManagedRepo(wt, false); err != nil {
		t.Fatalf("RemoveManagedRepo: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Error("expected repo directory to be removed despite uncommitted changes")
	}
}

func TestMergeBranchIntoFastForward(t *testing.T) {
	repo := initRepo(t)
	clone := filepath.Join(t.TempDir(), "clone")
	runGit(t, repo, "clone", repo, clone)
	configureRepo(t, clone)

	runGit(t, clone, "checkout", "-b", "feature")
	writeFile(t, clone, "feature.txt", "feature")
	runGit(t, clone, "add", "feature.txt")
	runGit(t, clone, "commit", "-m", "feature commit")

	if err := MergeBranchInto(repo, clone, "feature", "main"); err != nil {
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
	branch, err := BranchAt(repo)
	if err != nil {
		t.Fatalf("BranchAt: %v", err)
	}
	if branch != "main" {
		t.Errorf("expected to be back on 'main', got %q", branch)
	}
}

func TestMergeBranchIntoConflict(t *testing.T) {
	repo := initRepo(t)
	clone := filepath.Join(t.TempDir(), "clone")
	runGit(t, repo, "clone", repo, clone)
	configureRepo(t, clone)

	runGit(t, clone, "checkout", "-b", "feature")
	writeFile(t, clone, "README.md", "feature")
	runGit(t, clone, "add", "README.md")
	runGit(t, clone, "commit", "-m", "feature commit")

	writeFile(t, repo, "README.md", "main")
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "main commit")

	err := MergeBranchInto(repo, clone, "feature", "main")
	if err == nil {
		t.Fatal("expected merge conflict error")
	}
	branch, err := BranchAt(repo)
	if err != nil {
		t.Fatalf("BranchAt: %v", err)
	}
	if branch != "main" {
		t.Errorf("expected to be back on 'main' after failed merge, got %q", branch)
	}
	has, err := HasUncommittedChanges(repo)
	if err != nil {
		t.Fatalf("HasUncommittedChanges: %v", err)
	}
	if has {
		t.Error("expected clean repo after aborted merge")
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
	if out, err := exec.Command("git", "-C", repo, "merge", "feature").CombinedOutput(); err == nil {
		t.Fatal("expected manual merge conflict")
	} else {
		t.Logf("manual merge output: %s", out)
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
