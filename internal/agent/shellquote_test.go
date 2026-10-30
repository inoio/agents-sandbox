package agent

import (
	"slices"
	"testing"
)

func TestShellQuote(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"plain", "'plain'"},
		{"with space", "'with space'"},
		{"it's", `'it'\''s'`},
		{"", "''"},
	}
	for _, tc := range cases {
		if got := shellQuote(tc.in); got != tc.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestShellQuoteArgs(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, []string{}},
		{"plain", []string{"--print-logs"}, []string{"'--print-logs'"}},
		{
			"spaces stay one word",
			[]string{"chat", "-q", "explain this function"},
			[]string{"'chat'", "'-q'", "'explain this function'"},
		},
		{"embedded single quote", []string{"it's"}, []string{`'it'\''s'`}},
	}
	for _, tc := range cases {
		if got := shellQuoteArgs(tc.in); !slices.Equal(got, tc.want) {
			t.Errorf("%s: shellQuoteArgs(%v) = %v, want %v", tc.name, tc.in, got, tc.want)
		}
	}
}
