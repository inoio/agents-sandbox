package image

import (
	"testing"
)

func TestInstruction(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		wantKeyword string
		wantRest    string
	}{
		{"empty line", "", "", ""},
		{"keyword without args", "FROM", "", ""},
		{"from with image", "FROM debian:trixie-slim", "FROM", "debian:trixie-slim"},
		{"lowercase from", "from debian:trixie-slim", "FROM", "debian:trixie-slim"},
		{"mixed-case from", "From debian:trixie-slim", "FROM", "debian:trixie-slim"},
		{"leading whitespace and tab separator", "  \tFrom\talpine", "FROM", "alpine"},
		{"no word boundary", "FROMAGE x", "", ""},
		{"unknown instruction", "POTATO x", "", ""},
		{"other known instruction", "RUN echo hi", "RUN", "echo hi"},
		{"comment", "# FROM debian", "", ""},
		{"indented comment", "  # comment", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyword, rest := instruction(tt.line)
			if keyword != tt.wantKeyword || rest != tt.wantRest {
				t.Errorf("instruction(%q) = (%q, %q), want (%q, %q)",
					tt.line, keyword, rest, tt.wantKeyword, tt.wantRest)
			}
		})
	}
}
