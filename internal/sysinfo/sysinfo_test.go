package sysinfo

import (
	"testing"
)

func TestParseMemInfo(t *testing.T) {
	data := []byte("MemTotal:       16384000 kB\nMemFree:          123456 kB\nMemAvailable:    8000000 kB\n")
	totalKB, ok := parseMemInfo(data)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if totalKB != 16384000 {
		t.Errorf("expected 16384000, got %d", totalKB)
	}
}

func TestParseMemInfoMissing(t *testing.T) {
	data := []byte("MemFree: 123 kB\n")
	got, ok := parseMemInfo(data)
	if ok {
		t.Error("expected ok=false when MemTotal missing")
	}
	if got != 0 {
		t.Errorf("expected 0 KiB on failure, got %d", got)
	}
}

func TestParseMemInfoEmpty(t *testing.T) {
	got, ok := parseMemInfo(nil)
	if ok {
		t.Error("expected ok=false for empty input")
	}
	if got != 0 {
		t.Errorf("expected 0 KiB on failure, got %d", got)
	}
}

func TestParseMemInfoIgnoresNonMemTotalLines(t *testing.T) {
	data := []byte("MemFree: 123 kB\nMemTotal:       16384000 kB\n")
	totalKB, ok := parseMemInfo(data)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if totalKB != 16384000 {
		t.Errorf("expected 16384000, got %d", totalKB)
	}
}

func TestParseMemInfoMissingValue(t *testing.T) {
	data := []byte("MemTotal:\n")
	got, ok := parseMemInfo(data)
	if ok {
		t.Error("expected ok=false when MemTotal has no value field")
	}
	if got != 0 {
		t.Errorf("expected 0 KiB on failure, got %d", got)
	}
}

func TestParseMemInfoInvalidNumber(t *testing.T) {
	data := []byte("MemTotal: notanumber kB\n")
	got, ok := parseMemInfo(data)
	if ok {
		t.Error("expected ok=false when MemTotal value is not an integer")
	}
	if got != 0 {
		t.Errorf("expected 0 KiB on failure, got %d", got)
	}
}

func TestParseMemInfoTwoFields(t *testing.T) {
	data := []byte("MemTotal:\t16384000\n")
	totalKB, ok := parseMemInfo(data)
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
