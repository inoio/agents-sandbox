package termio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscardTypeAheadSkipsANonTerminalStdin(t *testing.T) {
	calls := 0
	original := discardTypeAhead
	discardTypeAhead = func(int) error { calls++; return nil }
	t.Cleanup(func() { discardTypeAhead = original })
	file, err := os.Create(filepath.Join(t.TempDir(), "stdin"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })

	(&printer{stdin: strings.NewReader("scripted\n"), isTerminal: func(int) bool { return true }}).DiscardTypeAhead()
	(&printer{stdin: file, isTerminal: func(int) bool { return false }}).DiscardTypeAhead()

	if calls != 0 {
		t.Errorf("discarded %d times, want none for a reader or a non-terminal file", calls)
	}
}

func TestDiscardTypeAheadDefaultsToTheProcessStdin(t *testing.T) {
	var flushed []int
	original := discardTypeAhead
	discardTypeAhead = func(fd int) error { flushed = append(flushed, fd); return nil }
	t.Cleanup(func() { discardTypeAhead = original })
	p := &printer{isTerminal: func(int) bool { return true }}
	p.getStdinReader()

	p.DiscardTypeAhead()

	if len(flushed) != 1 || flushed[0] != int(os.Stdin.Fd()) {
		t.Errorf("flushed %v, want the process stdin", flushed)
	}
	if p.stdinReader != nil {
		t.Error("the buffered reader survived the discard, so buffered input could still answer")
	}
}
