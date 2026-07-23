package log

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
	var buf bytes.Buffer
	s := NewSpinner(New(&buf, false))
	s.Start("Building image")
	s.Stop()

	output := buf.String()
	if !strings.Contains(output, "Building image... ") {
		t.Errorf("expected start message, got %q", output)
	}
	if !strings.Contains(output, "done(") {
		t.Errorf("expected timed done suffix, got %q", output)
	}
}

func TestSpinnerNonTerminalError(t *testing.T) {
	var buf bytes.Buffer
	s := NewSpinner(New(&buf, false))
	s.Start("Building image")
	s.StopError(errors.New("build failed"))

	output := buf.String()
	if !strings.Contains(output, "Building image... ") {
		t.Errorf("expected start message, got %q", output)
	}
	if !strings.Contains(output, "failed (") || !strings.Contains(output, ": build failed") {
		t.Errorf("expected timed failed suffix, got %q", output)
	}
}

func TestSpinnerStopTwiceNoPanic(_ *testing.T) {
	var buf bytes.Buffer
	s := NewSpinner(New(&buf, false))
	s.Start("Building image")
	s.Stop()
	s.Stop()
	s.StopError(errors.New("err"))
}

func TestSpinnerHiddenAtQuietLevel(t *testing.T) {
	var buf bytes.Buffer
	l := NewWithLevel(&buf, false, LevelQuiet)
	s := NewSpinner(l)
	s.Start("Building image")
	s.Stop()
	if buf.String() != "" {
		t.Errorf("expected no spinner output at quiet level, got %q", buf.String())
	}
}
