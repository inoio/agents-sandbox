package journeys

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// promptTypeAttempts bounds how often a prompt is retyped before the step gives
// up: a harness swallows input only while it starts, so a few attempts cover it.
const promptTypeAttempts = 10

// promptEchoWait is how long one attempt waits for the harness to echo the typed
// text before it is typed again.
const promptEchoWait = 3 * time.Second

// echoPollInterval is how often an attempt checks the output for the echo.
const echoPollInterval = 100 * time.Millisecond

// echoTerminal is the part of a driver.Session that typing needs.
type echoTerminal interface {
	Write(data []byte) error
	Output() []byte
	HasExited() bool
}

// typeUntilEchoed types text and waits for the harness to echo it, retyping
// when the echo does not come. A TUI that paints before it takes input (opencode
// answers its terminal-capability queries first) swallows an early write; on the
// host it swallowed the whole write, so the retype is the only text in the
// editor. A partly swallowed write or an echo later than echoWait would leave
// stray characters before the retyped prompt; a journey only needs the turn to
// be sent, so it tolerates that. Only output printed after the write counts as
// the echo, matched with driver.MarkerText so escape sequences and cursor-move
// spacing do not hide it; the prompt must therefore be text a repaint does not
// print on its own.
func typeUntilEchoed(terminal echoTerminal, text string, attempts int, echoWait time.Duration) error {
	needle := driver.MarkerText(text)
	for attempt := 1; attempt <= attempts; attempt++ {
		if terminal.HasExited() {
			return errors.New("the harness exited before it echoed the prompt")
		}
		offset := len(terminal.Output())
		if err := terminal.Write([]byte(text)); err != nil {
			return fmt.Errorf("type the prompt: %w", err)
		}
		if waitForEcho(terminal, offset, needle, echoWait) {
			return nil
		}
	}
	return fmt.Errorf("the harness did not echo the prompt %q after %d attempts", text, attempts)
}

// waitForEcho reports whether needle appears in the output printed after offset
// within echoWait.
func waitForEcho(terminal echoTerminal, offset int, needle string, echoWait time.Duration) bool {
	echoed := false
	_ = pollUntil(echoWait, echoPollInterval, func() (bool, error) {
		echoed = strings.Contains(driver.MarkerText(string(terminal.Output()[offset:])), needle)
		return echoed || terminal.HasExited(), nil
	}, func() error { return nil })
	return echoed
}
