package channel

import (
	"errors"
	"net"
	"testing"
)

func TestProxyConnectionsAbortJoinsTheWorkerClose(t *testing.T) {
	//testenv:allow a fresh loopback socket exercises accepted connection teardown without external services
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	connections := &proxyConnections{Listener: listener}
	//testenv:allow the test dials its own freshly bound loopback listener
	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	worker, err := connections.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	connections.abort()
	if _, err := worker.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Errorf("Read after abort = %v, want the worker socket closed", err)
	}
	if len(connections.active) != 1 {
		t.Fatalf("abort removed the worker before it completed: %+v", connections.active)
	}
	_ = worker.Close()
	_ = worker.Close()
	connections.wait()
	if len(connections.active) != 0 {
		t.Error("closed worker is still registered")
	}
	if _, err := connections.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Errorf("Accept after abort = %v, want the listener closed", err)
	}
}

type acceptedConnectionListener struct {
	net.Listener

	connection net.Conn
}

func (l acceptedConnectionListener) Accept() (net.Conn, error) { return l.connection, nil }

func TestProxyConnectionsRejectsAnAcceptRacingWithAbort(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	connections := &proxyConnections{
		Listener: acceptedConnectionListener{connection: server}, stopping: true,
	}
	if connection, err := connections.Accept(); connection != nil || !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept = %v, %v, want a rejected connection", connection, err)
	}
	if _, err := client.Read(make([]byte, 1)); err == nil {
		t.Fatal("the rejected socket is still open")
	}
	connections.wait()
}

func assertProxyConnectionsRejectsUntrackableWebsocketConnections(t *testing.T) {
	t.Helper()
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	connections := &proxyConnections{active: make(map[*proxyConnection]struct{})}
	tracked := &proxyConnection{Conn: server, owner: connections}
	connections.active[tracked] = struct{}{}

	if connections.attachWebsocket("", nil) {
		t.Fatal("attachWebsocket accepted an empty remote address")
	}
	connections.stopping = true
	if connections.attachWebsocket(server.RemoteAddr().String(), nil) {
		t.Fatal("attachWebsocket accepted a connection after shutdown began")
	}
	connections.stopping = false
	if connections.attachWebsocket("different-remote", nil) {
		t.Fatal("attachWebsocket matched an unrelated remote address")
	}
}

func assertProxyConnectionRejectsIOAfterWebsocketRelayShutdown(t *testing.T) {
	t.Helper()
	client, peer := net.Pipe()
	defer peer.Close()

	relay := newWebsocketRelay(7, &proxyLog{})
	relay.setClient(client)
	connection := &proxyConnection{Conn: client}
	connection.setWebsocketRelay(relay)
	relay.shutdown()

	if _, err := connection.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Errorf("Read after relay shutdown = %v, want net.ErrClosed", err)
	}
	if _, err := connection.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Errorf("Write after relay shutdown = %v, want net.ErrClosed", err)
	}
}

func TestProxyConnectionsRejectsUntrackableWebsocketConnections(t *testing.T) {
	assertProxyConnectionsRejectsUntrackableWebsocketConnections(t)
}

func TestProxyConnectionRejectsIOAfterWebsocketRelayShutdown(t *testing.T) {
	assertProxyConnectionRejectsIOAfterWebsocketRelayShutdown(t)
}
