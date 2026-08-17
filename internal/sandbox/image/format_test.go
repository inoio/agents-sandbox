package image

import (
	"testing"
	"time"
)

func ptr(n int64) *int64 { return &n } //nolint:modernize // address-of-value is the intended pattern

func TestFormatSize(t *testing.T) {
	tests := []struct {
		name string
		in   *int64
		want string
	}{
		{"nil", nil, "unknown"},
		{"zero", ptr(0), "0 B"},
		{"bytes", ptr(512), "512 B"},
		{"kib", ptr(1024), "1 KiB"},
		{"kib-frac", ptr(1536), "1.5 KiB"},
		{"mib", ptr(1024 * 1024 * 824), "824 MiB"},
		{"mib-frac", ptr(864256000), "824.2 MiB"},
		{"gib", ptr(2 * 1024 * 1024 * 1024), "2 GiB"},
		{"gib-frac", ptr(2254857830), "2.1 GiB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatSize(tt.in); got != tt.want {
				t.Errorf("FormatSize(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

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
