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
	_, ok := parseMemInfo(data)
	if ok {
		t.Error("expected ok=false when MemTotal missing")
	}
}

func TestParseMemInfoEmpty(t *testing.T) {
	_, ok := parseMemInfo(nil)
	if ok {
		t.Error("expected ok=false for empty input")
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
	_, ok := parseMemInfo(data)
	if ok {
		t.Error("expected ok=false when MemTotal has no value field")
	}
}

func TestParseMemInfoInvalidNumber(t *testing.T) {
	data := []byte("MemTotal: notanumber kB\n")
	_, ok := parseMemInfo(data)
	if ok {
		t.Error("expected ok=false when MemTotal value is not an integer")
	}
}

func TestNumCPUsAtLeastOne(t *testing.T) {
	cpus := NumCPUs()
	if cpus < 1 {
		t.Errorf("expected at least 1 CPU, got %d", cpus)
	}
}
