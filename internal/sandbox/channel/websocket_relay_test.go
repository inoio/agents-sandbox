package channel

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestWebsocketRelayJoinsContextCancellationWarning(t *testing.T) {
	logger := &proxyLog{}
	relay := newWebsocketRelay(7, logger)

	if !relay.beginIO() {
		t.Fatal("beginIO = false, want an active relay")
	}
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

func TestWebsocketRelayCancelsClosedWriteWarning(t *testing.T) {
	logger := &proxyLog{}
	relay := newWebsocketRelay(7, logger)

	if !relay.beginIO() {
		t.Fatal("beginIO = false, want first active write")
	}
	relay.finishIO(clientToUpstream, true, 0, 1, errors.New("write failed"))
	if !relay.beginIO() {
		t.Fatal("beginIO = false, want second active write")
	}
	relay.finishIO(clientToUpstream, true, 0, 1, net.ErrClosed)

	relay.group.mu.Lock()
	pending := relay.group.pending
	relay.group.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pending warnings = %d, want the closed write to cancel its warning", pending)
	}
	relay.shutdown()
}

func TestWebsocketRelayTracksShortWritesUntilLogged(t *testing.T) {
	logger := &proxyLog{}
	relay := newWebsocketRelay(7, logger)

	if !relay.beginIO() {
		t.Fatal("beginIO = false, want an active write")
	}
	relay.finishIO(clientToUpstream, true, 1, 2, nil)
	relay.group.mu.Lock()
	pending := relay.group.pending
	relay.group.mu.Unlock()
	if pending != 1 {
		t.Fatalf("pending warnings = %d, want one warning for a short write", pending)
	}
	logger.Printf(websocketCopyWarning, int64(7), errors.New("short write"))
	relay.shutdown()
}

func TestWebsocketRelayShutdownWaitsForActiveIO(t *testing.T) {
	client, peer := net.Pipe()
	defer peer.Close()
	relay := newWebsocketRelay(7, &proxyLog{})
	relay.setClient(client)
	if !relay.beginIO() {
		t.Fatal("beginIO = false, want an active connection operation")
	}

	shutdownDone := make(chan struct{})
	go func() {
		relay.shutdown()
		close(shutdownDone)
	}()
	if err := peer.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if _, err := peer.Read(make([]byte, 1)); err == nil {
		t.Fatal("peer read succeeded after relay shutdown closed the client")
	}
	relay.finishIO(clientToUpstream, false, 0, 1, net.ErrClosed)
	select {
	case <-shutdownDone:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not finish after active I/O completed")
	}
}

func TestTrackedWebsocketBodyCloseClosesItsRelay(t *testing.T) {
	upstream, peer := net.Pipe()
	defer peer.Close()
	relay := newWebsocketRelay(7, &proxyLog{})
	relay.setUpstream(upstream)
	body := trackedWebsocketBody{body: upstream, relay: relay}

	if err := body.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := body.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Errorf("Read after Close = %v, want net.ErrClosed", err)
	}
	if _, err := body.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Errorf("Write after Close = %v, want net.ErrClosed", err)
	}
}
