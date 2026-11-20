package globmatch

import (
	"slices"
	"testing"
)

// TestNameMatches pins the basename-only matching across the supported glob
// features: `*`, a brace alternative, and a pattern that does not match.
func TestNameMatches(t *testing.T) {
	cases := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"opencode*.json*", "opencode.jsonc", true},
		{"opencode*.json*", "config.json", false},
		{"pi-*.{json,yaml}", "pi-model.yaml", true},
		{"pi-*.{json,yaml}", "pi-model.toml", false},
		{"settings*.json*", "settings.json", true},
		{"config*.yaml", "config.yaml", true},
		{"config*.yaml", "config-broken.yaml", true},
	}
	for _, c := range cases {
		if got := NameMatches(c.pattern, c.name); got != c.want {
			t.Errorf("NameMatches(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestExpandBracesUnbalanced(t *testing.T) {
	// Unmatched open or close brace: returned unchanged.
	if got := expandBraces("a-{b,c"); len(got) != 1 || got[0] != "a-{b,c" {
		t.Errorf("unmatched open brace = %v, want [a-{b,c]", got)
	}
	if got := expandBraces("a-b}"); len(got) != 1 || got[0] != "a-b}" {
		t.Errorf("unmatched close brace = %v, want [a-b}]", got)
	}
}

func TestExpandBracesStartingWithBrace(t *testing.T) {
	// open == 0 is a real brace group, not "no brace".
	cases := []struct {
		pattern string
		want    []string
	}{
		{"{a,b}", []string{"a", "b"}},
		{"{a,b}x", []string{"ax", "bx"}},
	}
	for _, c := range cases {
		if got := expandBraces(c.pattern); !slices.Equal(got, c.want) {
			t.Errorf("expandBraces(%q) = %v, want %v", c.pattern, got, c.want)
		}
	}
}
