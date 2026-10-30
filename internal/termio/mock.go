//deadcode:ignore reason="test support: referenced only by _test.go files"
package termio

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"testing"
)

// NewTestMock returns an empty termio.Mock for tests.
func NewTestMock(tb testing.TB) Mock {
	tb.Helper()
	return Mock{}
}

type ErrorCall struct {
	Msg string
	Err error
}

// Mock is a termio.UI double safe for concurrent use by the production
// goroutines that log through the UI (e.g. the notify watcher) alongside the
// main session goroutine. Recorded calls are guarded by mu.
type Mock struct {
	mu sync.Mutex

	InfoCalls         []string
	WarnCalls         []string
	ErrorCalls        []ErrorCall
	VerboseCalls      []string
	OutCalls          []string
	PrintCalls        []string
	SelectCalls       []string
	InputCalls        []string
	SpinnerCalls      []string
	SpinnerStops      int
	SpinnerStopErrors []error
	ConfirmCalls      []string
	AcknowledgeCalls  []string
	StdOutBuffer      bytes.Buffer
	StdErrBuffer      bytes.Buffer

	level               Level
	assumeYes           bool
	quiet               bool
	IsInteractiveResult bool
	ConfirmResult       bool
	ConfirmErr          error
	AcknowledgeErr      error

	SelectFn      func(prompt string, choices []Choice, defaultKey string) (string, error)
	InputFn       func(prompt, defaultValue string) (string, error)
	SecretInputFn func(prompt string) (string, error)
}

type mockSpinner struct{ owner *Mock }

func (s *mockSpinner) Stop() {
	if s.owner == nil {
		return
	}
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	s.owner.SpinnerStops++
}

func (s *mockSpinner) StopError(err error) {
	if s.owner == nil {
		return
	}
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	s.owner.SpinnerStopErrors = append(s.owner.SpinnerStopErrors, err)
}

func (m *Mock) Info(msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.InfoCalls = append(m.InfoCalls, msg)
}

func (m *Mock) Infof(format string, args ...any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.InfoCalls = append(m.InfoCalls, fmt.Sprintf(format, args...))
}

func (m *Mock) Warn(msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.WarnCalls = append(m.WarnCalls, msg)
}

func (m *Mock) Warnf(format string, args ...any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.WarnCalls = append(m.WarnCalls, fmt.Sprintf(format, args...))
}

func (m *Mock) Error(msg string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ErrorCalls = append(m.ErrorCalls, ErrorCall{Msg: msg, Err: err})
}

func (m *Mock) Errorf(format string, args ...any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ErrorCalls = append(m.ErrorCalls, ErrorCall{Msg: fmt.Sprintf(format, args...)})
}

func (m *Mock) Verbose(msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.VerboseCalls = append(m.VerboseCalls, msg)
}

func (m *Mock) Verbosef(format string, args ...any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.VerboseCalls = append(m.VerboseCalls, fmt.Sprintf(format, args...))
}

func (m *Mock) Out(msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.OutCalls = append(m.OutCalls, stripANSICodes(msg))
}

// Print records a data line written regardless of the quiet setting, mirroring
// the production printer's Print.
func (m *Mock) Print(msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.PrintCalls = append(m.PrintCalls, stripANSICodes(msg))
}

// Header records a table header line in OutCalls. The mock never applies ANSI
// styling, matching the color-disabled printer path used by tests.
func (m *Mock) Header(msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.OutCalls = append(m.OutCalls, stripANSICodes(msg))
}

// NewTable returns an empty aligned Table that records output in the mock.
func (m *Mock) NewTable(headers ...string) *Table {
	return &Table{ui: m, headers: headers}
}

func (m *Mock) Outf(format string, args ...any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.OutCalls = append(m.OutCalls, fmt.Sprintf(format, args...))
}

func (m *Mock) Spinner(msg string) Spinner {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SpinnerCalls = append(m.SpinnerCalls, msg)
	return &mockSpinner{owner: m}
}
func (m *Mock) Spinnerf(format string, args ...any) Spinner {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SpinnerCalls = append(m.SpinnerCalls, fmt.Sprintf(format, args...))
	return &mockSpinner{owner: m}
}

func (m *Mock) SetLevel(level Level) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.level = level
}

func (m *Mock) SetAssumeYes(assumeYes bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.assumeYes = assumeYes
}

func (m *Mock) SetQuiet(quiet bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.quiet = quiet
}

func (m *Mock) Level() Level {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.level
}

func (m *Mock) AssumeYes() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.assumeYes
}

func (m *Mock) Quiet() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.quiet
}

func (m *Mock) IsInteractive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.IsInteractiveResult
}

func (m *Mock) Select(prompt string, choices []Choice, defaultKey string) (string, error) {
	m.mu.Lock()
	m.SelectCalls = append(m.SelectCalls, prompt)
	selectFn := m.SelectFn
	m.mu.Unlock()
	if selectFn != nil {
		return selectFn(prompt, choices, defaultKey)
	}
	return defaultKey, nil
}

func (m *Mock) Input(prompt, defaultValue string) (string, error) {
	m.mu.Lock()
	m.InputCalls = append(m.InputCalls, prompt)
	inputFn := m.InputFn
	m.mu.Unlock()
	if inputFn != nil {
		return inputFn(prompt, defaultValue)
	}
	return defaultValue, nil
}

func (m *Mock) SecretInput(prompt string) (string, error) {
	m.mu.Lock()
	secretInputFn := m.SecretInputFn
	m.mu.Unlock()
	if secretInputFn != nil {
		return secretInputFn(prompt)
	}
	return "", nil
}

func (m *Mock) Confirm(prompt string, defaultYes bool) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.assumeYes {
		return true, nil
	}
	if !m.IsInteractiveResult {
		return false, ErrConfirmationRequired
	}
	m.ConfirmCalls = append(m.ConfirmCalls, prompt)
	return m.ConfirmResult, m.ConfirmErr
}

// Acknowledge records the notice regardless of IsInteractiveResult: both the
// interactive and the non-interactive path call it, and the caller decides
// whether the acknowledgment is recorded.
func (m *Mock) Acknowledge(message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.AcknowledgeCalls = append(m.AcknowledgeCalls, message)
	return m.AcknowledgeErr
}

func (m *Mock) StdOut() io.Writer {
	return &m.StdOutBuffer
}

func (m *Mock) StdErr() io.Writer {
	return &m.StdErrBuffer
}
