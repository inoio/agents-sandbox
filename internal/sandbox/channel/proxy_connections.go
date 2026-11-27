package channel

import (
	"net"
	"sync"
)

// proxyConnections owns accepted connections past HTTP hijacking. The proxy's
// MITM worker closes its connection after recording its final warning, so abort
// closes the socket directly and wait joins the worker's subsequent Close.
type proxyConnections struct {
	net.Listener

	mu       sync.Mutex
	stopping bool
	active   map[*proxyConnection]struct{}
	workers  sync.WaitGroup
}

type proxyConnection struct {
	net.Conn

	owner *proxyConnections
	once  sync.Once
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
	defer l.mu.Unlock()
	l.stopping = true
	_ = l.Listener.Close()
	for connection := range l.active {
		_ = connection.Conn.Close()
	}
}

func (l *proxyConnections) wait() { l.workers.Wait() }

func (c *proxyConnection) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		c.owner.mu.Lock()
		delete(c.owner.active, c)
		c.owner.mu.Unlock()
		c.owner.workers.Done()
	})
	return err
}
