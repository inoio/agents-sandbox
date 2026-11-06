package configread_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configread"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

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
	if _, err := configread.ReadFile(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Fatal("ReadFile = nil error, want the open failure")
	}
}
