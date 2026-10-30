package termio

import (
	"errors"
	"sync"
	"testing"
)

func TestMockRecordsOutputCalls(t *testing.T) {
	m := &Mock{}
	m.Info("ok")
	m.Infof("hello %s", "world")
	m.Warn("careful")
	m.Warnf("%s", "danger")
	m.Error("boom", errors.New("nope"))
	m.Errorf("fail %d", 1)
	m.Verbose("debug")
	m.Verbosef("trace %s", "x")
	m.Out("data")
	m.Outf("row %d", 1)

	if len(m.InfoCalls) != 2 {
		t.Errorf("expected 2 success calls, got %d", len(m.InfoCalls))
	}
	if len(m.WarnCalls) != 2 {
		t.Errorf("expected 2 warn calls, got %d", len(m.WarnCalls))
	}
	if len(m.ErrorCalls) != 2 {
		t.Errorf("expected 2 error calls, got %d", len(m.ErrorCalls))
	}
	if len(m.VerboseCalls) != 2 {
		t.Errorf("expected 2 verbose calls, got %d", len(m.VerboseCalls))
	}
	if len(m.OutCalls) != 2 {
		t.Errorf("expected 2 out calls, got %d", len(m.OutCalls))
	}
}

func TestMockErrorStoresMessageAndError(t *testing.T) {
	m := &Mock{}
	m.Error("build failed", errors.New("exit 1"))
	if len(m.ErrorCalls) != 1 {
		t.Fatalf("expected 1 error call, got %d", len(m.ErrorCalls))
	}
	if m.ErrorCalls[0].Msg != "build failed" {
		t.Errorf("expected message 'build failed', got %q", m.ErrorCalls[0].Msg)
	}
	if m.ErrorCalls[0].Err == nil || m.ErrorCalls[0].Err.Error() != "exit 1" {
		t.Errorf("expected err exit 1, got %v", m.ErrorCalls[0].Err)
	}
}

func TestMockPrintRecordsDataLine(t *testing.T) {
	m := &Mock{}
	m.Print("agent-1")
	if len(m.PrintCalls) != 1 || m.PrintCalls[0] != "agent-1" {
		t.Errorf("PrintCalls = %v, want [agent-1]", m.PrintCalls)
	}
}

func TestMockSpinnerRecordsStopsAndStopErrors(t *testing.T) {
	m := &Mock{}
	spin := m.Spinner("work")
	spin.Stop()
	stopErr := errors.New("x")
	spin.StopError(stopErr)
	if len(m.SpinnerCalls) != 1 {
		t.Errorf("expected 1 spinner call, got %d", len(m.SpinnerCalls))
	}
	if m.SpinnerStops != 1 {
		t.Errorf("SpinnerStops = %d, want 1", m.SpinnerStops)
	}
	if len(m.SpinnerStopErrors) != 1 || !errors.Is(m.SpinnerStopErrors[0], stopErr) {
		t.Errorf("SpinnerStopErrors = %v, want [x]", m.SpinnerStopErrors)
	}
}

// TestMockSpinnerWithNilOwnerIsNoOp pins the guard that lets a mockSpinner be
// used without a Mock: both methods return instead of dereferencing a nil owner.
func TestMockSpinnerWithNilOwnerIsNoOp(t *testing.T) {
	spin := &mockSpinner{}
	spin.Stop()
	spin.StopError(errors.New("x"))
}

func TestMockPromptDefaults(t *testing.T) {
	m := &Mock{}

	got, err := m.Select("prompt", []Choice{{Key: "a"}}, "a")
	if err != nil || got != "a" {
		t.Errorf("expected default select, got %q, %v", got, err)
	}

	value, err := m.Input("prompt", "default")
	if err != nil || value != "default" {
		t.Errorf("expected default input, got %q, %v", value, err)
	}
}

func TestMockRecordsPrompts(t *testing.T) {
	m := &Mock{}
	_, _ = m.Select("pick one", []Choice{{Key: "a"}}, "a")
	_, _ = m.Input("type value", "default")

	if len(m.SelectCalls) != 1 || m.SelectCalls[0] != "pick one" {
		t.Errorf("SelectCalls = %v, want [pick one]", m.SelectCalls)
	}
	if len(m.InputCalls) != 1 || m.InputCalls[0] != "type value" {
		t.Errorf("InputCalls = %v, want [type value]", m.InputCalls)
	}
}

