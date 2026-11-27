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
