package channel

import (
	"context"
	"testing"
)

func TestWebsocketRelayJoinsContextCancellationWarning(t *testing.T) {
	logger := &proxyLog{}
	relay := newWebsocketRelay(7, logger)

	relay.finishIO(upstreamToClient, false, 0, 1, context.Canceled)

	relay.group.mu.Lock()
	pending := relay.group.pending
	relay.group.mu.Unlock()
	if pending != 1 {
		t.Fatalf("pending warnings = %d, want context cancellation to await goproxy's warning", pending)
	}
	logger.beginShutdown()
	logger.Printf(websocketCopyWarning, int64(7), context.Canceled)
	relay.shutdown()
}
