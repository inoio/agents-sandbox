package termio

import (
	"strings"
	"testing"
)

func TestLevelString(t *testing.T) {
	tests := []struct {
		level Level
		want  string
	}{
		{LevelError, "error"},
		{LevelWarning, "warning"},
		{LevelInfo, "info"},
		{LevelVerbose, "verbose"},
	}
	for _, tc := range tests {
		if got := tc.level.String(); got != tc.want {
			t.Errorf("(%d).String() = %q, want %q", tc.level, got, tc.want)
		}
	}
	if got := Level(99).String(); got != "info" {
		t.Errorf("unknown level String() = %q, want %q", got, "info")
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		in   string
		want Level
	}{
		{"error", LevelError},
		{"warning", LevelWarning},
		{"info", LevelInfo},
		{"verbose", LevelVerbose},
		{"ERROR", LevelError},
		{"Info", LevelInfo},
	}
	for _, tc := range tests {
		got, err := ParseLevel(tc.in)
		if err != nil {
			t.Errorf("ParseLevel(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseLevelInvalid(t *testing.T) {
	if _, err := ParseLevel("bogus"); err == nil {
		t.Error("ParseLevel(bogus) should error")
	} else if !strings.Contains(err.Error(), "info") {
		t.Errorf("expected error to mention valid levels, got %q", err.Error())
	}
}

func TestLevelsAreMonotonic(t *testing.T) {
	if LevelError >= LevelWarning || LevelWarning >= LevelInfo || LevelInfo >= LevelVerbose {
		t.Error("levels must be strictly increasing: error < warning < info < verbose")
	}
}
