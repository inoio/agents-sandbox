package docker

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moby/moby/client"
)

// TestRealClientDialHijackConstructionFailure pins the shared-client construction
// failure branch the DialHijack method guards on, via the ensureMobyClient seam.
func TestRealClientDialHijackConstructionFailure(t *testing.T) {
	wantErr := errors.New("no docker")
	orig := ensureMobyClient
	ensureMobyClient = func() error { return wantErr }
	t.Cleanup(func() { ensureMobyClient = orig })

	_, err := (realDockerClient{}).DialHijack(context.Background(), "/grpc", "h2c", nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("DialHijack error = %v, want %v", err, wantErr)
	}
}

// TestRealClientImageListClientError pins the shared-client construction failure
// branch every real-client method guards on: when the moby client cannot be
// created, ImageList surfaces the error instead of dereferencing a nil client.
// The ensureMobyClient seam lets the branch be reached without a daemon.
func TestRealClientImageListClientError(t *testing.T) {
	wantErr := errors.New("no docker")
	orig := ensureMobyClient
	ensureMobyClient = func() error { return wantErr }
	t.Cleanup(func() { ensureMobyClient = orig })

	_, err := (realDockerClient{}).ImageList(context.Background(), client.ImageListOptions{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ImageList error = %v, want %v", err, wantErr)
	}
}

// TestRealClientImageListSuccess pins the delegation line: with a usable client,
// ImageList returns the daemon's response. A stub HTTP server stands in for the
// daemon so the success path is covered without a real Docker install.
func TestRealClientImageListSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	t.Cleanup(server.Close)

	mobyClientForTest, err := client.New(client.WithHost(server.URL))
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	origEnsure := ensureMobyClient
	origClient := mobyClient
	ensureMobyClient = func() error {
		mobyClient = mobyClientForTest
		return nil
	}
	t.Cleanup(func() {
		ensureMobyClient = origEnsure
		mobyClient = origClient
	})

	result, err := (realDockerClient{}).ImageList(context.Background(), client.ImageListOptions{})
	if err != nil {
		t.Fatalf("ImageList: %v", err)
	}
	if len(result.Items) != 0 {
		t.Errorf("ImageList items = %v, want none", result.Items)
	}
}

// TestRealClientDialHijackSuccess pins the delegation line: with a usable
// client, DialHijack returns the negotiated connection. A stub HTTP server
// upgrades the connection in place of a real daemon.
func TestRealClientDialHijackSuccess(t *testing.T) {
	var serverConn net.Conn
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijack unsupported", http.StatusInternalServerError)
			return
		}
		conn, buf, err := hijacker.Hijack()
		if err != nil {
			return
		}
		serverConn = conn
		_, _ = buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: h2c\r\nConnection: Upgrade\r\n\r\n")
		_ = buf.Flush()
	}))
	t.Cleanup(func() {
		if serverConn != nil {
			_ = serverConn.Close()
		}
		server.Close()
	})

	mobyClientForTest, err := client.New(client.WithHost("tcp://" + strings.TrimPrefix(server.URL, "http://")))
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	origEnsure := ensureMobyClient
	origClient := mobyClient
	ensureMobyClient = func() error {
		mobyClient = mobyClientForTest
		return nil
	}
	t.Cleanup(func() {
		ensureMobyClient = origEnsure
		mobyClient = origClient
	})

	conn, err := (realDockerClient{}).DialHijack(context.Background(), "/grpc", "h2c", nil)
	if err != nil {
		t.Fatalf("DialHijack: %v", err)
	}
	_ = conn.Close()
}
