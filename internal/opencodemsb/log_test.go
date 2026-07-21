package opencodemsb

import (
	"bytes"
	"strings"
	"testing"
)

func TestInfoWritesWithoutColor(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf, false)
	l.Info("hello")
	out := buf.String()
	if !strings.Contains(out, "hello") {
		t.Errorf("expected output to contain 'hello', got %q", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("expected no ANSI codes when color disabled, got %q", out)
	}
}

func TestWarnWritesWithYellow(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf, true)
	l.Warn("danger")
	out := buf.String()
	if !strings.Contains(out, "danger") {
		t.Errorf("expected output to contain 'danger', got %q", out)
	}
	if !strings.Contains(out, "\x1b[33m") {
		t.Errorf("expected yellow ANSI code, got %q", out)
	}
}

func TestErrorWritesWithRed(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf, true)
	l.Error("boom")
	out := buf.String()
	if !strings.Contains(out, "\x1b[31m") {
		t.Errorf("expected red ANSI code, got %q", out)
	}
}

func TestTimingFormatsDuration(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf, false)
	l.Timing("preflight", 1250000000)
	out := buf.String()
	if !strings.Contains(out, "[timing] preflight: 1.250s") {
		t.Errorf("expected timing line, got %q", out)
	}
}
