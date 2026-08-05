package termio

import (
	"errors"
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

func TestMockSpinnerIsNoOp(t *testing.T) {
	m := &Mock{}
	spin := m.Spinner("work")
	spin.Stop()
	spin.StopError(errors.New("x"))
	if len(m.SpinnerCalls) != 1 {
		t.Errorf("expected 1 spinner call, got %d", len(m.SpinnerCalls))
	}
}

func TestMockPromptDefaults(t *testing.T) {
	m := &Mock{}

	got, err := m.Select("prompt", []Choice{{Key: "a"}}, "a")
	if err != nil || got != "a" {
		t.Errorf("expected default select, got %q, %v", got, err)
	}

	confirmed, err := m.ConfirmDefault("prompt", true)
	if err != nil || !confirmed {
		t.Errorf("expected default confirm true, got %v, %v", confirmed, err)
	}

	value, err := m.Input("prompt", "default")
	if err != nil || value != "default" {
		t.Errorf("expected default input, got %q, %v", value, err)
	}
}

func TestMockPromptFnsOverrideDefaults(t *testing.T) {
	m := &Mock{
		SelectFn:         func(string, []Choice, string) (string, error) { return "x", nil },
		ConfirmDefaultFn: func(string, bool) (bool, error) { return false, nil },
		InputFn:          func(string, string) (string, error) { return "y", nil },
	}

	got, _ := m.Select("", nil, "a")
	if got != "x" {
		t.Errorf("expected select override x, got %q", got)
	}
	confirmed, _ := m.ConfirmDefault("", true)
	if confirmed {
		t.Error("expected confirm override false")
	}
	value, _ := m.Input("", "default")
	if value != "y" {
		t.Errorf("expected input override y, got %q", value)
	}
}
