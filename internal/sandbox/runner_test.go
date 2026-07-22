package sandbox

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/git"
	"gitlab.inoio.de/inoio/opencode-msb/internal/log"
	"gitlab.inoio.de/inoio/opencode-msb/internal/prompt"
)

func TestParseMemoryGigabytes(t *testing.T) {
	got := parseMemory("4G")
	if got != 4096 {
		t.Errorf("expected 4096, got %d", got)
	}
}

func TestParseMemoryMegabytes(t *testing.T) {
	got := parseMemory("512M")
	if got != 512 {
		t.Errorf("expected 512, got %d", got)
	}
}

func TestParseMemoryPlainNumber(t *testing.T) {
	got := parseMemory("2048")
	if got != 2048 {
		t.Errorf("expected 2048, got %d", got)
	}
}

func TestParseMemoryLowercase(t *testing.T) {
	got := parseMemory("2g")
	if got != 2048 {
		t.Errorf("expected 2048, got %d", got)
	}
}

func TestSandboxNameTruncation(t *testing.T) {
	got := sandboxName("p-abcdef", "feat-very-long-branch-name-that-exceeds-the-limit-and-more")
	if len(got) > 128 {
		t.Errorf("expected name <= 128 bytes, got %d", len(got))
	}
}

func TestBuildEnvMap(t *testing.T) {
	envExtra := []string{"FOO=bar", "BAZ=qux"}
	got := buildEnvMap(envExtra)
	if got["SANDBOX_USER"] != "dev" {
		t.Errorf("expected SANDBOX_USER=dev, got %q", got["SANDBOX_USER"])
	}
	if got["SHELL"] != "/bin/bash" {
		t.Errorf("expected SHELL=/bin/bash, got %q", got["SHELL"])
	}
	if got["FOO"] != "bar" {
		t.Errorf("expected FOO=bar, got %q", got["FOO"])
	}
}

func TestReadSandboxEnvMissing(t *testing.T) {
	env := readSandboxEnv()
	if len(env) != 0 {
		t.Errorf("expected 0 env vars when .opencode-msb/env missing, got %d", len(env))
	}
}

func TestBuildOpencodeArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		auto bool
		want []string
	}{
		{"auto default", nil, true, []string{"--auto"}},
		{"auto with forwarded args", []string{"foo", "bar"}, true, []string{"--auto", "foo", "bar"}},
		{"no-auto", []string{"foo"}, false, []string{"foo"}},
		{"no-auto empty args", nil, false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildOpencodeArgs(tt.args, tt.auto)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildOpencodeArgs(%v, %v) = %v, want %v", tt.args, tt.auto, got, tt.want)
			}
		})
	}
}

func TestResolveWorkspaceNoWorktree(t *testing.T) {
	repo := createTempRepo(t)
	commitFile(t, repo, "README.md", "hello", "initial")
	branch := currentBranch(t, repo)

	wtPath, gotBranch, cwdBranch, created, err := resolveWorkspace(repo, RunOptions{}, Config{StateDir: t.TempDir()}, "test-project", newTestLogger(t))
	if err != nil {
		t.Fatalf("resolveWorkspace failed: %v", err)
	}
	if wtPath != repo {
		t.Errorf("expected workspace %q, got %q", repo, wtPath)
	}
	if gotBranch != branch {
		t.Errorf("expected branch %q, got %q", branch, gotBranch)
	}
	if cwdBranch != "" {
		t.Errorf("expected empty cwdBranch, got %q", cwdBranch)
	}
	if created {
		t.Error("expected created=false")
	}
}

func TestResolveWorkspaceWorktreeMatchesCurrentBranch(t *testing.T) {
	repo := createTempRepo(t)
	commitFile(t, repo, "README.md", "hello", "initial")
	branch := currentBranch(t, repo)

	wtPath, gotBranch, cwdBranch, created, err := resolveWorkspace(repo, RunOptions{Worktree: branch}, Config{StateDir: t.TempDir()}, "test-project", newTestLogger(t))
	if err != nil {
		t.Fatalf("resolveWorkspace failed: %v", err)
	}
	if wtPath != repo {
		t.Errorf("expected workspace %q, got %q", repo, wtPath)
	}
	if gotBranch != branch {
		t.Errorf("expected branch %q, got %q", branch, gotBranch)
	}
	if cwdBranch != branch {
		t.Errorf("expected cwdBranch %q, got %q", branch, cwdBranch)
	}
	if created {
		t.Error("expected created=false")
	}
}

