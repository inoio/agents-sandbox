package main

import (
	"context"
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
