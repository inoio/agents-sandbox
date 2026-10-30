package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// TestServeOnlyContextCancelWiring verifies that calling the cancel function
// from serveOnlyContext causes <-ctx.Done() to fire within a timeout.
func TestServeOnlyContextCancelWiring(t *testing.T) {
	ctx, cancel := serveOnlyContext(context.Background())
	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(done)
	}()
	cancel()
	select {
	case <-done:
		// Success: context was cancelled.
	case <-time.After(500 * time.Millisecond):
		t.Error("ServeOnlyContext: cancel() did not cause ctx.Done() to fire")
	}
}

// TestServeOnlyContextNoStdinWatchWhenNotTerminal verifies that the stdin EOF
// watcher is not wired when stdin is not an interactive terminal: a closed
// non-TTY stdin must not cancel the context mid-flight (#387).
func TestServeOnlyContextNoStdinWatchWhenNotTerminal(t *testing.T) {
	restore := serveOnlyStdinReader
	serveOnlyStdinReader = func() io.Reader { return nil }
	t.Cleanup(func() { serveOnlyStdinReader = restore })

	ctx, cancel := serveOnlyContext(context.Background())
	defer cancel()

	select {
	case <-ctx.Done():
		t.Fatal("context was cancelled; stdin EOF watcher must not be wired when stdin is not a terminal")
	case <-time.After(200 * time.Millisecond):
		// Success: the context stays alive for a non-terminal stdin.
	}
}

// TestServeOnlyContextCancelsOnStdinEOF verifies that the stdin EOF watcher
// still cancels the context for an interactive terminal (Ctrl-D).
func TestServeOnlyContextCancelsOnStdinEOF(t *testing.T) {
	restore := serveOnlyStdinReader
	serveOnlyStdinReader = func() io.Reader { return strings.NewReader("") }
	t.Cleanup(func() { serveOnlyStdinReader = restore })

	ctx, cancel := serveOnlyContext(context.Background())
	defer cancel()

	select {
	case <-ctx.Done():
		// Success: stdin EOF cancelled the context.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("stdin EOF did not cancel the context")
	}
}

// TestServeOnlyStdinReaderSelectsOnTerminal pins the default reader: it watches
// os.Stdin only when stdin is an interactive terminal, and returns nil otherwise
// so the context is not cancelled immediately (#387).
func TestServeOnlyStdinReaderSelectsOnTerminal(t *testing.T) {
	restore := serveOnlyIsTerminal
	t.Cleanup(func() { serveOnlyIsTerminal = restore })

	tests := []struct {
		name       string
		isTerminal bool
		wantStdin  bool
	}{
		{name: "interactive terminal watches stdin", isTerminal: true, wantStdin: true},
		{name: "non-terminal does not watch stdin", isTerminal: false, wantStdin: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			serveOnlyIsTerminal = func(int) bool { return tc.isTerminal }
			got := serveOnlyStdinReader()
			if tc.wantStdin && got != os.Stdin {
				t.Errorf("serveOnlyStdinReader() = %v, want os.Stdin", got)
			}
			if !tc.wantStdin && got != nil {
				t.Errorf("serveOnlyStdinReader() = %v, want nil", got)
			}
		})
	}
}
