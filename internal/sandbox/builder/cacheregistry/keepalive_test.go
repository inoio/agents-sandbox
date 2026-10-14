package cacheregistry

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestKeepAliveRefreshesRegistryUntilStopped asserts the keep-alive loop touches
// the daemon on its interval, and that stop() is deterministic (no touch
// happens afterwards) and idempotent.
func TestKeepAliveRefreshesRegistryUntilStopped(t *testing.T) {
	restoreInterval := registryKeepAliveInterval
	registryKeepAliveInterval = time.Millisecond
	t.Cleanup(func() { registryKeepAliveInterval = restoreInterval })

	restoreTouch := touchRegistry
	var touches atomic.Int32
	touchRegistry = func(context.Context, Endpoint) { touches.Add(1) }
	t.Cleanup(func() { touchRegistry = restoreTouch })

	endpoint := Endpoint{Host: GuestHost, Port: 5000, Token: "tok"}
	stop := endpoint.KeepAlive(context.Background())
	waitForRegistryTouches(t, &touches, 1)
	stop()
	stop() // idempotent: must not panic or block

	after := touches.Load()
	time.Sleep(20 * time.Millisecond)
	if got := touches.Load(); got != after {
		t.Errorf("registry touched %d more times after stop", got-after)
	}
}

// TestKeepAliveStopsWhenContextCancelled asserts a cancelled context ends the
// loop, so stop() returns without waiting on a live goroutine.
func TestKeepAliveStopsWhenContextCancelled(t *testing.T) {
	restoreInterval := registryKeepAliveInterval
	registryKeepAliveInterval = time.Millisecond
	t.Cleanup(func() { registryKeepAliveInterval = restoreInterval })

	restoreTouch := touchRegistry
	touchRegistry = func(context.Context, Endpoint) {}
	t.Cleanup(func() { touchRegistry = restoreTouch })

	ctx, cancel := context.WithCancel(context.Background())
	stop := Endpoint{Host: GuestHost, Port: 5000}.KeepAlive(ctx)
	cancel()

	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not return after the context was cancelled")
	}
}

func waitForRegistryTouches(t *testing.T, touches *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if touches.Load() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("registry touched %d times, want >= %d", touches.Load(), want)
}
