package reprovision

import "testing"

func TestSizeChangeFormatting(t *testing.T) {
	// user-provided "2G" parses to 2048 MiB
	c := SizeChange("/tmp tmpfs size", 2048, 4096, "2G", "4G")
	if c.Label != "/tmp tmpfs size" || c.Old != "2G" || c.New != "4G" {
		t.Errorf("unexpected SizeChange: %+v", c)
	}
}

func TestParseSizeSpec(t *testing.T) {
	if v, ok := ParseSizeSpec("4G"); !ok || v != 4096 {
		t.Errorf("4G -> 4096, ok; got %d %v", v, ok)
	}
	if v, ok := ParseSizeSpec(""); ok || v != 0 {
		t.Errorf("empty -> not ok; got %d %v", v, ok)
	}
	if _, ok := ParseSizeSpec("bogus"); ok {
		t.Error("bogus should not parse")
	}
}

func TestFormatSizeSpecVerbatimWhenMatches(t *testing.T) {
	if got := FormatSizeSpec(2048, "2G"); got != "2G" {
		t.Errorf("expected verbatim 2G, got %q", got)
	}
	if got := FormatSizeSpec(3500, "2G"); got != "3500M" {
		t.Errorf("expected fallback 3500M, got %q", got)
	}
	if got := FormatSizeSpec(2048, ""); got != "2048M" {
		t.Errorf("expected 2048M for empty raw, got %q", got)
	}
}
