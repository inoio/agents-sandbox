package channel

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
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
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elazarl/goproxy"
)

// errChannelSeam is the failure a substituted seam returns, so an error branch
// is exercised without a real IO or crypto failure.
var errChannelSeam = errors.New("channel seam failure")

// withSwap replaces a package-level seam for one test and restores it with
// t.Cleanup, matching the seam-restoration contract (no t.Parallel).
func withSwap[T any](t *testing.T, seam *T, replacement T) {
	t.Helper()

	original := *seam
	*seam = replacement
	t.Cleanup(func() { *seam = original })
}

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
	// The upstream is httptest's self-signed TLS server; skip upstream
	// verification so the test exercises observation, not certificate trust.
	handle, err := Start("127.0.0.1", true)
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
	// A plain-HTTP upstream keeps the test on the default (verifying) origin
	// transport: streaming is transport-agnostic, and httptest's TLS server is
	// self-signed, which the default transport now rejects.
	handle, err := Start("127.0.0.1", false)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer handle.Stop()

	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

// TestProxyTerminatesForTheSNIHostnameOfAnIPConnect mirrors the msb runtime
// under T2: it opens the tunnel by IP (CONNECT 127.0.0.1:<port>) but verifies the
// proxy's certificate against the hostname it sends as SNI. The leaf must be
// issued for that hostname, or the runtime rejects the handshake and the
// agent's request never leaves the VM.
func TestProxyTerminatesForTheSNIHostnameOfAnIPConnect(t *testing.T) {
	handle, err := Start("127.0.0.1", true)
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
	const sniHostname = "localhost"

	//testenv:allow dials the test's own loopback proxy, which Start bound a moment ago
	conn, err := net.Dial("tcp", handle.Addr)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	connectTarget := upstreamURL.Host // 127.0.0.1:<port>, an IP like the runtime sends
	if _, err := io.WriteString(
		conn,
		"CONNECT "+connectTarget+" HTTP/1.1\r\nHost: "+connectTarget+"\r\n\r\n",
	); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	reader := bufio.NewReader(conn)
	connectResponse, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("read CONNECT response: %v", err)
	}
	if connectResponse.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT status = %d, want 200", connectResponse.StatusCode)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(handle.CAPEM())) {
		t.Fatal("the proxy CA did not parse")
	}
	tlsConn := tls.Client(conn, &tls.Config{ServerName: sniHostname, RootCAs: pool, MinVersion: tls.VersionTLS12})
	if err := tlsConn.Handshake(); err != nil {
		t.Fatalf("handshake with SNI %q over an IP CONNECT: %v", sniHostname, err)
	}

	request := "GET /v1/messages HTTP/1.1\r\nHost: " + sniHostname + ":" + upstreamURL.Port() + "\r\nConnection: close\r\n\r\n"
	if _, err := io.WriteString(tlsConn, request); err != nil {
		t.Fatalf("write request: %v", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(tlsConn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}

	destination := destinationFor(t, handle.Stop().Destinations, "https")
	if destination.Host != sniHostname {
		t.Errorf("https destination host = %q, want the SNI hostname %q", destination.Host, sniHostname)
	}
}

func TestProxyRecordsDeniedDestination(t *testing.T) {
	handle, err := Start("127.0.0.1", false)
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

// The origin transport verifies the upstream certificate by default, so an
// upstream whose certificate the proxy's roots do not contain (httptest's
// self-signed one) is rejected and the request is recorded as a denial — a
// forwarded credential can never reach an impostor.
func TestProxyVerifiesUpstreamCertificateByDefault(t *testing.T) {
	handle, err := Start("127.0.0.1", false)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer handle.Stop()

	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	resp, err := proxyClient(t, handle).Get(upstream.URL)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatal("an untrusted upstream certificate was accepted, want a failed request")
		}
	}

	observation := handle.Stop()
	destination := destinationFor(t, observation.Destinations, "https")
	if !destination.Denied {
		t.Errorf("denied = false, want true for an untrusted upstream certificate")
	}
	if destination.Success {
		t.Errorf("success = true, want false for an untrusted upstream certificate")
	}
}

