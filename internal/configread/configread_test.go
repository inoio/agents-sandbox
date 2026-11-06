package configread_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configread"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestMaxBytesIsSixteenMiB pins the documented untrusted-read bound so the
// constant itself cannot drift.
func TestMaxBytesIsSixteenMiB(t *testing.T) {
	if configread.MaxBytes != 16<<20 {
		t.Errorf("MaxBytes = %d, want %d (16 MiB)", configread.MaxBytes, 16<<20)
	}
}

func TestReadFileReadsWithinTheBound(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "config.yaml", "memory: 4G\n")

	data, err := configread.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "memory: 4G\n" {
		t.Errorf("data = %q, want the file contents", data)
	}
}

func TestReadFileAcceptsExactlyMaxBytes(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "at.yaml", strings.Repeat("x", configread.MaxBytes))

	data, err := configread.ReadFile(filepath.Join(dir, "at.yaml"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) != configread.MaxBytes {
		t.Errorf("len(data) = %d, want %d", len(data), configread.MaxBytes)
	}
}

func TestReadFileRejectsAnOversizedFile(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "big.yaml", strings.Repeat("x", configread.MaxBytes+1))

	_, err := configread.ReadFile(filepath.Join(dir, "big.yaml"))
	if !errors.Is(err, configread.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestReadFileReportsAMissingFile(t *testing.T) {
	_, err := configread.ReadFile(filepath.Join(t.TempDir(), "absent.yaml"))
	if err == nil {
		t.Fatal("ReadFile = nil error, want the open failure")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want it to wrap os.ErrNotExist", err)
	}
}

// TestReadFileReportsAReadError pins that a read failure after the open is
// surfaced rather than yielding an empty result: a directory opens but cannot be
// read.
func TestReadFileReportsAReadError(t *testing.T) {
	if _, err := configread.ReadFile(t.TempDir()); err == nil {
		t.Fatal("ReadFile(dir) = nil error, want the read failure")
	}
}