func TestResolveWorkspaceWorktreeCreatesManagedWorktree(t *testing.T) {
	prompt.AssumeYes = true
	defer func() { prompt.AssumeYes = false }()

	repo := createTempRepo(t)
	commitFile(t, repo, "README.md", "hello", "initial")
	branch := currentBranch(t, repo)

	stateDir := t.TempDir()
	projectSlug := "test-project"
	wantPath := filepath.Join(stateDir, "worktrees", projectSlug, "feature")

	wtPath, gotBranch, cwdBranch, created, err := resolveWorkspace(repo, RunOptions{Worktree: "feature"}, Config{StateDir: stateDir}, projectSlug, newTestLogger(t))
	if err != nil {
		t.Fatalf("resolveWorkspace failed: %v", err)
	}
	if !created {
		t.Error("expected created=true")
	}
	if gotBranch != "feature" {
		t.Errorf("expected branch feature, got %q", gotBranch)
	}
	if cwdBranch != branch {
		t.Errorf("expected cwdBranch %q, got %q", branch, cwdBranch)
	}
	if wtPath != wantPath {
		t.Errorf("expected worktree path %q, got %q", wantPath, wtPath)
	}
}

func TestResolveWorkspaceWorktreeOutsideRepo(t *testing.T) {
	tmp := t.TempDir()

	_, _, _, _, err := resolveWorkspace(tmp, RunOptions{Worktree: "feature"}, Config{StateDir: t.TempDir()}, "test-project", newTestLogger(t))
	if err == nil {
		t.Fatal("expected error when --worktree is used outside a git repo")
	}
}

func TestCleanupWorktreeRemovesCleanWorktree(t *testing.T) {
	prompt.AssumeYes = true
	defer func() { prompt.AssumeYes = false }()

	repo := createTempRepo(t)
	commitFile(t, repo, "README.md", "hello", "initial")
	wtPath := createManagedWorktree(t, repo, "feature")

	err := cleanupWorktree(wtPath, repo, currentBranch(t, repo), RunOptions{Worktree: "feature"}, newTestLogger(t))
	if err != nil {
		t.Fatalf("cleanupWorktree failed: %v", err)
	}
	if _, statErr := os.Stat(wtPath); !os.IsNotExist(statErr) {
		t.Errorf("expected worktree %q to be removed", wtPath)
	}
}

func TestCleanupWorktreeKeepsUncommittedChanges(t *testing.T) {
	prompt.AssumeYes = true
	defer func() { prompt.AssumeYes = false }()

	repo := createTempRepo(t)
	commitFile(t, repo, "README.md", "hello", "initial")
	wtPath := createManagedWorktree(t, repo, "feature")
	writeFile(t, filepath.Join(wtPath, "feature.txt"), "feature work")

	err := cleanupWorktree(wtPath, repo, currentBranch(t, repo), RunOptions{Worktree: "feature"}, newTestLogger(t))
	if err != nil {
		t.Fatalf("cleanupWorktree failed: %v", err)
	}
	if _, statErr := os.Stat(wtPath); os.IsNotExist(statErr) {
		t.Fatal("expected worktree to be kept")
	}
	got, err := os.ReadFile(filepath.Join(wtPath, "feature.txt"))
	if err != nil {
		t.Fatalf("reading kept file: %v", err)
	}
	if string(got) != "feature work" {
		t.Errorf("expected kept changes intact, got %q", got)
	}
}

