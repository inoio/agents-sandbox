package humanize

import "testing"

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
