package termio

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestSuccessWritesWithoutColor(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelInfo, false, false)
	ui.Info("hello")
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
	ui := New(nil, &stdout, &stderr, false, LevelInfo, false, false)
	ui.Info("status")
	if stderr.String() != "status\n" {
		t.Errorf("expected stderr status, got stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if stdout.String() != "" {
		t.Errorf("expected no stdout output, got %q", stdout.String())
	}
}

func TestOutWritesToStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	ui := New(nil, &stdout, &stderr, false, LevelInfo, false, false)
	ui.Out("data")
	if stdout.String() != "data\n" {
		t.Errorf("expected stdout data, got %q", stdout.String())
	}
	if stderr.String() != "" {
		t.Errorf("expected no stderr output, got %q", stderr.String())
	}
}

func TestWarnWritesWithYellow(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, true, LevelInfo, false, false)
	ui.Warn("danger")
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
	ui := New(nil, &bytes.Buffer{}, &stderr, true, LevelInfo, false, false)
	ui.Error("boom", errors.New("nope"))
	out := stderr.String()
	if !strings.Contains(out, "\x1b[31m") {
		t.Errorf("expected red ANSI code, got %q", out)
	}
	if !strings.Contains(out, "boom: nope") {
		t.Errorf("expected formatted error, got %q", out)
	}
}

func TestVerboseHiddenAtInfoLevel(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelInfo, false, false)
	ui.Verbose("secret")
	if stderr.String() != "" {
		t.Errorf("expected no verbose output at normal level, got %q", stderr.String())
	}
}

func TestVerboseShownAtDebugLevel(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelVerbose, false, false)
	ui.Verbose("secret")
	out := stderr.String()
	if !strings.Contains(out, "secret") {
		t.Errorf("expected verbose output, got %q", out)
	}
}

func TestSuccessHiddenAtErrorLevel(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelError, false, false)
	ui.Info("hello")
	if stderr.String() != "" {
		t.Errorf("expected no success output at error level, got %q", stderr.String())
	}
}

func TestOutAlwaysShownAtErrorLevel(t *testing.T) {
	var stdout bytes.Buffer
	ui := New(nil, &stdout, &bytes.Buffer{}, false, LevelError, false, false)
	ui.Out("hello")
	if stdout.String() != "hello\n" {
		t.Errorf("expected stdout output even at error level, got %q", stdout.String())
	}
}

func TestWarnShownAtWarningLevel(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelWarning, false, false)
	ui.Warn("danger")
	out := stderr.String()
	if !strings.Contains(out, "danger") {
		t.Errorf("expected warn at warning level, got %q", out)
	}
}

func TestWarnHiddenAtErrorLevel(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelError, false, false)
	ui.Warn("danger")
	if stderr.String() != "" {
		t.Errorf("expected no warn output at error level, got %q", stderr.String())
	}
}

func TestWarnfHiddenAtErrorLevel(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelError, false, false)
	ui.Warnf("danger %s", "x")
	if stderr.String() != "" {
		t.Errorf("expected no warnf output at error level, got %q", stderr.String())
	}
}

func TestErrorShownAtErrorLevel(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelError, false, false)
	ui.Errorf("boom")
	out := stderr.String()
	if !strings.Contains(out, "boom") {
		t.Errorf("expected error at error level, got %q", out)
	}
}

func TestErrorRendersSingleColonBeforeErr(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelInfo, false, false)
	ui.Error("Failed", errors.New("the error"))
	out := stderr.String()
	if !strings.Contains(out, "Failed: the error") {
		t.Errorf("expected single colon 'Failed: the error', got %q", out)
	}
	if strings.Contains(out, ": :") {
		t.Errorf("expected no double colon, got %q", out)
	}
}

func TestErrorFormatsArgs(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelInfo, false, false)
	ui.Errorf("msb not found: %v", errors.New("nope"))
	out := stderr.String()
	if !strings.Contains(out, "msb not found: nope") {
		t.Errorf("expected formatted error, got %q", out)
	}
}

func TestWarnFormatsArgs(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelInfo, false, false)
	ui.Warnf("kept repo %s on branch %s", "/p", "feat")
	out := stderr.String()
	if !strings.Contains(out, "kept repo /p on branch feat") {
		t.Errorf("expected formatted warn, got %q", out)
	}
}

func TestSuccessFormatsArgs(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelInfo, false, false)
	ui.Infof("using default %q", "y")
	out := stderr.String()
	if !strings.Contains(out, `using default "y"`) {
		t.Errorf("expected formatted success, got %q", out)
	}
}

func TestVerboseFormatsArgsAtDebug(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelVerbose, false, false)
	ui.Verbosef("workspace: %s (branch=%s)", "/p", "feat")
	out := stderr.String()
	if !strings.Contains(out, "workspace: /p (branch=feat)") {
		t.Errorf("expected formatted verbose, got %q", out)
	}
}

func TestSetLevelRaisesToDebug(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelInfo, false, false)
	ui.Verbose("secret")
	if stderr.String() != "" {
		t.Fatalf("verbose output at normal level must be suppressed, got %q", stderr.String())
	}
	ui.SetLevel(LevelVerbose)
	ui.Verbose("secret")
	if !strings.Contains(stderr.String(), "secret") {
		t.Errorf("expected verbose output after SetLevel, got %q", stderr.String())
	}
}

