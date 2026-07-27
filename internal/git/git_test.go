package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/output"
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

func TestHashIDReturns14Chars(t *testing.T) {
	got := HashID("test-input")
	if len(got) != 14 {
		t.Errorf("expected 14 chars, got %d (%q)", len(got), got)
	}
}

func TestHashIDDeterministic(t *testing.T) {
	a := HashID("sha256:abc123def456")
	b := HashID("sha256:abc123def456")
	if a != b {
		t.Errorf("expected deterministic output, got %q and %q", a, b)
	}
}

func TestHashIDDifferentInputs(t *testing.T) {
	a := HashID("hello")
	b := HashID("world")
	if a == b {
		t.Errorf("expected different hashes for different inputs, got %q for both", a)
	}
}

func TestHashIDBase36AlphabetOnly(t *testing.T) {
	got := HashID("sha256:fce5c4a3b2d1e0f9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5")
	for _, r := range got {
		isBase36 := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z')
		if !isBase36 {
			t.Errorf("expected base36 alphabet only, found %q in %q", r, got)
		}
	}
}

func TestHashIDKnownValues(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "5oaq0bjhj6un82"},
		{"sha256:abc123def456", "3k5q07ywpibwp5"},
		{"hello", "14bu24ea7cq4jh"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := HashID(tt.input); got != tt.want {
				t.Errorf("HashID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSanitizeFolderNameLowercases(t *testing.T) {
	got := sanitizeFolderName("MyApp")
	if got != "myapp" {
		t.Errorf("expected %q, got %q", "myapp", got)
	}
}

func TestSanitizeFolderNameReplacesNonAlnum(t *testing.T) {
	got := sanitizeFolderName("My App")
	if got != "my-app" {
		t.Errorf("expected %q, got %q", "my-app", got)
	}
}

func TestSanitizeFolderNameCollapsesDashes(t *testing.T) {
	got := sanitizeFolderName("My---App!!!")
	if got != "my-app" {
		t.Errorf("expected %q, got %q", "my-app", got)
	}
}

func TestSanitizeFolderNameTrimsLeadingTrailingDashes(t *testing.T) {
	got := sanitizeFolderName("---leading-and-trailing---")
	if got != "leading-and-trailing" {
		t.Errorf("expected %q, got %q", "leading-and-trailing", got)
	}
}

func TestSanitizeFolderNameCapsAt20(t *testing.T) {
	got := sanitizeFolderName("abcdefghijklmnopqrstuvwxyz")
	if len(got) > 20 {
		t.Errorf("expected <= 20 chars, got %d (%q)", len(got), got)
	}
}

func TestSanitizeFolderNameEmptyInput(t *testing.T) {
	got := sanitizeFolderName("")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestProjectSlugFormat(t *testing.T) {
	repo := initRepo(t)
	// ProjectSlug uses the working directory, so chdir into the repo.
	t.Chdir(repo)
	l := output.NewPrinter(os.Stderr, false)
	got := ProjectSlug(l)
	// Expected format: <sanitized-folder>-<14 base36 chars>.
	// The folder name is filepath.Base(repo), sanitized.
	folderName := sanitizeFolderName(filepath.Base(repo))
	if !strings.HasPrefix(got, folderName+"-") {
		t.Errorf("expected slug to start with %q, got %q", folderName+"-", got)
	}
	hashPart := got[len(folderName)+1:]
	if len(hashPart) != 14 {
		t.Errorf("expected 14-char hash suffix, got %d chars (%q)", len(hashPart), hashPart)
	}
	for _, r := range hashPart {
		isBase36 := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z')
		if !isBase36 {
			t.Errorf("expected base36 hash, found %q in %q", r, hashPart)
		}
	}
}

func TestProjectSlugDeterministic(t *testing.T) {
	repo := initRepo(t)
	t.Chdir(repo)
	l := output.NewPrinter(os.Stderr, false)
	a := ProjectSlug(l)
	b := ProjectSlug(l)
	if a != b {
		t.Errorf("expected deterministic slug, got %q and %q", a, b)
	}
}

func TestPruneWorktreesCleansStaleEntries(t *testing.T) {
	repo := initRepo(t)
	wtDir := filepath.Join(t.TempDir(), "stale-wt")
	runGit(t, repo, "worktree", "add", "--detach", wtDir)
	if err := os.RemoveAll(wtDir); err != nil {
		t.Fatalf("remove worktree dir: %v", err)
	}
	out := runGit(t, repo, "worktree", "list")
	if !strings.Contains(out, "prunable") {
		t.Fatalf("expected prunable entry, got: %s", out)
	}
	if err := PruneWorktrees(repo); err != nil {
		t.Fatalf("PruneWorktrees: %v", err)
	}
	out = runGit(t, repo, "worktree", "list")
	if strings.Contains(out, "prunable") {
		t.Errorf("expected no prunable entries after prune, got: %s", out)
	}
}

func TestPruneWorktreesNoRepo(t *testing.T) {
	dir := t.TempDir()
	err := PruneWorktrees(dir)
	if err == nil {
		t.Error("expected error when not in a git repo")
	}
}
