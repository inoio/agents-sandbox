package log

import (
	"bytes"
	"strings"
	"testing"
)

func TestInfoWritesWithoutColor(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, false)
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
	l := New(&buf, true)
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
	l := New(&buf, true)
	l.Error("boom")
	out := buf.String()
	if !strings.Contains(out, "\x1b[31m") {
		t.Errorf("expected red ANSI code, got %q", out)
	}
}

func TestDebugHiddenAtNormalLevel(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, false)
	l.Debug("secret")
	if buf.String() != "" {
		t.Errorf("expected no output at normal level, got %q", buf.String())
	}
}

func TestDebugShownAtVerboseLevel(t *testing.T) {
	var buf bytes.Buffer
	l := NewWithLevel(&buf, false, LevelVerbose)
	l.Debug("secret")
	out := buf.String()
	if !strings.Contains(out, "secret") {
		t.Errorf("expected output to contain 'secret' at verbose level, got %q", out)
	}
}

func TestInfoHiddenAtQuietLevel(t *testing.T) {
	var buf bytes.Buffer
	l := NewWithLevel(&buf, false, LevelQuiet)
	l.Info("hello")
	if buf.String() != "" {
		t.Errorf("expected no info output at quiet level, got %q", buf.String())
	}
}

func TestWarnShownAtQuietLevel(t *testing.T) {
	var buf bytes.Buffer
	l := NewWithLevel(&buf, false, LevelQuiet)
	l.Warn("danger")
	out := buf.String()
	if !strings.Contains(out, "danger") {
		t.Errorf("expected warn at quiet level, got %q", out)
	}
}

func TestErrorShownAtQuietLevel(t *testing.T) {
	var buf bytes.Buffer
	l := NewWithLevel(&buf, false, LevelQuiet)
	l.Error("boom")
	out := buf.String()
	if !strings.Contains(out, "boom") {
		t.Errorf("expected error at quiet level, got %q", out)
	}
}
