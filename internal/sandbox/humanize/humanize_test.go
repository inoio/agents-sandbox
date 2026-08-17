package humanize

import "testing"

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0"},
		{1023, "1023"},
		{1024, "1.0K"},
		{1536, "1.5K"},
		{1024 * 1024, "1.0M"},
		{1234567, "1.2M"},
		{1024 * 1024 * 1024, "1.0G"},
		{3 * 1024 * 1024 * 1024, "3.0G"},
	}
	for _, tc := range cases {
		if got := FormatBytes(tc.in); got != tc.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
