package opener

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestOpenSpoolReportsMissingVolume(t *testing.T) {
	if _, err := openSpool(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want it to wrap fs.ErrNotExist", err)
	}
}

// TestSpoolOpenFlagsRequireTheSafetyBits pins the open flags: the spool
// directory must be opened as a directory without blocking, and a claimed file
// without following a symlink or blocking.
func TestSpoolOpenFlagsRequireTheSafetyBits(t *testing.T) {
	if spoolDirectoryFlags&syscall.O_DIRECTORY == 0 || spoolDirectoryFlags&syscall.O_NONBLOCK == 0 {
		t.Fatalf("spool directory flags %#x must set O_DIRECTORY and O_NONBLOCK", spoolDirectoryFlags)
	}
	if claimedFileFlags&syscall.O_NONBLOCK == 0 || claimedFileFlags&syscall.O_NOFOLLOW == 0 {
		t.Fatalf("claimed file flags %#x must set O_NONBLOCK and O_NOFOLLOW", claimedFileFlags)
	}
}

func TestSpoolEntriesListsEveryEntry(t *testing.T) {
	homeVolume, spoolDirectory := newSpoolDirectory(t)
	for _, identifier := range []string{"a1", "b2", "c3"} {
		writeRequest(t, spoolDirectory, identifier, "https://example.com")
	}
	spoolDirectoryHandle, err := openSpool(homeVolume)
	if err != nil {
		t.Fatal(err)
	}
	defer spoolDirectoryHandle.close()

	entries, err := spoolDirectoryHandle.entries()
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want every one of the three requests", len(entries))
	}
}

func TestSpoolEntriesSkipsMalformedNames(t *testing.T) {
	homeVolume, spoolDirectory := newSpoolDirectory(t)
	writeRequest(t, spoolDirectory, "good", "https://example.com")
	for _, name := range []string{"notes.txt", "abc", "weird name!"} {
		if err := os.WriteFile(filepath.Join(spoolDirectory, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	spoolDirectoryHandle, err := openSpool(homeVolume)
	if err != nil {
		t.Fatal(err)
	}
	defer spoolDirectoryHandle.close()

	entries, err := spoolDirectoryHandle.entries()
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	if len(entries) != 1 || entries[0].identifier != "good" {
		t.Fatalf("entries = %+v, want only the well-formed request", entries)
	}
}

func TestParseSpoolNameRejectsNamesWithoutASuffix(t *testing.T) {
	for _, name := range []string{"abc", "notes.txt", "weird name!", ""} {
		if _, ok := parseSpoolName(name); ok {
			t.Fatalf("parseSpoolName(%q) = ok, want false", name)
		}
	}
}

func TestReadClaimedReportsMissingClaim(t *testing.T) {
	homeVolume, _ := newSpoolDirectory(t)
	spoolDirectory, err := openSpool(homeVolume)
	if err != nil {
		t.Fatal(err)
	}
	defer spoolDirectory.close()

	if _, err := spoolDirectory.readClaimed("gone"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want it to wrap fs.ErrNotExist", err)
	}
}

func TestReadClaimedRefusesNonRegularFile(t *testing.T) {
	homeVolume, spoolDirectory := newSpoolDirectory(t)
	if err := syscall.Mkfifo(filepath.Join(spoolDirectory, "fifo"+claimedSuffix), 0o600); err != nil {
		t.Fatal(err)
	}
	spoolDirectoryHandle, err := openSpool(homeVolume)
	if err != nil {
		t.Fatal(err)
	}
	defer spoolDirectoryHandle.close()

	if _, err := spoolDirectoryHandle.readClaimed("fifo"); err == nil ||
		!strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("err = %v, want a not-a-regular-file refusal", err)
	}
}

func TestReadClaimedReadsAtMostMaxTargetBytesPlusOne(t *testing.T) {
	homeVolume, spoolDirectory := newSpoolDirectory(t)
	content := strings.Repeat("a", 8198)
	if err := os.WriteFile(filepath.Join(spoolDirectory, "long"+claimedSuffix), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	spoolDirectoryHandle, err := openSpool(homeVolume)
	if err != nil {
		t.Fatal(err)
	}
	defer spoolDirectoryHandle.close()

	got, err := spoolDirectoryHandle.readClaimed("long")
	if err != nil {
		t.Fatalf("readClaimed: %v", err)
	}
	if len(got) != 8193 {
		t.Fatalf("read %d bytes, want a hard stop at 8193", len(got))
	}
}