func TestMockConfirmRecordsPromptAndReturnsConfigured(t *testing.T) {
	m := &Mock{IsInteractiveResult: true, ConfirmResult: true}
	got, err := m.Confirm("Remove it?", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Error("expected configured ConfirmResult true")
	}
	if len(m.ConfirmCalls) != 1 || m.ConfirmCalls[0] != "Remove it?" {
		t.Errorf("expected recorded prompt, got %v", m.ConfirmCalls)
	}

	boom := errors.New("boom")
	m = &Mock{IsInteractiveResult: true, ConfirmErr: boom}
	got, err = m.Confirm("Remove it?", true)
	if !errors.Is(err, boom) {
		t.Errorf("expected configured error, got %v", err)
	}
	if got {
		t.Error("expected false result alongside error")
	}
}

func TestMockConfirmNonInteractiveRequiresConfirmation(t *testing.T) {
	m := &Mock{ConfirmResult: true}
	got, err := m.Confirm("Remove it?", false)
	if !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("expected ErrConfirmationRequired, got %v", err)
	}
	if got {
		t.Error("expected false when confirmation is required")
	}
	if len(m.ConfirmCalls) != 0 {
		t.Errorf("non-interactive confirm must not record a prompt, got: %v", m.ConfirmCalls)
	}
}

func TestMockConfirmReturnsTrueWithoutRecordingWhenAssumeYes(t *testing.T) {
	m := &Mock{ConfirmResult: false}
	m.SetAssumeYes(true)

	got, err := m.Confirm("Remove it?", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Error("expected true under assume-yes")
	}
	if len(m.ConfirmCalls) != 0 {
		t.Errorf("expected no recorded prompt under assume-yes, got: %v", m.ConfirmCalls)
	}
}

func TestMockIsSafeForConcurrentUse(t *testing.T) {
	m := &Mock{}
	const workers = 8
	const callsPerWorker = 50
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for range callsPerWorker {
				m.Verbose("v")
				m.Verbosef("v %d", 1)
				m.Info("i")
				m.Warn("w")
				m.Out("o")
				m.Spinner("s")
				_ = m.Level()
				_ = m.AssumeYes()
				_ = m.Quiet()
				_ = m.IsInteractive()
			}
		})
	}
	wg.Wait()
	if want := workers * callsPerWorker; len(m.VerboseCalls) != want*2 {
		t.Errorf("VerboseCalls = %d, want %d", len(m.VerboseCalls), want*2)
	}
}

func TestMockAcknowledgeRecordsAndReturnsError(t *testing.T) {
	m := &Mock{AcknowledgeErr: errors.New("boom")}
	if err := m.Acknowledge("note"); err == nil {
		t.Fatal("Acknowledge() error = nil, want the configured error")
	}
	if len(m.AcknowledgeCalls) != 1 || m.AcknowledgeCalls[0] != "note" {
		t.Errorf("AcknowledgeCalls = %v, want [\"note\"]", m.AcknowledgeCalls)
	}
}

func TestMockPromptFnsOverrideDefaults(t *testing.T) {
	m := &Mock{
		SelectFn:      func(string, []Choice, string) (string, error) { return "x", nil },
		InputFn:       func(string, string) (string, error) { return "y", nil },
		SecretInputFn: func(string) (string, error) { return "secret", nil },
	}

	got, _ := m.Select("", nil, "a")
	if got != "x" {
		t.Errorf("expected select override x, got %q", got)
	}
	value, _ := m.Input("", "default")
	if value != "y" {
		t.Errorf("expected input override y, got %q", value)
	}
	secret, _ := m.SecretInput("")
	if secret != "secret" {
		t.Errorf("expected secret input override, got %q", secret)
	}
}

func TestMockSecretInputDefault(t *testing.T) {
	var m Mock
	secret, err := m.SecretInput("secret")
	if err != nil || secret != "" {
		t.Fatalf("default secret input = %q, %v", secret, err)
	}
}
