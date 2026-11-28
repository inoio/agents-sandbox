package channel

import (
	"bufio"
	"net"
	"net/http"
)

// proxyHandler closes direct HTTP upgrades when goproxy's synchronous relay
// returns. CONNECT tunnels instead belong to its asynchronous MITM worker,
// which closes the connection after recording the final handshake warning.
type proxyHandler struct{ http.Handler }

func (h proxyHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodConnect {
		h.Handler.ServeHTTP(writer, request)
		return
	}
	tracked := &upgradeResponseWriter{ResponseWriter: writer, connection: nil}
	defer tracked.closeUpgrade()
	h.Handler.ServeHTTP(tracked, request)
}

type upgradeResponseWriter struct {
	http.ResponseWriter

	connection net.Conn
}

func (w *upgradeResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	connection, buffered, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.connection = connection
	}
	return connection, buffered, err
}

func (w *upgradeResponseWriter) Flush() { _ = http.NewResponseController(w.ResponseWriter).Flush() }

func (w *upgradeResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *upgradeResponseWriter) closeUpgrade() {
	if w.connection != nil {
		_ = w.connection.Close()
	}
}
