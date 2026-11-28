package channel

import (
	"errors"
	"io"
	"net"
	"sync"
)

const (
	clientToUpstream = iota
	upstreamToClient
)

// websocketRelayGroup joins copy-warning callbacks for goproxy's 16-bit
// session number. A wrapped session number can identify several live relays, so
// every relay waits for the group's pending warnings to drain.
type websocketRelayGroup struct {
	mu         sync.Mutex
	changed    *sync.Cond
	pending    int
	references int
}

func newWebsocketRelayGroup() *websocketRelayGroup {
	//exhaustruct:ignore // the condition and counters start empty
	group := &websocketRelayGroup{}
	group.changed = sync.NewCond(&group.mu)
	return group
}

func (g *websocketRelayGroup) retain() {
	g.mu.Lock()
	g.references++
	g.mu.Unlock()
}

func (g *websocketRelayGroup) release() {
	g.mu.Lock()
	g.references--
	g.changed.Broadcast()
	g.mu.Unlock()
}

func (g *websocketRelayGroup) addPendingWarning() {
	g.mu.Lock()
	g.pending++
	g.mu.Unlock()
}

func (g *websocketRelayGroup) cancelPendingWarning() {
	g.mu.Lock()
	if g.pending > 0 {
		g.pending--
	}
	g.changed.Broadcast()
	g.mu.Unlock()
}

func (g *websocketRelayGroup) completePendingWarning() {
	g.cancelPendingWarning()
}

func (g *websocketRelayGroup) waitForWarnings() {
	g.mu.Lock()
	for g.pending > 0 {
		g.changed.Wait()
	}
	g.mu.Unlock()
}

func (g *websocketRelayGroup) idle() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.references == 0 && g.pending == 0
}

// websocketRelay tracks the two io.Copy directions goproxy launches for a
// WebSocket. A copy error becomes pending before it returns to goproxy, and the
// logger clears it when goproxy emits the corresponding warning.
type websocketRelay struct {
	sessionID uint16
	logger    *proxyLog
	group     *websocketRelayGroup

	mu       sync.Mutex
	changed  *sync.Cond
	client   net.Conn
	upstream io.ReadWriteCloser
	active   int
	pending  [2]bool
	stopping bool

	shutdownOnce sync.Once
	shutdownDone chan struct{}
}

func newWebsocketRelay(sessionID uint16, logger *proxyLog) *websocketRelay {
	group := logger.registerWebsocketRelay(sessionID)
	//exhaustruct:ignore // endpoints and counters are bound as the upgrade passes through goproxy
	relay := &websocketRelay{
		sessionID: sessionID, logger: logger, group: group, shutdownDone: make(chan struct{}),
	}
	relay.changed = sync.NewCond(&relay.mu)
	return relay
}

func (r *websocketRelay) setClient(connection net.Conn) {
	r.mu.Lock()
	r.client = connection
	r.mu.Unlock()
}

func (r *websocketRelay) setUpstream(connection io.ReadWriteCloser) {
	r.mu.Lock()
	r.upstream = connection
	r.mu.Unlock()
}

func (r *websocketRelay) beginIO() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping {
		return false
	}
	r.active++
	return true
}

func (r *websocketRelay) finishIO(direction int, write bool, count, requested int, err error) {
	addWarning := false
	cancelWarning := false
	r.mu.Lock()
	switch {
	case errors.Is(err, net.ErrClosed):
		if r.pending[direction] {
			r.pending[direction] = false
			cancelWarning = true
		}
	case err != nil && (write || !errors.Is(err, io.EOF)):
		if !r.pending[direction] {
			r.pending[direction] = true
			addWarning = true
		}
	case write && err == nil && count < requested:
		if !r.pending[direction] {
			r.pending[direction] = true
			addWarning = true
		}
	}
	if addWarning {
		r.group.addPendingWarning()
	}
	if cancelWarning {
		r.group.cancelPendingWarning()
	}
	r.active--
	r.changed.Broadcast()
	r.mu.Unlock()
}

func (r *websocketRelay) shutdown() {
	r.shutdownOnce.Do(func() {
		r.mu.Lock()
		r.stopping = true
		client, upstream := r.client, r.upstream
		r.mu.Unlock()
		if client != nil {
			_ = client.Close()
		}
		if upstream != nil {
			_ = upstream.Close()
		}

		r.mu.Lock()
		for r.active > 0 {
			r.changed.Wait()
		}
		r.mu.Unlock()
		r.group.waitForWarnings()
		r.logger.unregisterWebsocketRelay(r.sessionID, r.group)
		close(r.shutdownDone)
	})
	<-r.shutdownDone
}

type trackedWebsocketBody struct {
	body  io.ReadWriteCloser
	relay *websocketRelay
}

func (b trackedWebsocketBody) Read(buffer []byte) (int, error) {
	if !b.relay.beginIO() {
		return 0, net.ErrClosed
	}
	count, err := b.body.Read(buffer)
	b.relay.finishIO(upstreamToClient, false, count, len(buffer), err)
	return count, err
}

func (b trackedWebsocketBody) Write(buffer []byte) (int, error) {
	if !b.relay.beginIO() {
		return 0, net.ErrClosed
	}
	count, err := b.body.Write(buffer)
	b.relay.finishIO(clientToUpstream, true, count, len(buffer), err)
	return count, err
}

func (b trackedWebsocketBody) Close() error {
	b.relay.shutdown()
	return nil
}
