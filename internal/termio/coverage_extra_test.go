package termio

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

// ---- printer: NewTable, Spinnerf, StdOut/StdErr, quiet-return paths ----

func TestPrinterNewTableReturnsEmptyTable(t *testing.T) {
	var stdout bytes.Buffer
	ui := New(nil, &stdout, &bytes.Buffer{}, false, LevelInfo, false, false).(*printer)
	tbl := ui.NewTable("A", "B")
	if tbl == nil {
		t.Fatal("NewTable returned nil")
	}
	if got := tbl.render(); got != "" {
		t.Errorf("render() of fresh table = %q, want empty", got)
	}
	tbl.AddRow("x", "y")
	if !strings.Contains(tbl.render(), "x") {
		t.Errorf("expected added row to render, got %q", tbl.render())
	}
}

func TestPrinterNewTableWithNoHeaders(t *testing.T) {
	ui := New(nil, &bytes.Buffer{}, &bytes.Buffer{}, false, LevelInfo, false, false).(*printer)
	tbl := ui.NewTable()
	if tbl == nil {
		t.Fatal("NewTable returned nil")
	}
}

func TestPrinterSpinnerf(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelInfo, false, false)
	spin := ui.Spinnerf("Building %s", "image")
	spin.Stop()
	out := stderr.String()
	if !strings.Contains(out, "Building image... ") {
		t.Errorf("expected formatted spinner msg, got %q", out)
	}
}

func TestPrinterStdOutStdErr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	ui := New(nil, &stdout, &stderr, false, LevelInfo, false, false).(*printer)
	if got := ui.StdOut(); got != &stdout {
		t.Errorf("StdOut() = %v, want %v", got, &stdout)
	}
	if got := ui.StdErr(); got != &stderr {
		t.Errorf("StdErr() = %v, want %v", got, &stderr)
	}
}

func TestInfofHiddenAtQuietLevel(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelError, false, false)
	ui.Infof("using default %q", "y")
	if stderr.String() != "" {
		t.Errorf("expected no infof output at quiet level, got %q", stderr.String())
	}
}

func TestVerbosefHiddenAtNormalLevel(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, false, LevelInfo, false, false)
	ui.Verbosef("workspace: %s", "/p")
	if stderr.String() != "" {
		t.Errorf("expected no verbosef output at normal level, got %q", stderr.String())
	}
}

func TestOutfHiddenAtQuietLevel(t *testing.T) {
	var stdout bytes.Buffer
	ui := New(nil, &stdout, &bytes.Buffer{}, false, LevelError, true, false)
	ui.Outf("%s", "data")
	if stdout.String() != "" {
		t.Errorf("expected no outf output at quiet level, got %q", stdout.String())
	}
}

// ---- OutToVerboseRedirect ----

func TestOutToVerboseRedirectOut(t *testing.T) {
	m := &Mock{}
	v := &OutToVerboseRedirect{UI: m}
	v.Out("redirected")
	if len(m.VerboseCalls) != 1 || m.VerboseCalls[0] != "redirected" {
		t.Errorf("expected Verbose call with message, got %v", m.VerboseCalls)
	}
}

func TestOutToVerboseRedirectOutf(t *testing.T) {
	m := &Mock{}
	v := &OutToVerboseRedirect{UI: m}
	v.Outf("kept repo %s on branch %s", "/p", "feat")
	if len(m.VerboseCalls) != 1 || m.VerboseCalls[0] != "kept repo /p on branch feat" {
		t.Errorf("expected formatted Verbose call, got %v", m.VerboseCalls)
	}
}

// ---- spinner: animate and finish branches ----

func TestSpinnerColorAnimateAndDone(t *testing.T) {
	var stderr bytes.Buffer
	ui := New(nil, &bytes.Buffer{}, &stderr, true, LevelInfo, false, false)
	spin := ui.Spinner("Working")
	spin.Stop()
	out := stderr.String()
	if !strings.Contains(out, "\x1b[32m✓") {
		t.Errorf("expected green done mark in color spinner, got %q", out)
	}
	if !strings.Contains(out, "Working") {
		t.Errorf("expected msg in color spinner output, got %q", out)
	}
}

func TestSpinnerFinishDefaultResult(t *testing.T) {
	var stderr bytes.Buffer
	s := newSpinner(&stderr, false, LevelInfo, "step")
	s.finish("custom result")
	out := stderr.String()
	if !strings.Contains(out, "custom result (") {
		t.Errorf("expected default result render, got %q", out)
	}
}

func TestSpinnerFinishDefaultResultColor(t *testing.T) {
	var stderr bytes.Buffer
	s := newSpinner(&stderr, true, LevelInfo, "step")
	s.finish("custom result")
	out := stderr.String()
	if !strings.Contains(out, "custom result (") {
		t.Errorf("expected default color result render, got %q", out)
	}
}

func TestSpinnerAnimateIterates(t *testing.T) {
	var stderr bytes.Buffer
	s := newSpinner(&stderr, true, LevelInfo, "step")
	time.Sleep(3 * spinnerInterval)
	s.finish("done")
	out := stderr.String()
	if !strings.Contains(out, "step") {
		t.Errorf("expected spinner msg in animated output, got %q", out)
	}
}

func TestSpinnerFinishDoneNonColor(t *testing.T) {
	var stderr bytes.Buffer
	s := newSpinner(&stderr, false, LevelInfo, "step")
	s.finish("done")
	out := stderr.String()
	if !strings.Contains(out, "✓(") {
		t.Errorf("expected done mark render, got %q", out)
	}
}

func TestSpinnerFinishFailedNonColor(t *testing.T) {
	var stderr bytes.Buffer
	s := newSpinner(&stderr, false, LevelInfo, "step")
	s.finish("failed: nope")
	out := stderr.String()
	if !strings.Contains(out, "failed (") || !strings.Contains(out, ": nope") {
		t.Errorf("expected failed render, got %q", out)
	}
}

// ---- mock helpers (cover statements in shared mock.go without editing it) ----

func TestMockHelperCoverage(t *testing.T) {
	m := NewTestMock(t)
	if m.InfoCalls != nil {
		t.Error("fresh mock should have nil InfoCalls")
	}

	m.SetLevel(LevelVerbose)
	if m.Level() != LevelVerbose {
		t.Errorf("Level() = %v, want verbose", m.Level())
	}
	m.SetAssumeYes(true)
	if !m.AssumeYes() {
		t.Error("AssumeYes() = false, want true")
	}
	m.SetQuiet(true)
	if !m.Quiet() {
		t.Error("Quiet() = false, want true")
	}
	m.SetQuiet(false)
	if m.Quiet() {
		t.Error("Quiet() = true after SetQuiet(false), want false")
	}

	m.IsInteractiveResult = true
	if !m.IsInteractive() {
		t.Error("IsInteractive() = false, want true")
	}

	if m.StdOut() == nil {
		t.Error("StdOut() returned nil")
	}
	if m.StdErr() == nil {
		t.Error("StdErr() returned nil")
	}

	spin := m.Spinnerf("build %d", 42)
	if len(m.SpinnerCalls) != 1 || m.SpinnerCalls[0] != "build 42" {
		t.Errorf("Spinnerf calls = %v, want [build 42]", m.SpinnerCalls)
	}
	spin.Stop()
	spin.StopError(errors.New("boom"))
}
