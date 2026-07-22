package log

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestNewTimingDisabled(t *testing.T) {
	var buf bytes.Buffer
	tick, summary := NewTiming(New(&buf, false), false)
	tick("phase1")
	tick("phase2")
	summary()
	if buf.Len() > 0 {
		t.Errorf("expected no output when timing disabled, got %q", buf.String())
	}
}

func TestNewTimingEnabled(t *testing.T) {
	var buf bytes.Buffer
	tick, summary := NewTiming(New(&buf, false), true)
	tick("phase1")
	time.Sleep(1 * time.Millisecond)
	tick("phase2")
	summary()
	out := buf.String()
	if !strings.Contains(out, "[timing] phase1") {
		t.Errorf("expected timing output for phase1, got %q", out)
	}
	if !strings.Contains(out, "[timing] phase2") {
		t.Errorf("expected timing output for phase2, got %q", out)
	}
	if !strings.Contains(out, "[timing] total launcher overhead") {
		t.Errorf("expected total timing output, got %q", out)
	}
}
