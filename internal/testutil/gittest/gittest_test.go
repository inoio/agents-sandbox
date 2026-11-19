package gittest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCommitExposesFileContent(t *testing.T) {
	repo := New(t)
	repo.Write("f", "a\n")
	repo.Commit("base")

	content, ok := repo.FileAt("main", "f")
	if !ok {
		t.Fatal("f not found at main")
	}
	if content != "a\n" {
		t.Errorf("FileAt(main, f) = %q, want %q", content, "a\n")
	}
}

func TestSetBranchAndCheckoutMoveTheWorktree(t *testing.T) {
	repo := New(t)
	repo.Write("f", "a\n")
	base := repo.Commit("base")
	repo.SetBranch("release/0.1", base)

	repo.Write("f", "b\n")
	repo.Commit("fix")
	repo.Checkout("release/0.1")

	onDisk, err := os.ReadFile(filepath.Join(repo.Dir(), "f"))
	if err != nil {
		t.Fatalf("read worktree f: %v", err)
	}
	if string(onDisk) != "a\n" {
		t.Errorf("release worktree f = %q, want %q", string(onDisk), "a\n")
	}
}

func TestMergeCommitHasTwoParentsAndSourceTree(t *testing.T) {
	repo := New(t)
	repo.Write("f", "a\n")
	base := repo.Commit("base")
	repo.SetBranch("release/0.1", base)

	repo.SetBranch("topic", base)
	repo.Checkout("topic")
	repo.Write("f", "b\n")
	repo.Commit("first")
	repo.Write("g", "c\n")
	second := repo.Commit("second")

	repo.Checkout("main")
	merge := repo.MergeCommit("Merge pull request #7", base, second)

	commit, err := repo.repo.CommitObject(merge)
	if err != nil {
		t.Fatalf("read merge commit: %v", err)
	}
	if got := commit.ParentHashes; len(got) != 2 || got[0] != base || got[1] != second {
		t.Errorf("merge parents = %v, want [%s %s]", got, base, second)
	}
	content, ok := repo.FileAt("main", "g")
	if !ok || content != "c\n" {
		t.Errorf("FileAt(main, g) = %q,%v, want %q,true", content, ok, "c\n")
	}
}

func TestPushPublishesEveryBranch(t *testing.T) {
	repo := New(t)
	repo.Write("f", "a\n")
	base := repo.Commit("base")
	repo.SetBranch("release/0.1", base)
	origin := repo.AddBareOrigin()
	repo.Push(t)

	refs := OriginRefs(t, origin)
	for _, want := range []string{"refs/heads/main", "refs/heads/release/0.1"} {
		if !slices.Contains(refs, want) {
			t.Errorf("origin refs %v missing %s", refs, want)
		}
	}
}

func TestOriginRangeReflectsTheMergedTopic(t *testing.T) {
	repo := New(t)
	repo.Write("f", "a\n")
	base := repo.Commit("base")
	repo.SetBranch("release/0.1", base)

	repo.SetBranch("topic", base)
	repo.Checkout("topic")
	repo.Write("f", "b\n")
	repo.Commit("first")
	repo.Write("g", "c\n")
	second := repo.Commit("second")

	repo.Checkout("main")
	repo.MergeCommit("Merge pull request #7", base, second)
	origin := repo.AddBareOrigin()
	repo.Push(t)

	if got := OriginCount(t, origin, "release/0.1", "main"); got != 3 {
		t.Errorf("OriginCount(release/0.1..main) = %d, want 3", got)
	}
	messages := strings.Join(OriginMessages(t, origin, "release/0.1", "main"), "\n")
	for _, want := range []string{"first", "second", "Merge pull request #7"} {
		if !strings.Contains(messages, want) {
			t.Errorf("OriginMessages = %q, missing %q", messages, want)
		}
	}
}

func TestHeadReturnsTheCurrentCommit(t *testing.T) {
	repo := New(t)
	repo.Write("f", "a\n")
	hash := repo.Commit("base")

	head, err := repo.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head != hash {
		t.Errorf("Head() = %s, want %s", head, hash)
	}
}

func TestRemoveStagesADeletion(t *testing.T) {
	repo := New(t)
	repo.Write("f", "a\n")
	repo.Commit("base")

	repo.Remove("f")
	repo.Commit("remove f")

	if _, ok := repo.FileAt("main", "f"); ok {
		t.Error("f still present after removal commit")
	}
}

func TestOriginFileReadsAPushedBlob(t *testing.T) {
	repo := New(t)
	repo.Write("changelog.d/7.fixed.md", "The fix.\n")
	repo.Commit("base")
	origin := repo.AddBareOrigin()
	repo.Push(t)

	content, ok := OriginFile(t, origin, "main", "changelog.d/7.fixed.md")
	if !ok {
		t.Fatal("fragment not found on origin")
	}
	if content != "The fix.\n" {
		t.Errorf("OriginFile = %q, want %q", content, "The fix.\n")
	}
	if _, ok := OriginFile(t, origin, "main", "changelog.d/99.fixed.md"); ok {
		t.Error("unexpected fragment at 99")
	}
}
