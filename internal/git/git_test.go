package git

import (
	"path/filepath"
	"testing"
)

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
