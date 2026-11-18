package filewalk

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/projectguard"
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

func TestWalkDoesNotConsultFilterForRoot(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "keep.txt"), "keep")

	// Walk skips the root before filter runs, so a filter that would reject
	// the root's "." relative path must not prune the whole tree.
	filter := func(rel string, _ bool) bool { return rel != "." }
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
	if _, _, _, _, ok := readFile(t.TempDir(), 0); ok {
		t.Error("readFile(directory) ok = true, want false")
	}
	if _, _, _, _, ok := readFile(filepath.Join(t.TempDir(), "missing"), 0); ok {
		t.Error("readFile(missing) ok = true, want false")
	}
}

// TestWalkCappedRecordsOverCapFileWithoutReading pins the content cap: a file
// larger than the cap is visited with its metadata but no data, so a caller can
// record it opaque without the walk loading it into memory.
func TestWalkCappedRecordsOverCapFileWithoutReading(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "small.txt"), "small")
	writeFile(t, filepath.Join(root, "large.txt"), strings.Repeat("x", 64))

	const contentCap = 16
	entries := map[string]Entry{}
	if err := WalkCapped(root, contentCap, func(string, bool) bool { return true }, func(entry Entry) error {
		entries[entry.Rel] = entry
		return nil
	}); err != nil {
		t.Fatalf("WalkCapped() error = %v", err)
	}

	small, ok := entries["small.txt"]
	if !ok {
		t.Fatal("small.txt not visited")
	}
	if string(small.Data) != "small" || small.Size != 5 {
		t.Errorf("small.txt = %q size %d, want %q size 5", small.Data, small.Size, "small")
	}

	large, ok := entries["large.txt"]
	if !ok {
		t.Fatal("large.txt not visited: an over-cap file must still be recorded")
	}
	if large.Data != nil {
		t.Errorf("large.txt data = %q, want nil (over the cap)", large.Data)
	}
	if large.Size != 64 {
		t.Errorf("large.txt size = %d, want 64", large.Size)
	}
	if !large.Mode.IsRegular() {
		t.Errorf("large.txt mode = %v, want a regular file", large.Mode)
	}
	if large.ModTime.IsZero() {
		t.Error("large.txt mtime is zero, want the stat mtime")
	}
}

