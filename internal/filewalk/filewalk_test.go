package filewalk

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func collectRels(t *testing.T, root string, filter func(string, bool) bool) []string {
	t.Helper()
	var rels []string
	if err := Walk(root, filter, func(entry Entry) error {
		rels = append(rels, entry.Rel)
		return nil
	}); err != nil {
		t.Fatalf("Walk() error = %v", err)
	}
	slices.Sort(rels)
	return rels
}

func TestWalkVisitsEveryRegularFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "a")
	writeFile(t, filepath.Join(root, "sub", "b.txt"), "b")

	got := collectRels(t, root, func(string, bool) bool { return true })
	want := []string{"a.txt", filepath.Join("sub", "b.txt")}
	if !slices.Equal(got, want) {
		t.Errorf("visited %v, want %v", got, want)
	}
}

func TestWalkPrunesUnselectedDirectory(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "keep.txt"), "keep")
	writeFile(t, filepath.Join(root, "skip", "nested.txt"), "nested")

	filter := func(rel string, isDir bool) bool {
		return !isDir || rel != "skip"
	}
	got := collectRels(t, root, filter)
	if !slices.Equal(got, []string{"keep.txt"}) {
		t.Errorf("visited %v, want [keep.txt]", got)
	}
}

func TestWalkSkipsUnselectedFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "keep.txt"), "keep")
	writeFile(t, filepath.Join(root, "skip.txt"), "skip")

	got := collectRels(t, root, func(rel string, _ bool) bool { return rel != "skip.txt" })
	if !slices.Equal(got, []string{"keep.txt"}) {
		t.Errorf("visited %v, want [keep.txt]", got)
	}
}

func TestReadFileRejectsUnreadablePath(t *testing.T) {
	if _, _, ok := readFile(t.TempDir()); ok {
		t.Error("readFile(directory) ok = true, want false")
	}
	if _, _, ok := readFile(filepath.Join(t.TempDir(), "missing")); ok {
		t.Error("readFile(missing) ok = true, want false")
	}
}

func TestWalkSkipsUnreadableEntry(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "ok.txt"), "ok")
	if err := os.Symlink(filepath.Join(root, "missing"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}

	got := collectRels(t, root, func(string, bool) bool { return true })
	if !slices.Equal(got, []string{"ok.txt"}) {
		t.Errorf("visited %v, want [ok.txt] (dangling symlink skipped)", got)
	}
}

func TestWalkMissingRootIsNotAnError(t *testing.T) {
	var visited int
	err := Walk(filepath.Join(t.TempDir(), "absent"), func(string, bool) bool { return true }, func(Entry) error {
		visited++
		return nil
	})
	if err != nil {
		t.Fatalf("Walk() error = %v, want nil", err)
	}
	if visited != 0 {
		t.Errorf("visited %d entries, want 0", visited)
	}
}

func TestWalkVisitErrorAborts(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "a")
	sentinel := errors.New("stop")

	err := Walk(root, func(string, bool) bool { return true }, func(Entry) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Errorf("Walk() error = %v, want %v", err, sentinel)
	}
}
