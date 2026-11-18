package channel

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// proxyClient returns a client that routes HTTPS through the observation proxy
// and trusts its freshly generated CA, so the proxy terminates the connection.
func proxyClient(t *testing.T, handle Handle) *http.Client {
	t.Helper()

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(handle.CAPEM())) {
		t.Fatal("the proxy CA did not parse")
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(&url.URL{Scheme: "http", Host: handle.Addr}),
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
	}
}

func TestProxyRecordsDestinationAndCredentialHeaderName(t *testing.T) {
	handle, err := Start("127.0.0.1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer handle.Stop()

	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream URL: %v", err)
	}

	const secret = "channel-test-secret-value"
	req, err := http.NewRequest(http.MethodGet, upstream.URL+"/v1/chat", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+secret)

	resp, err := proxyClient(t, handle).Do(req)
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	observation := handle.Stop()
	if len(observation.Destinations) != 2 {
		t.Fatalf("destinations = %+v, want a connect and an https entry", observation.Destinations)
	}
	connectDestination := destinationFor(t, observation.Destinations, "connect")
	if !connectDestination.Success || connectDestination.Denied {
		t.Errorf("connect destination = %+v, want success and not denied", connectDestination)
	}
	if connectDestination.Path != "" {
		t.Errorf("connect path = %q, want empty", connectDestination.Path)
	}
	destination := destinationFor(t, observation.Destinations, "https")
	if destination.Host != upstreamURL.Hostname() {
		t.Errorf("host = %q, want %q", destination.Host, upstreamURL.Hostname())
	}
	if destination.IP != upstreamURL.Hostname() {
		t.Errorf("ip = %q, want %q", destination.IP, upstreamURL.Hostname())
	}
	wantPort, err := strconv.ParseUint(upstreamURL.Port(), 10, 16)
	if err != nil {
		t.Fatalf("upstream port %q: %v", upstreamURL.Port(), err)
	}
	if destination.Port != uint16(wantPort) {
		t.Errorf("port = %d, want %d", destination.Port, wantPort)
	}
	if destination.Protocol != "https" {
		t.Errorf("protocol = %q, want https", destination.Protocol)
	}
	if destination.Path != "/v1/chat" {
		t.Errorf("path = %q, want /v1/chat", destination.Path)
	}
	if !destination.Success {
		t.Errorf("success = false, want true")
	}
	if destination.Denied {
		t.Errorf("denied = true, want false")
	}
	if destination.Count != 1 {
		t.Errorf("count = %d, want 1", destination.Count)
	}
	if destination.Channel != observedViaProxy {
		t.Errorf("channel = %q, want %q", destination.Channel, observedViaProxy)
	}

	if len(observation.Attribution) != 1 {
		t.Fatalf("attribution = %+v, want exactly one", observation.Attribution)
	}
	attribution := observation.Attribution[0]
	if attribution.Host != destination.Host || attribution.Path != "/v1/chat" {
		t.Errorf("attribution = %+v, want host %q path /v1/chat", attribution, destination.Host)
	}
	if !reflect.DeepEqual(attribution.HeaderNames, []string{"authorization"}) {
		t.Errorf("header names = %v, want [authorization]", attribution.HeaderNames)
	}

	encoded, err := json.Marshal(observation)
	if err != nil {
		t.Fatalf("marshal observation: %v", err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("the artefact carries the header value: %s", encoded)
	}
}

func TestProxyStreamsSSEResponseIncrementally(t *testing.T) {
	handle, err := Start("127.0.0.1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer handle.Stop()

	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }

	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}
		io.WriteString(w, "data: first\n\n")
		flusher.Flush()
		// Block until the client has received the first event, so a buffering
		// proxy can never deliver it before the response completes.
		<-release
		io.WriteString(w, "data: second\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()
	defer unblock()

	resp, err := proxyClient(t, handle).Get(upstream.URL)
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	defer resp.Body.Close()

	firstEvent := make(chan string, 1)
	readErr := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(resp.Body).ReadString('\n')
		if err != nil {
			readErr <- err
			return
		}
		firstEvent <- line
	}()

	select {
	case line := <-firstEvent:
		if !strings.Contains(line, "data: first") {
			t.Fatalf("first event = %q, want a \"data: first\" line", line)
		}
		unblock()
	case err := <-readErr:
		t.Fatalf("read the first event: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the proxy buffered the streaming response: the first event never arrived")
	}
}