func TestReadFileReportsContentsAndPermissionBits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "readable")
	writeFile(t, path, "hello")
	// chmod is not masked by umask, so the asserted bits are exactly what
	// readFile must return.
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}

	data, mode, _, _, ok := readFile(path, 0)
	if !ok {
		t.Fatal("readFile(readable) ok = false, want true")
	}
	if string(data) != "hello" {
		t.Errorf("readFile data = %q, want %q", data, "hello")
	}
	if want := fs.FileMode(0o640); mode != want {
		t.Errorf("readFile mode = %v, want %v", mode, want)
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

func TestWalkCallsFilterOnceForADirectoryThatVanishes(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o750); err != nil {
		t.Fatal(err)
	}

	// Removing the directory makes WalkDir's ReadDir fail, so it invokes the
	// callback a second time carrying the error. The err guard must skip that
	// callback; otherwise the filter runs again for the same entry.
	var subCalls int
	filter := func(rel string, isDir bool) bool {
		if rel == "sub" && isDir {
			subCalls++
			_ = os.Remove(sub) // idempotent: a repeat call finds it already gone
		}
		return true
	}
	if err := Walk(root, filter, func(Entry) error { return nil }); err != nil {
		t.Fatalf("Walk() error = %v, want nil", err)
	}
	if subCalls != 1 {
		t.Errorf("filter called %d times for the vanished directory, want 1", subCalls)
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

	data, mode, _, _, ok := readFile(path, 0)
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

	data, mode, _, _, ok := readFile(path, 0)
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

// TestWalkSkipsAFileChangedSinceItsApproval pins that the walk reads project
// files through the approval: a changed file is not visited, and the walk
// reports it.
func TestWalkSkipsAFileChangedSinceItsApproval(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "plugin.ts")
	t.Cleanup(projectguard.Install([]string{root}, map[string]projectguard.Approval{
		path: projectguard.ApprovalOf([]byte("approved")),
	}))
	writeFile(t, path, "changed")
	var visited []string
	err := Walk(root, func(string, bool) bool { return true }, func(entry Entry) error {
		visited = append(visited, entry.Rel)
		return nil
	})
	if len(visited) != 0 || !errors.Is(err, projectguard.ErrNotApproved) {
		t.Errorf("Walk visited %q, returned %v; want the changed file refused", visited, err)
	}
}

// TestWalkRefusesAnApprovedFileThatIsGone pins that the walk reports an
// approved file it would have taken but did not reach, while one its filter
// leaves out may be gone.
func TestWalkRefusesAnApprovedFileThatIsGone(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(projectguard.Install([]string{root}, map[string]projectguard.Approval{
		filepath.Join(root, "plugins", "a.ts"):       projectguard.ApprovalOf([]byte("a")),
		filepath.Join(root, "node_modules", "x.js"):  projectguard.ApprovalOf([]byte("x")),
		filepath.Join(root, "plugins", "skip.local"): projectguard.ApprovalOf([]byte("s")),
	}))
	filter := func(rel string, isDir bool) bool {
		if isDir {
			return rel != "node_modules"
		}
		return filepath.Ext(rel) != ".local"
	}
	if err := os.MkdirAll(filepath.Join(root, "plugins"), 0o750); err != nil {
		t.Fatal(err)
	}
	err := Walk(root, filter, func(Entry) error { return nil })
	if !errors.Is(err, projectguard.ErrNotApproved) || !strings.Contains(err.Error(), "a.ts") {
		t.Errorf("Walk = %v, want the gone plugins/a.ts refused", err)
	}
	writeFile(t, filepath.Join(root, "plugins", "a.ts"), "a")
	t.Cleanup(projectguard.Install([]string{root}, map[string]projectguard.Approval{
		filepath.Join(root, "plugins", "a.ts"):       projectguard.ApprovalOf([]byte("a")),
		filepath.Join(root, "node_modules", "x.js"):  projectguard.ApprovalOf([]byte("x")),
		filepath.Join(root, "plugins", "skip.local"): projectguard.ApprovalOf([]byte("s")),
	}))
	if err := Walk(root, filter, func(Entry) error { return nil }); err != nil {
		t.Errorf("Walk with only filtered-out files gone = %v, want nil", err)
	}
}

// TestWalkRefusesAnApprovedFileReplacedByADanglingLink pins that a listed
// entry counts as reached only once read: an approved file swapped for a link
// to nothing is refused, not skipped.
func TestWalkRefusesAnApprovedFileReplacedByADanglingLink(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "plugin.ts")
	t.Cleanup(projectguard.Install([]string{root}, map[string]projectguard.Approval{
		path: projectguard.ApprovalOf([]byte("approved")),
	}))
	if err := os.Symlink(filepath.Join(root, "nowhere"), path); err != nil {
		t.Fatal(err)
	}
	if err := Walk(
		root,
		func(string, bool) bool { return true },
		func(Entry) error { return nil },
	); !errors.Is(
		err,
		projectguard.ErrNotApproved,
	) {
		t.Errorf("Walk = %v, want the dangling approved file refused", err)
	}
}

// TestReadFileStatErrorReturnsAllZeroValues: a path that cannot be statted
// yields every result at its zero value, even when a content cap is set (so the
// cap check must not dereference a nil FileInfo).
func TestReadFileStatErrorReturnsAllZeroValues(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")

	data, mode, size, modTime, ok := readFile(missing, 16)

	if ok {
		t.Error("readFile(missing) ok = true, want false")
	}
	if data != nil {
		t.Errorf("readFile(missing) data = %q, want nil", data)
	}
	if mode != chmod0000 {
		t.Errorf("readFile(missing) mode = %v, want 0", mode)
	}
	if size != 0 {
		t.Errorf("readFile(missing) size = %d, want 0", size)
	}
	if !modTime.IsZero() {
		t.Errorf("readFile(missing) modtime = %v, want zero", modTime)
	}
}

