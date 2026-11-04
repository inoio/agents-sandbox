package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/upgrade"
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

// TestRunServeOnlyWrapsContextForCancellation pins that the run path replaces
// the command context with the serve-only context when --serve-only is set: an
// interactive stdin EOF must cancel the context the upgrade check receives.
func TestRunServeOnlyWrapsContextForCancellation(t *testing.T) {
	initTestProjectDir(t)

	oldPrepare := prepareMSBRuntime
	prepareMSBRuntime = func(context.Context, termio.Logger, string) error { return nil }
	t.Cleanup(func() { prepareMSBRuntime = oldPrepare })

	origCheck := upgradeCheck
	t.Cleanup(func() { upgradeCheck = origCheck })
	var done <-chan struct{}
	upgradeCheck = func(ctx context.Context, _ upgrade.Options) (upgrade.Result, error) {
		done = ctx.Done()
		return upgrade.Result{Exit: true}, nil
	}

	restoreReader := serveOnlyStdinReader
	serveOnlyStdinReader = func() io.Reader { return strings.NewReader("") }
	t.Cleanup(func() { serveOnlyStdinReader = restoreReader })

	root, _ := setupRunMocks(t, &msb.MockMsbClient{}, &msb.MockSandbox{}, "run", "--serve-only")
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if done == nil {
		t.Fatal("upgrade check did not run with a cancellable context")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("serve-only context was not cancelled on stdin EOF")
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

// TestRunFuncServeOnlyContextError drives runFunc directly with --serve-only
// and a failing preflight so it takes the serve-only context branch and then
// returns the preflight error from runFunc itself.
func TestRunFuncServeOnlyContextError(t *testing.T) {
	initTestProjectDir(t)
	configpaths.WithMockConfigPaths(t)
	doctor.MockedCheckAll(t, false)
	ui := &termio.Mock{}

	cmd := buildRunCmd(ui)
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Set(flagServeOnly, "true"); err != nil {
		t.Fatalf("set serve-only: %v", err)
	}
	fn := runFunc(ui)
	if err := fn(cmd, nil); err == nil || err.Error() != "preflight failed" {
		t.Fatalf("expected preflight failure, got %v", err)
	}
}
