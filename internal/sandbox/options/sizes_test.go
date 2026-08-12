package options

import "testing"

func TestParseMemoryOK_ValidGiB(t *testing.T) {
	got, ok := ParseMemoryOK("4G")
	if !ok {
		t.Fatal("expected ok for valid GiB spec")
	}
	if got != 4096 {
		t.Errorf("expected 4096, got %d", got)
	}
}

func TestParseMemoryOK_ValidMB(t *testing.T) {
	got, ok := ParseMemoryOK("512M")
	if !ok {
		t.Fatal("expected ok for valid MB spec")
	}
	if got != 512 {
		t.Errorf("expected 512, got %d", got)
	}
}

func TestParseMemoryOK_Empty(t *testing.T) {
	got, ok := ParseMemoryOK("")
	if ok {
		t.Fatal("expected not ok for empty spec")
	}
	if got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestParseMemoryOK_Bogus(t *testing.T) {
	got, ok := ParseMemoryOK("bogus")
	if ok {
		t.Fatal("expected not ok for bogus spec")
	}
	if got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestParseMemoryOK_ValidLowercase(t *testing.T) {
	got, ok := ParseMemoryOK("2g")
	if !ok {
		t.Fatal("expected ok for valid lowercase GiB spec")
	}
	if got != 2048 {
		t.Errorf("expected 2048, got %d", got)
	}
}

func TestParseMemoryOK_ValidPlainNumber(t *testing.T) {
	got, ok := ParseMemoryOK("2048")
	if !ok {
		t.Fatal("expected ok for valid plain number")
	}
	if got != 2048 {
		t.Errorf("expected 2048, got %d", got)
	}
}

func TestParseMemoryOK_TrailingLetterNotGOrM(t *testing.T) {
	got, ok := ParseMemoryOK("10X")
	if ok {
		t.Fatal("expected not ok for trailing non-G/M letter")
	}
	if got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestParseMemoryOK_Whitespace(t *testing.T) {
	got, ok := ParseMemoryOK("  4G  ")
	if !ok {
		t.Fatal("expected ok for spec with whitespace")
	}
	if got != 4096 {
		t.Errorf("expected 4096, got %d", got)
	}

	_, ok = ParseMemoryOK("   ")
	if ok {
		t.Fatal("expected not ok for whitespace-only spec")
	}
}

func TestParseMemoryOK_DefaultMemoryFallsThrough(t *testing.T) {
	if ParseMemory("") != DefaultMemoryMiB {
		t.Errorf("expected default %d for empty spec", DefaultMemoryMiB)
	}
	if ParseMemory("bogus") != DefaultMemoryMiB {
		t.Errorf("expected default %d for bogus spec", DefaultMemoryMiB)
	}
}

func TestResolveTmpSizeMiB_DelegatesToParseMemoryOK(t *testing.T) {
	if got := ResolveTmpSizeMiB(""); got != DefaultTmpSizeMiB {
		t.Errorf("expected default %d for empty, got %d", DefaultTmpSizeMiB, got)
	}
	if got := ResolveTmpSizeMiB("bogus"); got != DefaultTmpSizeMiB {
		t.Errorf("expected default %d for bogus, got %d", DefaultTmpSizeMiB, got)
	}
	if got := ResolveTmpSizeMiB("4G"); got != 4096 {
		t.Errorf("expected 4096 for 4G, got %d", got)
	}
}
