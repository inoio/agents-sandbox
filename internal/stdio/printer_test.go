package stdio

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestSuccessWritesWithoutColor(t *testing.T) {
	var stderr bytes.Buffer
	io := New(nil, &bytes.Buffer{}, &stderr, false, LevelNormal, false)
	io.Success("hello")
	out := stderr.String()
	if !strings.Contains(out, "hello") {
		t.Errorf("expected stderr to contain 'hello', got %q", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("expected no ANSI codes when color disabled, got %q", out)
	}
}

func TestSuccessWritesToStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	io := New(nil, &stdout, &stderr, false, LevelNormal, false)
	io.Success("status")
	if stderr.String() != "status\n" {
		t.Errorf("expected stderr status, got stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if stdout.String() != "" {
		t.Errorf("expected no stdout output, got %q", stdout.String())
	}
}

func TestOutWritesToStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	io := New(nil, &stdout, &stderr, false, LevelNormal, false)
	io.Out("data")
	if stdout.String() != "data\n" {
		t.Errorf("expected stdout data, got %q", stdout.String())
	}
	if stderr.String() != "" {
		t.Errorf("expected no stderr output, got %q", stderr.String())
	}
}

func TestWarnWritesWithYellow(t *testing.T) {
	var stderr bytes.Buffer
	io := New(nil, &bytes.Buffer{}, &stderr, true, LevelNormal, false)
	io.Warn("danger")
	out := stderr.String()
	if !strings.Contains(out, "danger") {
		t.Errorf("expected warn output, got %q", out)
	}
	if !strings.Contains(out, "\x1b[33m") {
		t.Errorf("expected yellow ANSI code, got %q", out)
	}
}

func TestErrorWritesWithRed(t *testing.T) {
	var stderr bytes.Buffer
	io := New(nil, &bytes.Buffer{}, &stderr, true, LevelNormal, false)
	io.Error("boom", errors.New("nope"))
	out := stderr.String()
	if !strings.Contains(out, "\x1b[31m") {
		t.Errorf("expected red ANSI code, got %q", out)
	}
	if !strings.Contains(out, "boom: nope") {
		t.Errorf("expected formatted error, got %q", out)
	}
}

func TestVerboseHiddenAtNormalLevel(t *testing.T) {
	var stderr bytes.Buffer
	io := New(nil, &bytes.Buffer{}, &stderr, false, LevelNormal, false)
	io.Verbose("secret")
	if stderr.String() != "" {
		t.Errorf("expected no verbose output at normal level, got %q", stderr.String())
	}
}

func TestVerboseShownAtVerboseLevel(t *testing.T) {
	var stderr bytes.Buffer
	io := New(nil, &bytes.Buffer{}, &stderr, false, LevelVerbose, false)
	io.Verbose("secret")
	out := stderr.String()
	if !strings.Contains(out, "secret") {
		t.Errorf("expected verbose output, got %q", out)
	}
}

func TestSuccessHiddenAtQuietLevel(t *testing.T) {
	var stderr bytes.Buffer
	io := New(nil, &bytes.Buffer{}, &stderr, false, LevelQuiet, false)
	io.Success("hello")
	if stderr.String() != "" {
		t.Errorf("expected no success output at quiet level, got %q", stderr.String())
	}
}

func TestOutHiddenAtQuietLevel(t *testing.T) {
	var stdout bytes.Buffer
	io := New(nil, &stdout, &bytes.Buffer{}, false, LevelQuiet, false)
	io.Out("hello")
	if stdout.String() != "" {
		t.Errorf("expected no stdout output at quiet level, got %q", stdout.String())
	}
}

func TestWarnShownAtQuietLevel(t *testing.T) {
	var stderr bytes.Buffer
	io := New(nil, &bytes.Buffer{}, &stderr, false, LevelQuiet, false)
	io.Warn("danger")
	out := stderr.String()
	if !strings.Contains(out, "danger") {
		t.Errorf("expected warn at quiet level, got %q", out)
	}
}

func TestErrorShownAtQuietLevel(t *testing.T) {
	var stderr bytes.Buffer
	io := New(nil, &bytes.Buffer{}, &stderr, false, LevelQuiet, false)
	io.Errorf("boom")
	out := stderr.String()
	if !strings.Contains(out, "boom") {
		t.Errorf("expected error at quiet level, got %q", out)
	}
}

func TestErrorFormatsArgs(t *testing.T) {
	var stderr bytes.Buffer
	io := New(nil, &bytes.Buffer{}, &stderr, false, LevelNormal, false)
	io.Errorf("msb not found: %v", errors.New("nope"))
	out := stderr.String()
	if !strings.Contains(out, "msb not found: nope") {
		t.Errorf("expected formatted error, got %q", out)
	}
}

func TestWarnFormatsArgs(t *testing.T) {
	var stderr bytes.Buffer
	io := New(nil, &bytes.Buffer{}, &stderr, false, LevelNormal, false)
	io.Warnf("kept repo %s on branch %s", "/p", "feat")
	out := stderr.String()
	if !strings.Contains(out, "kept repo /p on branch feat") {
		t.Errorf("expected formatted warn, got %q", out)
	}
}

func TestSuccessFormatsArgs(t *testing.T) {
	var stderr bytes.Buffer
	io := New(nil, &bytes.Buffer{}, &stderr, false, LevelNormal, false)
	io.Successf("using default %q", "y")
	out := stderr.String()
	if !strings.Contains(out, `using default "y"`) {
		t.Errorf("expected formatted success, got %q", out)
	}
}

func TestVerboseFormatsArgsAtVerbose(t *testing.T) {
	var stderr bytes.Buffer
	io := New(nil, &bytes.Buffer{}, &stderr, false, LevelVerbose, false)
	io.Verbosef("workspace: %s (branch=%s)", "/p", "feat")
	out := stderr.String()
	if !strings.Contains(out, "workspace: /p (branch=feat)") {
		t.Errorf("expected formatted verbose, got %q", out)
	}
}

func TestOutfFormatsArgs(t *testing.T) {
	var stdout bytes.Buffer
	io := New(nil, &stdout, &bytes.Buffer{}, false, LevelNormal, false)
	io.Outf("%-40s %s", "name", "status")
	out := stdout.String()
	if !strings.Contains(out, "name"+strings.Repeat(" ", 37)+"status") {
		t.Errorf("expected formatted stdout, got %q", out)
	}
}
