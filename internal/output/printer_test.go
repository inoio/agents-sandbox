package output

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestInfoWritesWithoutColor(t *testing.T) {
	var buf bytes.Buffer
	l := NewPrinter(&buf, false)
	l.Infof("hello")
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
	l := NewPrinter(&buf, true)
	l.Warnf("danger")
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
	l := NewPrinter(&buf, true)
	l.Errorf("boom")
	out := buf.String()
	if !strings.Contains(out, "\x1b[31m") {
		t.Errorf("expected red ANSI code, got %q", out)
	}
}

func TestDebugHiddenAtNormalLevel(t *testing.T) {
	var buf bytes.Buffer
	l := NewPrinter(&buf, false)
	l.Debugf("secret")
	if buf.String() != "" {
		t.Errorf("expected no output at normal level, got %q", buf.String())
	}
}

func TestDebugShownAtVerboseLevel(t *testing.T) {
	var buf bytes.Buffer
	l := NewPrinterWithLevel(&buf, false, LevelVerbose)
	l.Debugf("secret")
	out := buf.String()
	if !strings.Contains(out, "secret") {
		t.Errorf("expected output to contain 'secret' at verbose level, got %q", out)
	}
}

func TestInfoHiddenAtQuietLevel(t *testing.T) {
	var buf bytes.Buffer
	l := NewPrinterWithLevel(&buf, false, LevelQuiet)
	l.Infof("hello")
	if buf.String() != "" {
		t.Errorf("expected no info output at quiet level, got %q", buf.String())
	}
}

func TestWarnShownAtQuietLevel(t *testing.T) {
	var buf bytes.Buffer
	l := NewPrinterWithLevel(&buf, false, LevelQuiet)
	l.Warnf("danger")
	out := buf.String()
	if !strings.Contains(out, "danger") {
		t.Errorf("expected warn at quiet level, got %q", out)
	}
}

func TestErrorShownAtQuietLevel(t *testing.T) {
	var buf bytes.Buffer
	l := NewPrinterWithLevel(&buf, false, LevelQuiet)
	l.Errorf("boom")
	out := buf.String()
	if !strings.Contains(out, "boom") {
		t.Errorf("expected error at quiet level, got %q", out)
	}
}

func TestErrorFormatsArgs(t *testing.T) {
	var buf bytes.Buffer
	l := NewPrinter(&buf, false)
	l.Errorf("msb not found: %v", errors.New("nope"))
	out := buf.String()
	if !strings.Contains(out, "msb not found: nope") {
		t.Errorf("expected formatted error, got %q", out)
	}
}

func TestWarnFormatsArgs(t *testing.T) {
	var buf bytes.Buffer
	l := NewPrinter(&buf, false)
	l.Warnf("kept repo %s on branch %s", "/p", "feat")
	out := buf.String()
	if !strings.Contains(out, "kept repo /p on branch feat") {
		t.Errorf("expected formatted warn, got %q", out)
	}
}

func TestInfoFormatsArgs(t *testing.T) {
	var buf bytes.Buffer
	l := NewPrinter(&buf, false)
	l.Infof("using default %q", "y")
	out := buf.String()
	if !strings.Contains(out, `using default "y"`) {
		t.Errorf("expected formatted info, got %q", out)
	}
}

func TestDebugFormatsArgsAtVerbose(t *testing.T) {
	var buf bytes.Buffer
	l := NewPrinterWithLevel(&buf, false, LevelVerbose)
	l.Debugf("workspace: %s (branch=%s)", "/p", "feat")
	out := buf.String()
	if !strings.Contains(out, "workspace: /p (branch=feat)") {
		t.Errorf("expected formatted debug, got %q", out)
	}
}

func TestNoArgsPassthrough(t *testing.T) {
	var buf bytes.Buffer
	l := NewPrinter(&buf, false)
	l.Infof("hello world")
	out := buf.String()
	if !strings.Contains(out, "hello world") {
		t.Errorf("expected message passthrough when no args, got %q", out)
	}
}