// TestReadFileReadErrorReturnsAllZeroValues: a path that stats but cannot be
// read (a directory) yields every result at its zero value.
func TestReadFileReadErrorReturnsAllZeroValues(t *testing.T) {
	data, mode, size, modTime, ok := readFile(t.TempDir(), 0)

	if ok {
		t.Error("readFile(directory) ok = true, want false")
	}
	if data != nil {
		t.Errorf("readFile(directory) data = %q, want nil", data)
	}
	if mode != chmod0000 {
		t.Errorf("readFile(directory) mode = %v, want 0", mode)
	}
	if size != 0 {
		t.Errorf("readFile(directory) size = %d, want 0", size)
	}
	if !modTime.IsZero() {
		t.Errorf("readFile(directory) modtime = %v, want zero", modTime)
	}
}

// TestReadFileReportsModTime: a read file carries its own modification time, not
// the zero time.
func TestReadFileReportsModTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	writeFile(t, path, "hi")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	_, _, _, modTime, ok := readFile(path, 0)

	if !ok {
		t.Fatal("readFile(readable) ok = false, want true")
	}
	if !modTime.Equal(info.ModTime()) {
		t.Errorf("readFile modtime = %v, want %v", modTime, info.ModTime())
	}
}

// TestWalkCappedPositiveCapIsDistinctFromZero: a positive cap bounds the read, so
// a two-byte file over a one-byte cap is recorded opaque, not read whole.
func TestWalkCappedPositiveCapIsDistinctFromZero(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "two"), "12")

	entries := map[string]Entry{}
	if err := WalkCapped(root, 1, func(string, bool) bool { return true }, func(entry Entry) error {
		entries[entry.Rel] = entry
		return nil
	}); err != nil {
		t.Fatalf("WalkCapped() error = %v", err)
	}

	two, ok := entries["two"]
	if !ok {
		t.Fatal("two not visited")
	}
	if two.Data != nil {
		t.Errorf("two data = %q, want nil (2 bytes over a 1-byte cap)", two.Data)
	}
}

// TestWalkCappedBoundaryAndMode pins that a file exactly at the cap is read
// whole, and that an over-cap file still reports its permission bits.
func TestWalkCappedBoundaryAndMode(t *testing.T) {
	root := t.TempDir()
	atCap := filepath.Join(root, "atcap")
	writeFile(t, atCap, "12345")
	overCap := filepath.Join(root, "overcap")
	writeFile(t, overCap, "123456")
	for _, path := range []string{atCap, overCap} {
		if err := os.Chmod(path, 0o640); err != nil {
			t.Fatal(err)
		}
	}

	entries := map[string]Entry{}
	if err := WalkCapped(root, 5, func(string, bool) bool { return true }, func(entry Entry) error {
		entries[entry.Rel] = entry
		return nil
	}); err != nil {
		t.Fatalf("WalkCapped() error = %v", err)
	}

	at, ok := entries["atcap"]
	if !ok {
		t.Fatal("atcap not visited")
	}
	if at.Data == nil {
		t.Error("a file exactly at the cap must be read")
	}
	if want := fs.FileMode(0o640); at.Mode != want {
		t.Errorf("atcap mode = %v, want %v", at.Mode, want)
	}

	over, ok := entries["overcap"]
	if !ok {
		t.Fatal("overcap not visited")
	}
	if over.Data != nil {
		t.Error("an over-cap file must not be read")
	}
	if want := fs.FileMode(0o640); over.Mode != want {
		t.Errorf("overcap mode = %v, want %v", over.Mode, want)
	}
}
