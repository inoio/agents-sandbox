package opener

import (
	"strings"
	"testing"
)

// TestReportBoundsRefusalsAtTheLimit pins the refusal cap with literals, so the
// boundary does not move with the constant.
func TestReportBoundsRefusalsAtTheLimit(t *testing.T) {
	var report Report
	for range 10 {
		report.addRefusal("t", "r")
	}
	if len(report.Refusals) != 10 || report.Omitted != 0 {
		t.Fatalf("refusals %d omitted %d, want 10 and 0", len(report.Refusals), report.Omitted)
	}
	report.addRefusal("t", "r")
	if len(report.Refusals) != 10 || report.Omitted != 1 {
		t.Fatalf("refusals %d omitted %d, want 10 and 1", len(report.Refusals), report.Omitted)
	}
}

// TestShortenBoundaries pins the shortening limit with literal lengths.
func TestShortenBoundaries(t *testing.T) {
	atLimit := strings.Repeat("a", 200)
	if got := shorten(atLimit); got != atLimit {
		t.Fatalf("shorten at the limit changed a %d-char string", len(atLimit))
	}
	overLimit := strings.Repeat("a", 201)
	if got := shorten(overLimit); got != strings.Repeat("a", 200)+"…" {
		t.Fatalf("shorten over the limit = %q", got)
	}
}
