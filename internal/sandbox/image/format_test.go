package image

import (
	"testing"
	"time"
)

func TestFormatImageTime(t *testing.T) {
	zero := time.Time{}
	if got := FormatImageTime(zero); got != "" {
		t.Errorf("FormatImageTime(zero) = %q, want empty", got)
	}
	tm := time.Date(2026, 8, 17, 10, 42, 36, 0, time.UTC)
	if got, want := FormatImageTime(tm), "2026-08-17 10:42:36"; got != want {
		t.Errorf("FormatImageTime() = %q, want %q", got, want)
	}
}