func TestProxyRecordsDeniedDestination(t *testing.T) {
	handle, err := Start("127.0.0.1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer handle.Stop()

	port := unusedLoopbackPort(t)
	req, err := http.NewRequest(http.MethodGet, "https://127.0.0.1:"+port+"/v1/chat", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := proxyClient(t, handle).Do(req)
	if err == nil {
		resp.Body.Close()
	}

	observation := handle.Stop()
	if len(observation.Destinations) != 2 {
		t.Fatalf("destinations = %+v, want a connect and an https entry", observation.Destinations)
	}
	destination := destinationFor(t, observation.Destinations, "https")
	if !destination.Denied {
		t.Errorf("denied = false, want true for an unreachable upstream")
	}
	if destination.Success {
		t.Errorf("success = true, want false for an unreachable upstream")
	}
	if destination.Host != "127.0.0.1" {
		t.Errorf("host = %q, want 127.0.0.1", destination.Host)
	}
	if destination.Path != "/v1/chat" {
		t.Errorf("path = %q, want /v1/chat", destination.Path)
	}
	if destination.Protocol != "https" {
		t.Errorf("protocol = %q, want https", destination.Protocol)
	}
}

func TestProxyRecordsConnectTunnelWhenMitmHandshakeFails(t *testing.T) {
	handle, err := Start("127.0.0.1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer handle.Stop()

	upstream := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer upstream.Close()
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream URL: %v", err)
	}

	// A client that does not trust the proxy CA: the CONNECT is accepted, but
	// the MITM handshake fails, so no inner request is ever observed.
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: handle.Addr}),
		},
	}
	if _, err := client.Get(upstream.URL); err == nil {
		t.Fatal("the untrusted proxy CA should have failed the handshake")
	}

	observation := handle.Stop()
	if len(observation.Destinations) != 1 {
		t.Fatalf("destinations = %+v, want one connect entry and no https entry", observation.Destinations)
	}
	destination := observation.Destinations[0]
	if destination.Protocol != "connect" {
		t.Errorf("protocol = %q, want connect", destination.Protocol)
	}
	if !destination.Success {
		t.Errorf("success = false, want true: the tunnel was accepted")
	}
	if destination.Denied {
		t.Errorf("denied = true, want false")
	}
	if destination.Host != upstreamURL.Hostname() {
		t.Errorf("host = %q, want %q", destination.Host, upstreamURL.Hostname())
	}
	if destination.Path != "" {
		t.Errorf("path = %q, want empty", destination.Path)
	}
	if len(observation.Attribution) != 0 {
		t.Errorf("attribution = %+v, want none for an un-terminated tunnel", observation.Attribution)
	}
}

func TestProxyConnectHostnameRecordsEmptyIP(t *testing.T) {
	state := &proxyState{recorder: new(recorder)}

	state.observeConnect("api.example:443")

	observation := state.recorder.result(Observation{})
	if len(observation.Destinations) != 1 {
		t.Fatalf("destinations = %+v, want one", observation.Destinations)
	}
	destination := observation.Destinations[0]
	if destination.Host != "api.example" {
		t.Errorf("host = %q, want api.example", destination.Host)
	}
	if destination.Port != 443 {
		t.Errorf("port = %d, want 443", destination.Port)
	}
	if destination.IP != "" {
		t.Errorf("ip = %q, want empty for a hostname target", destination.IP)
	}
	if destination.Protocol != "connect" {
		t.Errorf("protocol = %q, want connect", destination.Protocol)
	}
	if !destination.Success {
		t.Errorf("success = false, want true")
	}
}

func TestProxyHandleExposesCAAndStopsIdempotently(t *testing.T) {
	handle, err := Start("127.0.0.1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if handle.CAFile() == "" {
		t.Fatal("CAFile is empty")
	}
	info, err := os.Stat(handle.CAFile())
	if err != nil {
		t.Fatalf("stat CA file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("CA file mode = %o, want 600", info.Mode().Perm())
	}

	block, _ := pem.Decode([]byte(handle.CAPEM()))
	if block == nil {
		t.Fatal("CAPEM is not PEM")
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		t.Fatalf("parse CA certificate: %v", err)
	}

	caFile := handle.CAFile()
	first := handle.Stop()
	second := handle.Stop()
	if !reflect.DeepEqual(first, second) {
		t.Errorf("Stop is not idempotent: %+v vs %+v", first, second)
	}
	if _, err := os.Stat(caFile); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("CA file %s still present after Stop: %v", caFile, err)
	}
}

// An empty bindHost must mean loopback: the proxy is never bound to every
// interface, so a blank value cannot expose it on the network.
func TestStartEmptyBindHostIsLoopback(t *testing.T) {
	handle, err := Start("")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer handle.Stop()

	host, _, err := net.SplitHostPort(handle.Addr)
	if err != nil {
		t.Fatalf("split bound address %q: %v", handle.Addr, err)
	}
	if parsed := net.ParseIP(host); parsed == nil || !parsed.IsLoopback() {
		t.Errorf("empty bindHost bound %q, want a loopback address", handle.Addr)
	}
}

// destinationFor returns the recorded destination with the given protocol.
func destinationFor(t *testing.T, destinations []Destination, protocol string) Destination {
	t.Helper()

	for _, destination := range destinations {
		if destination.Protocol == protocol {
			return destination
		}
	}
	t.Fatalf("no %q destination in %+v", protocol, destinations)
	return Destination{}
}

// unusedLoopbackPort binds a loopback port and releases it, giving a port that
// an upstream dial will refuse.
func unusedLoopbackPort(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen(
		"tcp",
		"127.0.0.1:0",
	) //testenv:allow the deny test needs a loopback port that refuses connections
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return strconv.Itoa(port)
}
