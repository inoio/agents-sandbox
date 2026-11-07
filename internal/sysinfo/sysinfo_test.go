package sysinfo

import (
	"errors"
	"math"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestParseMemInfo(t *testing.T) {
	data := []byte("MemTotal:       16384000 kB\nMemFree:          123456 kB\nMemAvailable:    8000000 kB\n")
	totalKB, ok := parseMemInfo(data, memTotalField)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if totalKB != 16384000 {
		t.Errorf("expected 16384000, got %d", totalKB)
	}
}

func TestParseMemInfoMissing(t *testing.T) {
	data := []byte("MemFree: 123 kB\n")
	got, ok := parseMemInfo(data, memTotalField)
	if ok {
		t.Error("expected ok=false when MemTotal missing")
	}
	if got != 0 {
		t.Errorf("expected 0 KiB on failure, got %d", got)
	}
}

func TestParseMemInfoEmpty(t *testing.T) {
	got, ok := parseMemInfo(nil, memTotalField)
	if ok {
		t.Error("expected ok=false for empty input")
	}
	if got != 0 {
		t.Errorf("expected 0 KiB on failure, got %d", got)
	}
}

func TestParseMemInfoIgnoresNonMemTotalLines(t *testing.T) {
	data := []byte("MemFree: 123 kB\nMemTotal:       16384000 kB\n")
	totalKB, ok := parseMemInfo(data, memTotalField)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if totalKB != 16384000 {
		t.Errorf("expected 16384000, got %d", totalKB)
	}
}

func TestParseMemInfoMissingValue(t *testing.T) {
	data := []byte("MemTotal:\n")
	got, ok := parseMemInfo(data, memTotalField)
	if ok {
		t.Error("expected ok=false when MemTotal has no value field")
	}
	if got != 0 {
		t.Errorf("expected 0 KiB on failure, got %d", got)
	}
}

func TestParseMemInfoInvalidNumber(t *testing.T) {
	data := []byte("MemTotal: notanumber kB\n")
	got, ok := parseMemInfo(data, memTotalField)
	if ok {
		t.Error("expected ok=false when MemTotal value is not an integer")
	}
	if got != 0 {
		t.Errorf("expected 0 KiB on failure, got %d", got)
	}
}

func TestParseMemInfoTwoFields(t *testing.T) {
	data := []byte("MemTotal:\t16384000\n")
	totalKB, ok := parseMemInfo(data, memTotalField)
	if !ok {
		t.Fatal("expected ok=true for a two-field MemTotal line")
	}
	if totalKB != 16384000 {
		t.Errorf("expected 16384000, got %d", totalKB)
	}
}

func TestNumCPUsAtLeastOne(t *testing.T) {
	cpus := NumCPUs()
	if cpus < 1 {
		t.Errorf("expected at least 1 CPU, got %d", cpus)
	}
}

func TestClampNumCPUs(t *testing.T) {
	// Values are literals, not maxCPUs-relative, so the bound itself is pinned:
	// a maxCPUs of 254 or 256 must fail one of these cases.
	cases := []struct {
		in   int
		want uint8
	}{
		{0, 1},
		{1, 1},
		{2, 2},
		{254, 254},
		{255, 255},
		{256, 255},
		{1 << 20, 255},
	}
	for _, c := range cases {
		if got := clampNumCPUs(c.in); got != c.want {
			t.Errorf("clampNumCPUs(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestParseMemInfoReadsTheNamedField pins that the parser reads the field it is
// asked for, so MemAvailable is not confused with MemTotal or MemFree.
func TestParseMemInfoReadsTheNamedField(t *testing.T) {
	data := []byte("MemTotal:       16384000 kB\nMemFree:          123456 kB\nMemAvailable:    8000000 kB\n")
	if got, ok := parseMemInfo(data, memAvailableField); !ok || got != 8000000 {
		t.Errorf("parseMemInfo(MemAvailable) = %d, %v; want 8000000, true", got, ok)
	}
}

// TestFreeDiskMiB pins the host free-space read: an existing directory and a
// not-yet-created path below it report their filesystem's free space.
func TestFreeDiskMiB(t *testing.T) {
	dir := t.TempDir()
	free, ok := FreeDiskMiB(dir)
	if !ok {
		t.Fatalf("FreeDiskMiB(%q) reported unknown", dir)
	}
	if below, belowOK := FreeDiskMiB(filepath.Join(dir, "not", "created")); !belowOK || below == 0 && free != 0 {
		t.Errorf("FreeDiskMiB(missing path) = %d, %v; want its existing ancestor's free space", below, belowOK)
	}
}

// TestFreeDiskMiBWithoutAnExistingAncestor pins that a path with no existing
// ancestor (a relative path that cannot be stat'ed is resolved upward to ".")
// still resolves, and that a failing stat at every level reports unknown.
func TestFreeDiskMiBWithoutAnExistingAncestor(t *testing.T) {
	original := statfs
	statfs = func(string, *unix.Statfs_t) error { return errors.New("no filesystem") }
	t.Cleanup(func() { statfs = original })
	if free, ok := FreeDiskMiB("/a/b"); ok || free != 0 {
		t.Errorf("FreeDiskMiB with failing stats = %d, %v; want 0, false", free, ok)
	}
}

// TestFreeDiskMiBConvertsBlocksToMiB pins the arithmetic: available blocks
// times the block size, in whole MiB, saturating at the uint32 maximum both
// when the MiB count exceeds it and when the byte count overflows 64 bits.
func TestFreeDiskMiBConvertsBlocksToMiB(t *testing.T) {
	original := statfs
	t.Cleanup(func() { statfs = original })
	for _, tc := range []struct {
		available uint64
		want      uint32
	}{
		{available: 2560, want: 10},
		{available: 1 << 40, want: math.MaxUint32},
		{available: 1 << 52, want: math.MaxUint32},
	} {
		statfs = func(_ string, stat *unix.Statfs_t) error {
			stat.Bavail = tc.available
			stat.Bsize = 4096
			return nil
		}
		if got, ok := FreeDiskMiB("/any"); !ok || got != tc.want {
			t.Errorf("FreeDiskMiB with %d free 4 KiB blocks = %d, %v; want %d, true", tc.available, got, ok, tc.want)
		}
	}
}
