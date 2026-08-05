package termio

import (
	"bytes"
	"fmt"
	"io"
)

type ErrorCall struct {
	Msg string
	Err error
}

type Mock struct {
	InfoCalls    []string
	WarnCalls    []string
	ErrorCalls   []ErrorCall
	VerboseCalls []string
	OutCalls     []string
	SpinnerCalls []string
	StdOutBuffer bytes.Buffer
	StdErrBuffer bytes.Buffer

	IsInteractiveResult bool

	SelectFn         func(prompt string, choices []Choice, defaultKey string) (string, error)
	ConfirmDefaultFn func(prompt string, defaultYes bool) (bool, error)
	InputFn          func(prompt, defaultValue string) (string, error)
}

type mockSpinner struct{}

func (m *mockSpinner) Stop() {}

func (m *mockSpinner) StopError(error) {}

func (m *Mock) Info(msg string) {
	m.InfoCalls = append(m.InfoCalls, msg)
}

func (m *Mock) Infof(format string, args ...any) {
	m.InfoCalls = append(m.InfoCalls, fmt.Sprintf(format, args...))
}

func (m *Mock) Warn(msg string) {
	m.WarnCalls = append(m.WarnCalls, msg)
}

func (m *Mock) Warnf(format string, args ...any) {
	m.WarnCalls = append(m.WarnCalls, fmt.Sprintf(format, args...))
}

func (m *Mock) Error(msg string, err error) {
	m.ErrorCalls = append(m.ErrorCalls, ErrorCall{Msg: msg, Err: err})
}

func (m *Mock) Errorf(format string, args ...any) {
	//nolint:exhaustruct // Err intentionally nil (set only via Error method)
	m.ErrorCalls = append(m.ErrorCalls, ErrorCall{Msg: fmt.Sprintf(format, args...)})
}

func (m *Mock) Verbose(msg string) {
	m.VerboseCalls = append(m.VerboseCalls, msg)
}

func (m *Mock) Verbosef(format string, args ...any) {
	m.VerboseCalls = append(m.VerboseCalls, fmt.Sprintf(format, args...))
}

func (m *Mock) Out(msg string) {
	m.OutCalls = append(m.OutCalls, msg)
}

func (m *Mock) Outf(format string, args ...any) {
	m.OutCalls = append(m.OutCalls, fmt.Sprintf(format, args...))
}

func (m *Mock) Spinner(msg string) Spinner {
	m.SpinnerCalls = append(m.SpinnerCalls, msg)
	return &mockSpinner{}
}
func (m *Mock) Spinnerf(format string, args ...any) Spinner {
	m.SpinnerCalls = append(m.SpinnerCalls, fmt.Sprintf(format, args...))
	return &mockSpinner{}
}

func (m *Mock) IsInteractive() bool {
	return m.IsInteractiveResult
}

func (m *Mock) Select(prompt string, choices []Choice, defaultKey string) (string, error) {
	if m.SelectFn != nil {
		return m.SelectFn(prompt, choices, defaultKey)
	}
	return defaultKey, nil
}

func (m *Mock) ConfirmDefault(prompt string, defaultYes bool) (bool, error) {
	if m.ConfirmDefaultFn != nil {
		return m.ConfirmDefaultFn(prompt, defaultYes)
	}
	return defaultYes, nil
}

func (m *Mock) Input(prompt, defaultValue string) (string, error) {
	if m.InputFn != nil {
		return m.InputFn(prompt, defaultValue)
	}
	return defaultValue, nil
}

func (m *Mock) StdOut() io.Writer {
	return &m.StdOutBuffer
}

func (m *Mock) StdErr() io.Writer {
	return &m.StdErrBuffer
}
