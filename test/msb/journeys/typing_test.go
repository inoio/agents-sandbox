package journeys

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTerminal swallows the first swallowed writes whole, the way a TUI still
// waiting for its terminal-capability replies consumes early input, and echoes
// every later write into its output.
type fakeTerminal struct {
	mu        sync.Mutex
	output    []byte
	swallowed int
	writes    []string
	exited    bool
}

func (f *fakeTerminal) Write(data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, string(data))
	if f.swallowed > 0 {
		f.swallowed--
		return nil
	}
	f.output = append(f.output, data...)
	return nil
}

func (f *fakeTerminal) Output() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.output...)
}

func (f *fakeTerminal) HasExited() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exited
}

const fastEchoWait = 50 * time.Millisecond

func TestTypeUntilEchoedRetypesASwallowedPrompt(t *testing.T) {
	terminal := &fakeTerminal{swallowed: 1}

	if err := typeUntilEchoed(terminal, "hello", 3, fastEchoWait); err != nil {
		t.Fatalf("typeUntilEchoed: %v", err)
	}
	if len(terminal.writes) != 2 {
		t.Errorf("writes = %q, want the prompt typed twice", terminal.writes)
	}
	if got := string(terminal.Output()); got != "hello" {
		t.Errorf("echoed output = %q, want the prompt once", got)
	}
}

func TestTypeUntilEchoedTypesOnceWhenTheFirstWriteEchoes(t *testing.T) {
	terminal := &fakeTerminal{}

	if err := typeUntilEchoed(terminal, "hello", 3, fastEchoWait); err != nil {
		t.Fatalf("typeUntilEchoed: %v", err)
	}
	if len(terminal.writes) != 1 {
		t.Errorf("writes = %q, want one", terminal.writes)
	}
}

// An echo is only what the terminal printed after the write: a placeholder or an
// earlier message already on screen that contains the prompt text is not one.
func TestTypeUntilEchoedIgnoresThePromptTextAlreadyOnScreen(t *testing.T) {
	terminal := &fakeTerminal{output: []byte("hello from an earlier turn"), swallowed: 3}

	err := typeUntilEchoed(terminal, "hello", 3, fastEchoWait)

	if err == nil {
		t.Fatal("typeUntilEchoed accepted text that was on screen before the write")
	}
}

func TestTypeUntilEchoedFailsNamingThePromptWhenEveryAttemptIsSwallowed(t *testing.T) {
	terminal := &fakeTerminal{swallowed: 3}

	err := typeUntilEchoed(terminal, "hello", 3, fastEchoWait)

	if err == nil {
		t.Fatal("typeUntilEchoed succeeded although nothing was echoed")
	}
	if !strings.Contains(err.Error(), `"hello"`) || !strings.Contains(err.Error(), "3 attempts") {
		t.Errorf("error = %v, want it to name the prompt and the attempts", err)
	}
	if len(terminal.writes) != 3 {
		t.Errorf("writes = %d, want 3", len(terminal.writes))
	}
}

func TestTypeUntilEchoedStopsWhenTheHarnessExited(t *testing.T) {
	terminal := &fakeTerminal{swallowed: 3, exited: true}

	err := typeUntilEchoed(terminal, "hello", 3, fastEchoWait)

	if err == nil || !strings.Contains(err.Error(), "exited") {
		t.Fatalf("error = %v, want an exited error", err)
	}
	if len(terminal.writes) != 0 {
		t.Errorf("writes = %d, want none to an exited harness", len(terminal.writes))
	}
}

// A diff-painting TUI renders the typed text with cursor moves between the
// characters; the echo match ignores escape sequences and whitespace.
func TestTypeUntilEchoedMatchesAnEchoInterleavedWithEscapes(t *testing.T) {
	terminal := &echoRewritingTerminal{fakeTerminal: fakeTerminal{}, rewrite: func(text string) string {
		return strings.Join(strings.Split(text, ""), "\x1b[1C")
	}}

	if err := typeUntilEchoed(terminal, "hello", 1, fastEchoWait); err != nil {
		t.Fatalf("typeUntilEchoed: %v", err)
	}
}

type echoRewritingTerminal struct {
	fakeTerminal

	rewrite func(string) string
}

func (e *echoRewritingTerminal) Write(data []byte) error {
	return e.fakeTerminal.Write([]byte(e.rewrite(string(data))))
}

// returnsWithin runs wait and fails the test unless it returns well before the
// wait's own budget, which proves the wait ended early.
func returnsWithin(t *testing.T, wait func() bool) bool {
	t.Helper()
	result := make(chan bool, 1)
	go func() { result <- wait() }()
	select {
	case got := <-result:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("the wait ran into its budget instead of ending early")
		return false
	}
}

func TestWaitForEchoEndsAsSoonAsTheEchoShows(t *testing.T) {
	terminal := &fakeTerminal{output: []byte("hello")}

	if !returnsWithin(t, func() bool { return waitForEcho(terminal, 0, "hello", time.Hour) }) {
		t.Error("waitForEcho = false, want the echo found")
	}
}

func TestWaitForEchoEndsAsSoonAsTheHarnessExited(t *testing.T) {
	terminal := &fakeTerminal{exited: true}

	if returnsWithin(t, func() bool { return waitForEcho(terminal, 0, "hello", time.Hour) }) {
		t.Error("waitForEcho = true, want no echo from an exited harness")
	}
}

func TestWaitForEchoWaitsForALateEcho(t *testing.T) {
	terminal := &fakeTerminal{}
	go func() {
		time.Sleep(3 * echoPollInterval) //testenv:allow the echo has to arrive after the first poll
		_ = terminal.Write([]byte("hello"))
	}()

	if !waitForEcho(terminal, 0, "hello", 5*time.Second) {
		t.Error("waitForEcho = false, want the late echo found")
	}
}
