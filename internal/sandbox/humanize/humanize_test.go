package humanize

import (
	"testing"
	"time"
)

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1 KiB"},
		{1536, "1.5 KiB"},
		{1024 * 1024, "1 MiB"},
		{1234567, "1.2 MiB"},
		{1024 * 1024 * 1024, "1 GiB"},
		{3 * 1024 * 1024 * 1024, "3 GiB"},
		{2254857830, "2.1 GiB"},
	}
	for _, tc := range cases {
		if got := FormatBytes(tc.in); got != tc.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatTimestamp(t *testing.T) {
	if got := FormatTimestamp(time.Time{}); got != "" {
		t.Errorf("FormatTimestamp(zero) = %q, want empty", got)
	}
	tm := time.Date(2026, 8, 17, 10, 42, 36, 0, time.UTC)
	if got, want := FormatTimestamp(tm), "2026-08-17 10:42:36"; got != want {
		t.Errorf("FormatTimestamp() = %q, want %q", got, want)
	}
}