// skip-upstream-verify lets the same self-signed upstream round-trip, proving
// the opt-out reaches the origin dial rather than only the constructor.
func TestProxySkipsUpstreamVerificationWhenConfigured(t *testing.T) {
	handle, err := Start("127.0.0.1", true)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer handle.Stop()

	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	resp, err := proxyClient(t, handle).Get(upstream.URL)
	if err != nil {
		t.Fatalf("request through proxy with verification skipped: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestProxyRecordsConnectTunnelWhenMitmHandshakeFails(t *testing.T) {
	handle, err := Start("127.0.0.1", false)
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
	handle, err := Start("127.0.0.1", false)
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
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse CA certificate: %v", err)
	}
	if !certificate.IsCA || !certificate.BasicConstraintsValid {
		t.Errorf("CA certificate is not a valid CA: isCA=%v basicConstraintsValid=%v",
			certificate.IsCA, certificate.BasicConstraintsValid)
	}
	if license := certificate.KeyUsage&x509.KeyUsageCertSign != 0; !license {
		t.Error("CA certificate lacks KeyUsageCertSign")
	}
	if revoke := certificate.KeyUsage&x509.KeyUsageCRLSign != 0; !revoke {
		t.Error("CA certificate lacks KeyUsageCRLSign")
	}
	if window := certificate.NotAfter.Sub(certificate.NotBefore); window != 25*time.Hour {
		t.Errorf("CA validity window = %v, want 25h (one hour before to 24 hours after now)", window)
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
	handle, err := Start("", false)
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

// A CONNECT target without a port, or with a non-numeric one, leaves the port
// zero rather than failing the observation: the tunnel is still visible.
func TestSplitHostPortHandlesPortlessAndBadPortTargets(t *testing.T) {
	cases := []struct {
		target string
		host   string
		port   uint16
	}{
		{"api.example:443", "api.example", 443},
		{"api.example", "api.example", 0},
		{"api.example:notaport", "api.example", 0},
		{"api.example:0", "api.example", 0},
		{"api.example:65535", "api.example", 65535},
		{"api.example:65536", "api.example", 0},
		{"api.example:70000", "api.example", 0},
	}
	for _, tc := range cases {
		host, port := splitHostPort(tc.target)
		if host != tc.host || port != tc.port {
			t.Errorf("splitHostPort(%q) = (%q, %d), want (%q, %d)", tc.target, host, port, tc.host, tc.port)
		}
	}
}

// requestPort uses the URL's port, falls back to the scheme's default, and
// treats an out-of-range port as absent so it falls back too.
func TestRequestPortDefaultsByScheme(t *testing.T) {
	cases := []struct {
		name string
		url  *url.URL
		want uint16
	}{
		{"explicit port", &url.URL{Scheme: "https", Host: "api.example:8443"}, 8443},
		{"https default", &url.URL{Scheme: "https", Host: "api.example"}, 443},
		{"http default", &url.URL{Scheme: "http", Host: "api.example"}, 80},
		{"out-of-range port", &url.URL{Scheme: "https", Host: "api.example:99999"}, 443},
	}
	for _, tc := range cases {
		if got := requestPort(tc.url); got != tc.want {
			t.Errorf("%s: requestPort = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// A request URL with no scheme is recorded as "http" rather than an empty
// protocol, so a plain-HTTP request keeps a protocol the analyser can read.
func TestOnRequestDefaultsEmptySchemeToHTTP(t *testing.T) {
	state := &proxyState{recorder: new(recorder)}
	req := &http.Request{URL: &url.URL{Host: "api.example", Path: "/v1/chat"}, Header: http.Header{}}

	returned, response := state.onRequest(req, &goproxy.ProxyCtx{})
	if returned != req || response != nil {
		t.Fatalf("onRequest = (%v, %v), want the request unchanged and no response", returned, response)
	}
	destinations := state.recorder.result(Observation{}).Destinations
	if len(destinations) != 1 || destinations[0].Protocol != "http" {
		t.Fatalf("destinations = %+v, want one http entry", destinations)
	}
}

// A nil response is returned unchanged: a round-trip failure is already recorded
// as a denial by the request hook, so there is nothing to mark successful.
func TestOnResponsePassesNilThrough(t *testing.T) {
	state := &proxyState{recorder: new(recorder)}

	if got := state.onResponse(nil, &goproxy.ProxyCtx{}); got != nil {
		t.Errorf("onResponse(nil) = %v, want nil", got)
	}
}

// A zero Handle has no proxy state, so Stop returns an empty artefact instead of
// dereferencing nil.
func TestHandleZeroValueStopIsEmpty(t *testing.T) {
	observation := Handle{}.Stop()
	if len(observation.Destinations) != 0 || len(observation.Attribution) != 0 {
		t.Errorf("zero Handle Stop = %+v, want an empty artefact", observation)
	}
}

// discardLogger swallows goproxy's output; calling it must not panic or write.
func TestDiscardLoggerSwallowsOutput(t *testing.T) {
	discardLogger{}.Printf("goproxy warning %d", 1)
}

// Start propagates a CA-generation failure (the key generator cannot produce a
// key), so a broken entropy source surfaces to the caller rather than yielding a
// proxy with no CA.
func TestStartFailsWhenCAKeyGenerationFails(t *testing.T) {
	withSwap(t, &generateECKey, func(elliptic.Curve, io.Reader) (*ecdsa.PrivateKey, error) {
		return nil, errChannelSeam
	})

	if _, err := Start("127.0.0.1", false); err == nil {
		t.Error("Start with a failing key generator returned no error")
	}
}

// Start propagates a CA-file write failure: with TMPDIR pointing at a regular
// file, os.CreateTemp cannot create the CA file.
func TestStartFailsWhenCAFileCannotBeWritten(t *testing.T) {
	notADirectory := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(notADirectory, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("TMPDIR", notADirectory)

	if _, err := Start("127.0.0.1", false); err == nil {
		t.Error("Start with an unwritable CA temp dir returned no error")
	}
}

// Start propagates a bind failure: an unparseable bind host fails before any
// request can be observed, and the CA file it already wrote is removed.
func TestStartFailsWhenListenFails(t *testing.T) {
	caDir := t.TempDir()
	t.Setenv("TMPDIR", caDir)

	if _, err := Start("[::1]", false); err == nil {
		t.Error("Start with an unparseable bind host returned no error")
	}
	assertNoCAFiles(t, caDir)
}

// generateCA propagates a certificate-creation failure and an unexpected parse
// failure, so a caller never receives a half-built CA.
func TestGenerateCAPropagatesCryptoErrors(t *testing.T) {
	t.Run("create certificate", func(t *testing.T) {
		withSwap(
			t,
			&createCertificate,
			func(io.Reader, *x509.Certificate, *x509.Certificate, any, any) ([]byte, error) {
				return nil, errChannelSeam
			},
		)

		_, _, err := generateCA()
		if !errors.Is(err, errChannelSeam) {
			t.Errorf("generateCA with a failing certificate creator: err = %v, want %v", err, errChannelSeam)
		}
	})

	t.Run("parse certificate", func(t *testing.T) {
		withSwap(t, &parseCertificate, func([]byte) (*x509.Certificate, error) {
			return nil, errChannelSeam
		})

		if _, _, err := generateCA(); err == nil {
			t.Error("generateCA with a failing certificate parser returned no error")
		}
	})
}

// writeCAFile propagates a write and a close failure, removing the temp file it
// created before returning.
func TestWriteCAFilePropagatesWriteAndCloseErrors(t *testing.T) {
	t.Run("write", func(t *testing.T) {
		caDir := t.TempDir()
		t.Setenv("TMPDIR", caDir)
		withSwap(t, &writeCATempFile, func(*os.File, string) (int, error) {
			return 0, errChannelSeam
		})

		if _, err := writeCAFile("pem"); !errors.Is(err, errChannelSeam) {
			t.Errorf("writeCAFile with a failing write: err = %v, want %v", err, errChannelSeam)
		}
		assertNoCAFiles(t, caDir)
	})

	t.Run("close", func(t *testing.T) {
		caDir := t.TempDir()
		t.Setenv("TMPDIR", caDir)
		withSwap(t, &closeCATempFile, func(*os.File) error {
			return errChannelSeam
		})

		if _, err := writeCAFile("pem"); !errors.Is(err, errChannelSeam) {
			t.Errorf("writeCAFile with a failing close: err = %v, want %v", err, errChannelSeam)
		}
		assertNoCAFiles(t, caDir)
	})
}

// assertNoCAFiles fails when writeCAFile's temp CA file survives a failure, so a
// missing cleanup is caught rather than only the error.
func assertNoCAFiles(t *testing.T, dir string) {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(dir, "agents-sandbox-channel-ca-*"))
	if err != nil {
		t.Fatalf("glob CA temp files: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("leftover CA temp files = %v, want none", matches)
	}
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

// terminateProxy configures a quiet proxy that dials upstream directly: the
// discard logger keeps goproxy's output off the agent's TUI, and a nil
// transport proxy and ConnectDial stop it reaching upstream through a host
// proxy environment variable.
func TestTerminateProxyIsQuietAndDialsDirectly(t *testing.T) {
	ca, _, err := generateCA()
	if err != nil {
		t.Fatalf("generateCA: %v", err)
	}
	state := &proxyState{recorder: new(recorder)}

	proxy := terminateProxy(ca, state, false)
	if _, ok := proxy.Logger.(discardLogger); !ok {
		t.Errorf("proxy.Logger = %T, want the discard logger", proxy.Logger)
	}
	if proxy.Tr == nil || proxy.Tr.Proxy != nil {
		t.Error("the observation proxy must dial upstream directly, not via a host proxy env var")
	}
	if proxy.ConnectDial != nil {
		t.Error("ConnectDial must be nil so the proxy dials upstream directly")
	}
	if proxy.Tr.TLSClientConfig != nil && proxy.Tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("the origin transport must verify upstream certificates by default")
	}
}

// terminateProxy forwards skipUpstreamVerify to the origin transport's TLS
// config, so the network.observe.skip-upstream-verify opt-out actually reaches
// the dial that carries a forwarded credential.
func TestTerminateProxyForwardsSkipUpstreamVerify(t *testing.T) {
	ca, _, err := generateCA()
	if err != nil {
		t.Fatalf("generateCA: %v", err)
	}

	proxy := terminateProxy(ca, &proxyState{recorder: new(recorder)}, true)
	if proxy.Tr == nil || proxy.Tr.TLSClientConfig == nil || !proxy.Tr.TLSClientConfig.InsecureSkipVerify {
		t.Errorf("origin transport TLS config = %+v, want InsecureSkipVerify set", proxy.Tr.TLSClientConfig)
	}
}

// A CONNECT target that is a literal IP records that IP, so an analyser can name
// the destination a pinned or non-HTTP client reached.
func TestProxyConnectIPHostRecordsIP(t *testing.T) {
	state := &proxyState{recorder: new(recorder)}

	state.observeConnect("203.0.113.7:443")

	destinations := state.recorder.result(Observation{}).Destinations
	if len(destinations) != 1 {
		t.Fatalf("destinations = %+v, want one", destinations)
	}
	if destinations[0].Host != "203.0.113.7" {
		t.Errorf("host = %q, want 203.0.113.7", destinations[0].Host)
	}
	if destinations[0].IP != "203.0.113.7" {
		t.Errorf("ip = %q, want the literal target 203.0.113.7", destinations[0].IP)
	}
}

// Two CONNECT tunnels to one host on different ports are two destinations: the
// port is part of the identity, so neither entry's port is overwritten and each
// keeps its own count.
func TestProxyConnectTunnelsOnDifferentPortsStayDistinct(t *testing.T) {
	state := &proxyState{recorder: new(recorder)}

	state.observeConnect("api.example:443")
	state.observeConnect("api.example:8443")

	destinations := state.recorder.result(Observation{}).Destinations
	if len(destinations) != 2 {
		t.Fatalf("destinations = %+v, want two (one per port)", destinations)
	}
	if destinations[0].Port != 443 || destinations[1].Port != 8443 {
		t.Errorf("ports = %d, %d, want 443 then 8443", destinations[0].Port, destinations[1].Port)
	}
	for _, destination := range destinations {
		if destination.Count != 1 {
			t.Errorf("count = %d for %+v, want 1", destination.Count, destination)
		}
	}
}

// A nil response is a round-trip failure already recorded as a denial, so the
// response hook must not also mark its destination successful.
func TestOnResponseNilDoesNotMarkDestination(t *testing.T) {
	state := &proxyState{recorder: new(recorder)}
	destination := &Destination{}
	ctx := &goproxy.ProxyCtx{UserData: destination}

	if got := state.onResponse(nil, ctx); got != nil {
		t.Fatalf("onResponse(nil) = %v, want nil", got)
	}
	if destination.Success {
		t.Error("a nil response marked the destination successful")
	}
}

// Stop closes the proxy's listener, so a learning session's proxy does not keep
// accepting connections after teardown. Serve closes a listener it has not yet
// registered when it observes the shutdown, so allow a brief window for that
// goroutine to run before declaring the listener still open.
func TestStopClosesProxyListener(t *testing.T) {
	handle, err := Start("127.0.0.1", false)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	handle.Stop()

	deadline := time.Now().Add(time.Second)
	for {
		conn, err := net.DialTimeout(
			"tcp",
			handle.Addr,
			time.Second,
		) //testenv:allow the Stop test dials the just-closed loopback listener to prove it refuses
		if err != nil {
			return
		}
		conn.Close()
		if time.Now().After(deadline) {
			t.Fatalf("the proxy still accepts connections at %s after Stop", handle.Addr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
