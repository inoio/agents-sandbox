//go:build linux

package sysinfo

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTotalMemoryGiB(t *testing.T) {
	dir := t.TempDir()

	t.Cleanup(func() { procMeminfoPath = "/proc/meminfo" })
	procMeminfoPath = filepath.Join(dir, "meminfo")

	t.Run("success", func(t *testing.T) {
		if err := os.WriteFile(procMeminfoPath, []byte("MemTotal:       16384000 kB\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := TotalMemoryGiB(); got != 15 {
			t.Errorf("TotalMemoryGiB() = %d, want 15 (16384000 kB / 1024 / 1024)", got)
		}
	})

	t.Run("divisor 1023-sensitive", func(t *testing.T) {
		// 2093058 kB = 1 GiB (floor); a 1023*1023 divisor would report 2.
		if err := os.WriteFile(procMeminfoPath, []byte("MemTotal: 2093058 kB\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := TotalMemoryGiB(); got != 1 {
			t.Errorf("TotalMemoryGiB() = %d, want 1 (2093058 kB / 1024 / 1024)", got)
		}
	})

	t.Run("divisor 1025-sensitive", func(t *testing.T) {
		// 2097152 kB = 2 GiB; a 1025*1025 divisor would report 1.
		if err := os.WriteFile(procMeminfoPath, []byte("MemTotal: 2097152 kB\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := TotalMemoryGiB(); got != 2 {
			t.Errorf("TotalMemoryGiB() = %d, want 2 (2097152 kB / 1024 / 1024)", got)
		}
	})

	t.Run("read error", func(t *testing.T) {
		procMeminfoPath = filepath.Join(dir, "does-not-exist")
		if got := TotalMemoryGiB(); got != 0 {
			t.Errorf("TotalMemoryGiB() with missing file = %d, want 0", got)
		}
	})

	t.Run("partial read", func(t *testing.T) {
		// os.ReadFile can return the bytes read before an error; a partial
		// MemTotal line must be discarded, not parsed.
		restore := readMeminfo
		readMeminfo = func(string) ([]byte, error) {
			return []byte("MemTotal: 2097152 kB\n"), errors.New("partial read")
		}
		t.Cleanup(func() { readMeminfo = restore })
		if got := TotalMemoryGiB(); got != 0 {
			t.Errorf("TotalMemoryGiB() with a partial read = %d, want 0", got)
		}
	})

	t.Run("parse error", func(t *testing.T) {
		if err := os.WriteFile(procMeminfoPath, []byte("MemFree: 123 kB\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := TotalMemoryGiB(); got != 0 {
			t.Errorf("TotalMemoryGiB() without MemTotal = %d, want 0", got)
		}
	})
}
