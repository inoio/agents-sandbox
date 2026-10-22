package filewalk

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// chmod0000 grants the mode its test needs. Modes do not restrict root, so a
// root-run test cannot observe an unreadable file and skips instead.
const chmod0000 = 0o000

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

func TestReadFileRejectsUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file modes do not restrict root, so a root-run test cannot make a file unreadable")
	}
	path := filepath.Join(t.TempDir(), "locked")
	writeFile(t, path, "locked")
	if err := os.Chmod(path, chmod0000); err != nil {
		t.Fatal(err)
	}

	data, mode, ok := readFile(path)
	if ok {
		t.Errorf("readFile(unreadable) ok = true, want false")
	}
	if data != nil {
		t.Errorf("readFile(unreadable) data = %q, want nil", data)
	}
	if want := fs.FileMode(chmod0000); mode != want {
		t.Errorf("readFile(unreadable) mode = %v, want %v", mode, want)
	}
}

func TestReadFileReportsAnUnstatablePathAsZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing")

	data, mode, ok := readFile(path)
	if ok {
		t.Errorf("readFile(missing) ok = true, want false")
	}
	if data != nil {
		t.Errorf("readFile(missing) data = %q, want nil", data)
	}
	if mode != chmod0000 {
		t.Errorf("readFile(missing) mode = %v, want 0", mode)
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
