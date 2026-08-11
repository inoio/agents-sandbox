package options

import "testing"

func TestParseMemoryGigabytes(t *testing.T) {
	got := ParseMemory("4G")
	if got != 4096 {
		t.Errorf("expected 4096, got %d", got)
	}
}

func TestParseMemoryMegabytes(t *testing.T) {
	got := ParseMemory("512M")
	if got != 512 {
		t.Errorf("expected 512, got %d", got)
	}
}

func TestParseMemoryPlainNumber(t *testing.T) {
	got := ParseMemory("2048")
	if got != 2048 {
		t.Errorf("expected 2048, got %d", got)
	}
}

func TestParseMemoryLowercase(t *testing.T) {
	got := ParseMemory("2g")
	if got != 2048 {
		t.Errorf("expected 2048, got %d", got)
	}
}

func TestResolveTmpSizeDefaultsWhenEmpty(t *testing.T) {
	got := ResolveTmpSizeMiB("")
	if got != DefaultTmpSizeMiB {
		t.Errorf("expected default %d, got %d", DefaultTmpSizeMiB, got)
	}
}

func TestResolveTmpSizeParsesSpec(t *testing.T) {
	got := ResolveTmpSizeMiB("4G")
	if got != 4096 {
		t.Errorf("expected 4096, got %d", got)
	}
}