func TestSetLevelLowersSeverity(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelInfo, false, false)
	ui.SetLevel(LevelError)
	ui.Info("hidden")
	if stderr.String() != "" {
		t.Errorf("expected no info output at error level, got %q", stderr.String())
	}
}

func TestOutfFormatsArgs(t *testing.T) {
	var stdout bytes.Buffer
	ui := New(nil, &stdout, &bytes.Buffer{}, false, LevelInfo, false, false)
	ui.Outf("%-40s %s", "name", "status")
	out := stdout.String()
	if !strings.Contains(out, "name"+strings.Repeat(" ", 37)+"status") {
		t.Errorf("expected formatted stdout, got %q", out)
	}
}

func TestHeaderWritesToStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	ui := New(nil, &stdout, &stderr, false, LevelInfo, false, false)
	ui.Header("NAME STATUS")
	if stdout.String() != "NAME STATUS\n" {
		t.Errorf("expected plain header on stdout, got %q", stdout.String())
	}
	if stderr.String() != "" {
		t.Errorf("expected no stderr output, got %q", stderr.String())
	}
}

func TestHeaderStyledBoldCyanWhenColor(t *testing.T) {
	var stdout bytes.Buffer
	ui := New(nil, &stdout, &bytes.Buffer{}, true, LevelInfo, false, false)
	ui.Header("NAME STATUS")
	out := stdout.String()
	if !strings.Contains(out, "\x1b[1;36m") {
		t.Errorf("expected bold cyan ANSI code, got %q", out)
	}
	if !strings.Contains(out, "\x1b[0m") {
		t.Errorf("expected ANSI reset, got %q", out)
	}
	if !strings.Contains(out, "NAME STATUS") {
		t.Errorf("expected header text, got %q", out)
	}
}

func TestHeaderAlwaysShownAtErrorLevel(t *testing.T) {
	var stdout bytes.Buffer
	ui := New(nil, &stdout, &bytes.Buffer{}, false, LevelError, false, false)
	ui.Header("NAME")
	if stdout.String() != "NAME\n" {
		t.Errorf("expected header even at error level, got %q", stdout.String())
	}
}

func TestQuietSuppressesOut(t *testing.T) {
	var stdout, stderr bytes.Buffer
	ui := New(nil, &stdout, &stderr, false, LevelInfo, false, false)
	ui.SetQuiet(true)
	ui.Out("data")
	if stdout.String() != "" {
		t.Errorf("quiet Out should write nothing to stdout, got %q", stdout.String())
	}
	if stderr.String() != "" {
		t.Errorf("quiet Out should not touch stderr, got %q", stderr.String())
	}
}

func TestQuietSuppressesOutf(t *testing.T) {
	var stdout bytes.Buffer
	ui := New(nil, &stdout, &bytes.Buffer{}, false, LevelInfo, true, false)
	ui.Outf("name=%s", "alpha")
	if stdout.String() != "" {
		t.Errorf("quiet Outf should write nothing to stdout, got %q", stdout.String())
	}
}

func TestQuietSuppressesHeader(t *testing.T) {
	var stdout bytes.Buffer
	ui := New(nil, &stdout, &bytes.Buffer{}, false, LevelInfo, true, false)
	ui.Header("NAME STATUS")
	if stdout.String() != "" {
		t.Errorf("quiet Header should write nothing to stdout, got %q", stdout.String())
	}
}

func TestQuietDoesNotSuppressStderrLogs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	ui := New(nil, &stdout, &stderr, false, LevelInfo, true, false)
	ui.Info("progress")
	ui.Warn("danger")
	if stderr.String() == "" {
		t.Error("quiet must not suppress Info/Warn on stderr")
	}
	if stdout.String() != "" {
		t.Errorf("quiet should still suppress stdout, got %q", stdout.String())
	}
}

func TestQuietCanBeDisabledAgain(t *testing.T) {
	var stdout bytes.Buffer
	ui := New(nil, &stdout, &bytes.Buffer{}, false, LevelInfo, true, false)
	ui.Out("hidden")
	if stdout.String() != "" {
		t.Fatalf("expected quiet suppression, got %q", stdout.String())
	}
	ui.SetQuiet(false)
	ui.Out("visible")
	if stdout.String() != "visible\n" {
		t.Errorf("expected stdout after SetQuiet(false), got %q", stdout.String())
	}
}

func TestOutStripsANSIIfColorDisabled(t *testing.T) {
	var stdout bytes.Buffer
	ui := New(nil, &stdout, &bytes.Buffer{}, false, LevelInfo, false, false)
	ui.Out(StyleStatus("running"))
	out := stdout.String()
	if out != "running\n" {
		t.Errorf("color-disabled Out should strip ANSI, got %q", out)
	}
}

func TestOutKeepsANSIIfColorEnabled(t *testing.T) {
	var stdout bytes.Buffer
	ui := New(nil, &stdout, &bytes.Buffer{}, true, LevelInfo, false, false)
	ui.Out(StyleStatus("running"))
	out := stdout.String()
	if !strings.Contains(out, "\x1b[1;32mrunning\x1b[0m") {
		t.Errorf("color-enabled Out should keep ANSI, got %q", out)
	}
}
