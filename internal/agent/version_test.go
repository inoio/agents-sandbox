package agent

import (
	"testing"
)

func TestExtractSemverFromOutput(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want string
	}{
		{"bare version", "0.5.0\n", "0.5.0"},
		{"v prefix", "v0.5.0\n", "0.5.0"},
		{"pi style", "pi 0.3.0\n", "0.3.0"},
		{"claude style", "@anthropic-ai/claude-code/2.3.4 linux-x64 node-v22.14.0\n", "2.3.4"},
		{"no version", "help text\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extractSemverFromOutput(tc.out)
			if tc.want == "" {
				if err == nil {
					t.Errorf("expected an error for %q, got %q", tc.out, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("extractSemverFromOutput(%q): %v", tc.out, err)
			}
			if got != tc.want {
				t.Errorf("extractSemverFromOutput(%q) = %q, want %q", tc.out, got, tc.want)
			}
		})
	}
}