func TestCleanupWorktreeDiscardsAndRemoves(t *testing.T) {
	cleanup := prompt.SetStdinForTesting(strings.NewReader("d\nr\n"))
	defer cleanup()

	repo := createTempRepo(t)
	commitFile(t, repo, "README.md", "hello", "initial")
	wtPath := createManagedWorktree(t, repo, "feature")
	writeFile(t, filepath.Join(wtPath, "feature.txt"), "feature work")

	err := cleanupWorktree(wtPath, repo, currentBranch(t, repo), RunOptions{Worktree: "feature"}, newTestLogger(t))
	if err != nil {
		t.Fatalf("cleanupWorktree failed: %v", err)
	}
	if _, statErr := os.Stat(wtPath); !os.IsNotExist(statErr) {
		t.Errorf("expected worktree %q to be removed", wtPath)
	}
}

func TestCleanupWorktreeMergeSuccess(t *testing.T) {
	cleanup := prompt.SetStdinForTesting(strings.NewReader("m\n\n"))
	defer cleanup()

	repo := createTempRepo(t)
	commitFile(t, repo, "README.md", "hello", "initial")
	wtPath := createManagedWorktree(t, repo, "feature")
	writeFile(t, filepath.Join(wtPath, "feature.txt"), "feature work")
	runGit(t, wtPath, "add", ".")
	runGit(t, wtPath, "commit", "-m", "feature commit")

	targetBranch := currentBranch(t, repo)
	err := cleanupWorktree(wtPath, repo, targetBranch, RunOptions{Worktree: "feature"}, newTestLogger(t))
	if err != nil {
		t.Fatalf("cleanupWorktree failed: %v", err)
	}
	if _, statErr := os.Stat(wtPath); !os.IsNotExist(statErr) {
		t.Errorf("expected worktree %q to be removed", wtPath)
	}
	out := runGitOutput(t, repo, "log", "--oneline", targetBranch)
	if !strings.Contains(out, "feature commit") {
		t.Errorf("feature commit not reachable from %q: %q", targetBranch, out)
	}
}

func TestCleanupWorktreeMergeConflict(t *testing.T) {
	cleanup := prompt.SetStdinForTesting(strings.NewReader("m\n\n"))
	defer cleanup()

	repo := createTempRepo(t)
	commitFile(t, repo, "file.txt", "main", "initial")
	wtPath := createManagedWorktree(t, repo, "feature")

	writeFile(t, filepath.Join(wtPath, "file.txt"), "feature")
	runGit(t, wtPath, "add", ".")
	runGit(t, wtPath, "commit", "-m", "feature commit")

	writeFile(t, filepath.Join(repo, "file.txt"), "main conflicting")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "main conflicting")

	targetBranch := currentBranch(t, repo)
	err := cleanupWorktree(wtPath, repo, targetBranch, RunOptions{Worktree: "feature"}, newTestLogger(t))
	if err == nil {
		t.Fatal("expected error for merge conflict")
	}
	if _, statErr := os.Stat(wtPath); !os.IsNotExist(statErr) {
		t.Errorf("expected worktree %q to be removed", wtPath)
	}
	mergeHead := filepath.Join(repo, ".git", "MERGE_HEAD")
	if _, statErr := os.Stat(mergeHead); !os.IsNotExist(statErr) {
		t.Error("expected merge to be aborted")
	}
}

func createTempRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	return dir
}

func currentBranch(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimSpace(runGitOutput(t, dir, "rev-parse", "--abbrev-ref", "HEAD"))
}

func createManagedWorktree(t *testing.T, repo, branch string) string {
	t.Helper()
	stateDir := t.TempDir()
	wtPath, _, err := git.EnsureWorktreeFromRef(repo, stateDir, "test-project", branch, "HEAD")
	if err != nil {
		t.Fatalf("create worktree for %q: %v", branch, err)
	}
	return wtPath
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func commitFile(t *testing.T, dir, relPath, content, msg string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, relPath), content)
	runGit(t, dir, "add", relPath)
	runGit(t, dir, "commit", "-m", msg)
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s failed: %v: %s", args, dir, err, out)
	}
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v: %s", args, dir, err, out)
	}
	return string(out)
}

func newTestLogger(t *testing.T) *log.Logger {
	t.Helper()
	return log.New(io.Discard, false)
}
