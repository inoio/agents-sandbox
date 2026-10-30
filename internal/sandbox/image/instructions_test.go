package image

import "testing"

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

func TestScanFromStagesLowercaseInstructions(t *testing.T) {
	lastImage, stageBase := scanFromStages([]byte(
		"from golang:1.24 as build\nrun go build\nFrom debian:trixie-slim AS final\n",
	))
	if lastImage != "debian:trixie-slim" {
		t.Errorf("lastImage = %q, want debian:trixie-slim", lastImage)
	}
	if got := stageBase["build"]; got != "golang:1.24" {
		t.Errorf("stageBase[build] = %q, want golang:1.24", got)
	}
	if got := stageBase["final"]; got != "debian:trixie-slim" {
		t.Errorf("stageBase[final] = %q, want debian:trixie-slim", got)
	}
}

func TestParseFromLowercaseKeywordAndAlias(t *testing.T) {
	image, alias := parseFrom("from --platform=linux/amd64 debian:trixie-slim as build")
	if image != "debian:trixie-slim" {
		t.Errorf("image = %q, want debian:trixie-slim", image)
	}
	if alias != "build" {
		t.Errorf("alias = %q, want build", alias)
	}
}

func TestParseFromIgnoresNonFromInstruction(t *testing.T) {
	if image, alias := parseFrom("RUN echo hi"); image != "" || alias != "" {
		t.Errorf("parseFrom(RUN ...) = (%q, %q), want empty", image, alias)
	}
}

// TestParseFromTrimsCRLF pins the trailing-whitespace trim of a CRLF Dockerfile
// line. Without it the alias keeps a trailing CR, which no longer matches the
// alias the line-buffered scanner sees, so a custom CRLF base fails the build.
func TestParseFromTrimsCRLF(t *testing.T) {
	image, alias := parseFrom("FROM ubuntu:24.04 AS base\r")
	if image != "ubuntu:24.04" || alias != "base" {
		t.Errorf("parseFrom(CRLF line) = (%q, %q), want (ubuntu:24.04, base)", image, alias)
	}
}

func TestParseFromIgnoresFromage(t *testing.T) {
	if image, _ := parseFrom("FROMAGE debian:trixie-slim"); image != "" {
		t.Errorf("parseFrom(FROMAGE ...) image = %q, want empty", image)
	}
}
