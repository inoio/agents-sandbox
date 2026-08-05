package termio

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestFormatElapsedLive(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "(0s)"},
		{1500 * time.Millisecond, "(1s)"},
		{59_900 * time.Millisecond, "(59s)"},
	}
	for _, c := range cases {
		got := formatElapsedLive(c.in)
		if got != c.want {
			t.Errorf("formatElapsedLive(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatElapsedDone(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "(0.0s)"},
		{3_640 * time.Millisecond, "(3.6s)"},
		// 1260 ms is used instead of 1250 ms because Go's %.1f rounds half to even.
		{1_260 * time.Millisecond, "(1.3s)"},
	}
	for _, c := range cases {
		got := formatElapsedDone(c.in)
		if got != c.want {
			t.Errorf("formatElapsedDone(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSpinnerNonTerminalStop(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelNormal, false)
	spin := ui.Spinner("Building image")
	spin.Stop()

	output := stderr.String()
	if !strings.Contains(output, "Building image... ") {
		t.Errorf("expected start message, got %q", output)
	}
	if !strings.Contains(output, "✅(") {
		t.Errorf("expected timed done suffix, got %q", output)
	}
}

func TestSpinnerNonTerminalError(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelNormal, false)
	spin := ui.Spinner("Building image")
	spin.StopError(errors.New("build failed"))

	output := stderr.String()
	if !strings.Contains(output, "Building image... ") {
		t.Errorf("expected start message, got %q", output)
	}
	if !strings.Contains(output, "failed (") || !strings.Contains(output, ": build failed") {
		t.Errorf("expected timed failed suffix, got %q", output)
	}
}

func TestSpinnerStopTwiceNoPanic(_ *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelNormal, false)
	spin := ui.Spinner("Building image")
	spin.Stop()
	spin.Stop()
	spin.StopError(errors.New("err"))
}

func TestSpinnerHiddenAtQuietLevel(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelQuiet, false)
	spin := ui.Spinner("Building image")
	spin.Stop()
	if stderr.String() != "" {
		t.Errorf("expected no spinner output at quiet level, got %q", stderr.String())
	}
}

func TestSpinnerVerboseSameAsNormal(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelVerbose, false)
	spin := ui.Spinner("Building image")
	spin.Stop()

	output := stderr.String()
	if !strings.Contains(output, "Building image... ") {
		t.Errorf("expected live prefix at verbose level, got %q", output)
	}
	if !strings.Contains(output, "✅(") {
		t.Errorf("expected done suffix at verbose level, got %q", output)
	}
}
