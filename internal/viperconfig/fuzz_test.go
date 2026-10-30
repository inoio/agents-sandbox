package viperconfig

import "testing"

// FuzzParseHumanDuration drives the `--older-than` duration parser. Its
// documented forms are "7d"/"2w"/"6h"/"30m" (Go's own ParseDuration plus days
// and weeks), and every caller treats the result as an age or an interval, so
// a successful parse must never yield a negative duration — a negative prune
// age matches every artifact. Day/week counts are bounded, so an oversized
// count must be rejected rather than wrap int64.
func FuzzParseHumanDuration(f *testing.F) {
	for _, s := range []string{
		"7d", "2w", "6h", "30m", "1s", "0d", "0", "  3d  ", "1w2d", "d", "5", "1e3d",
		"-1d", "-2w", "-1h", "106751d", "106752d", "110000d", "15251w",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		d, ok := ParseHumanDuration(s)
		if !ok {
			return
		}
		if d < 0 {
			t.Fatalf("ParseHumanDuration(%q) = %v, want a non-negative duration", s, d)
		}
	})
}
