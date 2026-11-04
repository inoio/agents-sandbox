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

func TestSpinnerFinishRendersResult(t *testing.T) {
	cases := []struct {
		name   string
		color  bool
		finish func(Spinner)
		want   []string
	}{
		{"done", false, func(s Spinner) { s.Stop() }, []string{"Building image... ", "✓("}},
		{
			"done in color",
			true,
			func(s Spinner) { s.Stop() },
			[]string{"Building image", ansiGreen + "✓" + ansiReset + "("},
		},
		{
			"failed", false, func(s Spinner) { s.StopError(errors.New("build failed")) },
			[]string{"Building image... ", "failed (", ": build failed"},
		},
		{
			"failed in color", true, func(s Spinner) { s.StopError(errors.New("boom")) },
			[]string{ansiRed + "failed (", ": boom" + ansiReset},
		},
		{"custom result", false, func(s Spinner) { s.(*spinner).finish("skipped") }, []string{"skipped ("}},
		{
			"custom result in color",
			true,
			func(s Spinner) { s.(*spinner).finish("skipped") },
			[]string{"Building image skipped ("},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stderr bytes.Buffer
			ui := New(nil, &bytes.Buffer{}, &stderr, c.color, LevelInfo, false, false)
			c.finish(ui.Spinner("Building image"))
			output := stderr.String()
			for _, want := range c.want {
				if !strings.Contains(output, want) {
					t.Errorf("expected %q in output, got %q", want, output)
				}
			}
		})
	}
}

func TestSpinnerColorDrawsAnimationFrameUntilFinished(t *testing.T) {
	orig := spinnerInterval
	spinnerInterval = time.Millisecond
	t.Cleanup(func() { spinnerInterval = orig })

	var stderr bytes.Buffer
	s := newSpinner(&stderr, true, LevelInfo, "step")
	time.Sleep(5 * spinnerInterval)
	s.finish("done")
	if want := "\r\033[Kstep " + spinnerChars[0] + "(0s)"; !strings.Contains(stderr.String(), want) {
		t.Errorf("expected first frame %q in output, got %q", want, stderr.String())
	}
}

// TestSpinnerFinishStopsPromptly verifies that finishing a spinner does not
// block for the animation interval: finish signals the goroutine, which must
// wake immediately instead of sleeping out the current frame.
func TestSpinnerFinishStopsPromptly(t *testing.T) {
	orig := spinnerInterval
	spinnerInterval = time.Hour
	t.Cleanup(func() { spinnerInterval = orig })

	var stderr bytes.Buffer
	s := newSpinner(&stderr, true, LevelInfo, "step")

	done := make(chan struct{})
	go func() {
		s.finish("done")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("finish blocked on the animation interval instead of stopping promptly")
	}
}

func TestSpinnerStopTwiceNoPanic(_ *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelInfo, false, false)
	spin := ui.Spinner("Building image")
	spin.Stop()
	spin.Stop()
	spin.StopError(errors.New("err"))
}

func TestSpinnerHiddenAtErrorLevel(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelError, false, false)
	spin := ui.Spinner("Building image")
	spin.Stop()
	if stderr.String() != "" {
		t.Errorf("expected no spinner output at error level, got %q", stderr.String())
	}
}

func TestSpinnerFinishSuppressedAtErrorLevel(t *testing.T) {
	var stderr bytes.Buffer
	s := &spinner{w: &stderr, level: LevelError, active: true}
	s.finish("done")
	if stderr.String() != "" {
		t.Errorf("expected finish suppressed at error level, got %q", stderr.String())
	}
}

func TestSpinnerShownAtDebugLevel(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelVerbose, false, false)
	spin := ui.Spinner("Building image")
	spin.Stop()

	output := stderr.String()
	if !strings.Contains(output, "Building image... ") {
		t.Errorf("expected live prefix at verbose level, got %q", output)
	}
	if !strings.Contains(output, "✓(") {
		t.Errorf("expected done suffix at verbose level, got %q", output)
	}
}
