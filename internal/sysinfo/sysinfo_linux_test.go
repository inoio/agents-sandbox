//go:build linux

package sysinfo

import (
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

	t.Run("read error", func(t *testing.T) {
		procMeminfoPath = filepath.Join(dir, "does-not-exist")
		if got := TotalMemoryGiB(); got != 0 {
			t.Errorf("TotalMemoryGiB() with missing file = %d, want 0", got)
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
