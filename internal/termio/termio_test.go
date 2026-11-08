package termio

import (
	"strings"
	"testing"
)

// prompterOnly implements Prompter alone: ConfirmAction must accept it.
type prompterOnly struct{ confirmed bool }

func (p *prompterOnly) IsInteractive() bool { return true }
func (p *prompterOnly) Select(string, []Choice, string) (string, error) {
	return "", nil
}
func (p *prompterOnly) Input(string, string) (string, error) { return "", nil }
func (p *prompterOnly) Confirm(string, bool) (bool, error) {
	p.confirmed = true
	return true, nil
}
func (p *prompterOnly) Acknowledge(string) error { return nil }

func TestConfirmActionAcceptsPrompterOnly(t *testing.T) {
	p := &prompterOnly{}
	if err := ConfirmAction(p, "remove?"); err != nil {
		t.Fatalf("ConfirmAction: %v", err)
	}
	if !p.confirmed {
		t.Fatal("Confirm was not called")
	}
}

func TestLevelString(t *testing.T) {
	tests := []struct {
		level Level
		want  string
	}{
		{LevelError, "error"},
		{LevelWarning, "warning"},
		{LevelInfo, "info"},
		{LevelVerbose, "verbose"},
		{Level(99), "info"},
	}
	for _, tc := range tests {
		if got := tc.level.String(); got != tc.want {
			t.Errorf("(%d).String() = %q, want %q", tc.level, got, tc.want)
		}
	}
}

func TestLevelsAreMonotonic(t *testing.T) {
	if LevelError >= LevelWarning || LevelWarning >= LevelInfo || LevelInfo >= LevelVerbose {
		t.Error("levels must be strictly increasing: error < warning < info < verbose")
	}
}

// TestParseLevelIsCaseInsensitiveAndNamesTheValidLevels pins that any casing of
// a level name parses and that an unknown name is rejected with the valid ones.
func TestParseLevelIsCaseInsensitiveAndNamesTheValidLevels(t *testing.T) {
	tests := []struct {
		in      string
		want    Level
		wantErr bool
	}{
		{in: "error", want: LevelError},
		{in: "warning", want: LevelWarning},
		{in: "info", want: LevelInfo},
		{in: "verbose", want: LevelVerbose},
		{in: "ERROR", want: LevelError},
		{in: "Info", want: LevelInfo},
		{in: "bogus", wantErr: true},
	}
	for _, tc := range tests {
		got, err := ParseLevel(tc.in)
		if tc.wantErr {
			if err == nil || !strings.Contains(err.Error(), "info") {
				t.Errorf("ParseLevel(%q) error = %v, want one naming the valid levels", tc.in, err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("ParseLevel(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}
}
