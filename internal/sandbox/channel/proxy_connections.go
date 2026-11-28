package channel

import (
	"net"
	"sync"
)

// proxyConnections owns accepted connections and joins their shutdown before
// the proxy freezes its observation.
type proxyConnections struct {
	net.Listener

	mu       sync.Mutex
	stopping bool
	active   map[*proxyConnection]struct{}
	workers  sync.WaitGroup
}

type proxyConnection struct {
	net.Conn

	owner   *proxyConnections
	relayMu sync.RWMutex
	relay   *websocketRelay
	once    sync.Once
}

func (l *proxyConnections) Accept() (net.Conn, error) {
	connection, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stopping {
		_ = connection.Close()
		return nil, net.ErrClosed
	}
	//exhaustruct:ignore // the connection's completion guard starts unused
	tracked := &proxyConnection{Conn: connection, owner: l}
	if l.active == nil {
		l.active = make(map[*proxyConnection]struct{})
	}
	l.active[tracked] = struct{}{}
	l.workers.Add(1)
	return tracked, nil
}

func (l *proxyConnections) abort() {
	l.mu.Lock()
	l.stopping = true
	connections := make([]*proxyConnection, 0, len(l.active))
	for connection := range l.active {
		connections = append(connections, connection)
	}
	listener := l.Listener
	l.mu.Unlock()
	_ = listener.Close()
	for _, connection := range connections {
		connection.abort()
	}
}

func (l *proxyConnections) wait() { l.workers.Wait() }

func (c *proxyConnection) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		if relay := c.websocketRelay(); relay != nil {
			relay.shutdown()
		}
		c.owner.mu.Lock()
		delete(c.owner.active, c)
		c.owner.mu.Unlock()
		c.owner.workers.Done()
	})
	return err
}

func (c *proxyConnection) Read(buffer []byte) (int, error) {
	relay := c.websocketRelay()
	if relay != nil && !relay.beginIO() {
		return 0, net.ErrClosed
	}
	count, err := c.Conn.Read(buffer)
	if relay != nil {
		relay.finishIO(clientToUpstream, false, count, len(buffer), err)
	}
	return count, err
}

func (c *proxyConnection) Write(buffer []byte) (int, error) {
	relay := c.websocketRelay()
	if relay != nil && !relay.beginIO() {
		return 0, net.ErrClosed
	}
	count, err := c.Conn.Write(buffer)
	if relay != nil {
		relay.finishIO(upstreamToClient, true, count, len(buffer), err)
	}
	return count, err
}

func (c *proxyConnection) setWebsocketRelay(relay *websocketRelay) {
	c.relayMu.Lock()
	c.relay = relay
	c.relayMu.Unlock()
}

func (c *proxyConnection) websocketRelay() *websocketRelay {
	c.relayMu.RLock()
	defer c.relayMu.RUnlock()
	return c.relay
}

func (c *proxyConnection) abort() {
	if relay := c.websocketRelay(); relay != nil {
		relay.shutdown()
		return
	}
	_ = c.Conn.Close()
}

func (l *proxyConnections) attachWebsocket(remoteAddress string, relay *websocketRelay) bool {
	if remoteAddress == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stopping {
		return false
	}
	for connection := range l.active {
		if connection.RemoteAddr().String() != remoteAddress {
			continue
		}
		connection.setWebsocketRelay(relay)
		relay.setClient(connection.Conn)
		return true
	}
	return false
}
