package termio

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// TestDiscardTypeAheadDropsQueuedInput pins the flush on a real terminal: a
// line typed before the discard is gone, a line typed after it is read.
func TestDiscardTypeAheadDropsQueuedInput(t *testing.T) {
	controller, terminal, err := pty.Open()
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	t.Cleanup(func() { _ = controller.Close(); _ = terminal.Close() })
	p := &printer{stdin: terminal, isTerminal: term.IsTerminal}
	if _, err := controller.WriteString("stale\n"); err != nil {
		t.Fatal(err)
	}
	waitForQueuedInput(t, terminal)

	p.DiscardTypeAhead()

	if _, err := controller.WriteString("fresh\n"); err != nil {
		t.Fatal(err)
	}
	line, err := p.getStdinReader().ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(line) != "fresh" {
		t.Errorf("read %q after the discard, want the line typed after it", line)
	}
}

// waitForQueuedInput waits until the line discipline has queued what the
// controller wrote, so the discard has something to drop.
func waitForQueuedInput(t *testing.T, terminal *os.File) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if queued, err := unix.IoctlGetInt(int(terminal.Fd()), unix.TIOCINQ); err != nil || queued > 0 {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("the written line never reached the terminal's input queue")
}
