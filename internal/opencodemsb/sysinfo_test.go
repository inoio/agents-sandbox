package opencodemsb

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

func TestNumCPUsAtLeastOne(t *testing.T) {
	cpus := NumCPUs()
	if cpus < 1 {
		t.Errorf("expected at least 1 CPU, got %d", cpus)
	}
}
