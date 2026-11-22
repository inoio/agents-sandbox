package channel

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/elazarl/goproxy"
)

// Crypto and CA-file IO seams. Generating an EC key fails when the entropy
// source does not, and the certificate and file operations fail on situations
// (an unsupported template, a full disk) a test's filesystem cannot produce, so
// tests substitute them to exercise the error handling.
var (
	generateECKey     = ecdsa.GenerateKey
	randomSerial      = rand.Int
	createCertificate = x509.CreateCertificate
	parseCertificate  = x509.ParseCertificate
	writeCATempFile   = (*os.File).WriteString
	closeCATempFile   = (*os.File).Close
)

// credentialHeaderNames are the request headers that can carry a credential, by
// lower-case name. Only their names are ever recorded; their values never leave
// the transport (hub invariant 3).
var credentialHeaderNames = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"x-api-key":           true,
	"api-key":             true,
	"cookie":              true,
}

// Handle is a running observation proxy. Addr is the host:port a sandbox's
// proxy setting points at. The CA behind CAPEM and CAFile is the upstream CA the
// runtime must trust under the settled T2 topology.
type Handle struct {
	Addr   string
	caPEM  string
	caFile string
	state  *proxyState
}

// proxyState is a running proxy's mutable state, held behind a pointer so a
// Handle stays copyable: the exported methods use value receivers.
type proxyState struct {
	server   *http.Server
	recorder *recorder
	caFile   string

	log      *proxyLog
	logPath  string
	logLines int

	mu sync.Mutex
}

// Start binds an observation proxy on a host-loopback port with a freshly
// generated CA and returns a handle. The proxy terminates TLS for every CONNECT
// and records each request's destination and credential-bearing header names.
// Stop tears the proxy down and returns the scrubbed artefact.
//
// An empty bindHost means loopback (127.0.0.1): the channel is never bound to
// every interface, so an unset or blank value cannot expose the proxy on the
// network.
//
// skipUpstreamVerify disables TLS certificate verification on the proxy's dial
// to the real upstream. It is false in the normal case, so a credential the
// proxy forwards cannot reach an impostor; it is the
// network.observe.skip-upstream-verify escape hatch for a host whose egress is
// transparently intercepted with a private CA the system roots do not include.
//
// openLog opens the session's proxy log; it is called on the first warning
// only, so a clean session creates no file. A nil openLog only counts.
func Start(bindHost string, skipUpstreamVerify bool, openLog func() (*os.File, error)) (Handle, error) {
	if bindHost == "" {
		bindHost = "127.0.0.1"
	}
	ca, caPEM, err := generateCA()
	if err != nil {
		return Handle{}, err
	}
	caFile, err := writeCAFile(caPEM)
	if err != nil {
		return Handle{}, err
	}

	listener, err := new(net.ListenConfig).Listen(context.Background(), "tcp", net.JoinHostPort(bindHost, "0"))
	if err != nil {
		_ = os.Remove(caFile)
		return Handle{}, err
	}

	state := new(proxyState)
	state.recorder = new(recorder)
	state.caFile = caFile
	state.log = &proxyLog{
		// gomutants:disable-next-line INTEGER_INCREMENT,INTEGER_DECREMENT reason="the line count starts at zero; a nonzero start only shifts a count no test of a real session can observe before a warning"
		mu: sync.Mutex{}, file: nil, openLog: openLog, lines: 0, closed: false,
		onHandshakeFailure: state.failTunnel,
	}
	state.server = &http.Server{
		Handler: terminateProxy(ca, state, skipUpstreamVerify),
		// A defensive ceiling on reading a request's headers; the value is not
		// a contract and a local test server answers before it can fire.
		// gomutants:disable-next-line INTEGER_INCREMENT,INTEGER_DECREMENT,ARITHMETIC_BASE reason="a defensive ReadHeaderTimeout cannot be observed by a local test server and the value is not a contract"
		ReadHeaderTimeout: 30 * time.Second,
	}
	go func() {
		_ = state.server.Serve(listener)
	}()

	return Handle{
		Addr:   listener.Addr().String(),
		caPEM:  caPEM,
		caFile: caFile,
		state:  state,
	}, nil
}

// terminateProxy builds the goproxy server: it terminates TLS with our CA,
// records every request's destination and credential-bearing header names,
// records success on a response, and records a denial when this proxy's own
// dial to the upstream fails. Under T2 the runtime enforces the guest's egress
// before our proxy is consulted, so a policy-denied destination never reaches
// the request hook at all; the denial this records is our proxy's dial error,
// not a sandbox-policy denial.
func terminateProxy(ca tls.Certificate, state *proxyState, skipUpstreamVerify bool) *goproxy.ProxyHttpServer {
	proxy := goproxy.NewProxyHttpServer()
	// goproxy's own logger warns unconditionally; route it to the session log
	// so it cannot interleave with the agent's TUI during a learning session.
	proxy.Logger = state.logger()
	// The observation proxy is the sandbox's egress, so it dials upstream
	// directly instead of honouring a host proxy environment variable.
	proxy.Tr.Proxy = nil
	proxy.ConnectDial = nil
	// goproxy's origin transport verifies upstream certificates by default;
	// keep that unless the operator opted out for a host whose egress is
	// transparently intercepted. Verification is what stops a forwarded
	// credential reaching an impostor.
	if skipUpstreamVerify {
		//nolint:gosec,exhaustruct_v5 // deliberate opt-out: only InsecureSkipVerify describes the intent, and the rest of the TLS config keeps the library defaults
		proxy.Tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	mitm := &goproxy.ConnectAction{
		Action:    goproxy.ConnectMitm,
		Hijack:    nil,
		TLSConfig: mitmTLSConfig(ca, state.nameTunnel),
	}
	proxy.OnRequest().HandleConnect(goproxy.FuncHttpsHandler(
		func(host string, _ *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
			state.observeConnect(host)
			return mitm, host
		},
	))
	proxy.OnRequest().DoFunc(state.onRequest)
	proxy.OnResponse().DoFunc(state.onResponse)
	return proxy
}

// observeConnect records a CONNECT target as a value-free "connect" destination
// and marks it successful, because the proxy accepts the tunnel. A client that
// pins certificates or speaks a non-HTTP protocol never produces an inner
// request, but the tunnel it opened is still visible. It carries no attribution:
// the request inside the tunnel is never seen (nil header names). A host that
// also terminates appears twice — once here as "connect" and once as its own
// "https" destination with attribution.
func (s *proxyState) observeConnect(host string) {
	hostname, port := splitHostPort(host)
	ip := ""
	if parsed := net.ParseIP(hostname); parsed != nil {
		// The canonical form, so nameTunnel matches the same address however
		// the client spelled it.
		ip = parsed.String()
		hostname = ip
	}

	s.mu.Lock()
	destination := s.recorder.observe(hostname, ip, "", ConnectProtocol, port, nil)
	destination.Success = true
	s.mu.Unlock()
}

// nameTunnel records the SNI hostname a client sent through a tunnel it opened
// by IP, so the tunnel is attributed to the host rather than the address. A
// tunnel opened by hostname, or a handshake without SNI, needs no naming.
func (s *proxyState) nameTunnel(connectTarget, serverName string) {
	hostname, port := splitHostPort(connectTarget)
	parsed := net.ParseIP(hostname)
	if serverName == "" || parsed == nil || serverName == parsed.String() {
		return
	}
	hostname = parsed.String()
	s.mu.Lock()
	s.recorder.nameTunnel(hostname, port, serverName)
	s.mu.Unlock()
}

// splitHostPort returns a CONNECT target's host and port, leaving the port zero
// when the target carries none.
func splitHostPort(host string) (string, uint16) {
	hostname, rawPort, err := net.SplitHostPort(host)
	if err != nil {
		return host, 0
	}
	port, err := strconv.ParseUint(rawPort, 10, 16)
	if err != nil {
		// ParseUint returns a clamped maximum on a range error, so the port
		// must be discarded rather than used.
		return hostname, 0
	}
	return hostname, uint16(port)
}

// onRequest records one request's destination and credential-bearing header
// names, and installs a round-trip hook that records a denial when this proxy's
// own dial to the upstream fails. The request is returned unchanged.
func (s *proxyState) onRequest(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	host := req.URL.Hostname()
	path := req.URL.Path
	protocol := req.URL.Scheme
	if protocol == "" {
		protocol = "http"
	}
	ip := ""
	if parsed := net.ParseIP(host); parsed != nil {
		ip = parsed.String()
	}

	port := requestPort(req.URL)
	s.mu.Lock()
	destination := s.recorder.observe(host, ip, path, protocol, port, observedCredentialHeaders(req.Header))
	s.mu.Unlock()

	ctx.UserData = destination
	ctx.RoundTripper = goproxy.RoundTripperFunc(
		func(roundTripReq *http.Request, roundTripCtx *goproxy.ProxyCtx) (*http.Response, error) {
			response, err := roundTripCtx.Proxy.Tr.RoundTrip(roundTripReq)
			if err != nil {
				s.mu.Lock()
				s.recorder.deny(host, ip, path, protocol, port)
				s.mu.Unlock()
			}
			return response, err
		},
	)
	return req, nil
}

// onResponse marks the destination of a completed request successful. A nil
// response — a round-trip error already recorded as a denial — is returned
// unchanged.
func (s *proxyState) onResponse(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
	if resp == nil {
		return resp
	}
	if destination, ok := ctx.UserData.(*Destination); ok {
		s.mu.Lock()
		destination.Success = true
		s.mu.Unlock()
	}
	return resp
}

// Stop tears the proxy down, removes its CA file, and returns the scrubbed
// artefact. It is idempotent: a second call returns the same artefact.
func (h Handle) Stop() Observation {
	if h.state == nil {
		// gomutants:disable-next-line RETURN_ZERO reason="an empty Observation is the zero value, so zeroing the literal is the identical value"
		return Observation{Destinations: nil, Attribution: nil, ProxyLog: "", ProxyWarnings: 0}
	}
	return h.state.stop()
}

// CAPEM returns the PEM-encoded CA certificate the runtime must trust as an
// upstream CA (R1). It is empty only on a zero Handle.
func (h Handle) CAPEM() string { return h.caPEM }

// CAFile returns the path to the 0600 host file holding the CA PEM, because the
// runtime's TLSConfig.UpstreamCACerts takes paths, not PEM contents (R1). Stop
// removes the file.
func (h Handle) CAFile() string { return h.caFile }

// stop closes the server, removes the CA file, closes the session log, and
// returns the artefact naming the log when it holds anything. It is idempotent.
func (s *proxyState) stop() Observation {
	// Close is idempotent, so a second Stop needs no flag to stay a no-op.
	_ = s.server.Close()
	// The log closes before the recorder is read, so a failure logged after
	// Stop reaches neither; one logged just before can still miss the
	// artefact, which is harmless once the session is over.
	if s.log != nil {
		s.logPath, s.logLines = s.log.close()
	}
	s.mu.Lock()
	// gomutants:disable-next-line INTEGER_INCREMENT,INTEGER_DECREMENT reason="ProxyWarnings is overwritten from the log below; the zero literal is exhaustruct's"
	observation := s.recorder.result(Observation{Destinations: nil, Attribution: nil, ProxyLog: "", ProxyWarnings: 0})
	s.mu.Unlock()

	_ = os.Remove(s.caFile)
	observation.ProxyLog, observation.ProxyWarnings = s.logPath, s.logLines
	return observation
}

// logger returns the state's session log, or a counting-only one for a state
// built without Start.
func (s *proxyState) logger() *proxyLog {
	if s.log == nil {
		s.log = &proxyLog{
			// gomutants:disable-next-line INTEGER_INCREMENT,INTEGER_DECREMENT reason="a counting-only log built for a state without Start; its start count is read by no caller"
			mu: sync.Mutex{}, file: nil, openLog: nil, lines: 0, closed: false,
			onHandshakeFailure: s.failTunnel,
		}
	}
	return s.log
}

// failTunnel records a handshake the client aborted on the tunnel it belongs to.
func (s *proxyState) failTunnel(connectTarget string) {
	s.mu.Lock()
	s.recorder.failTunnel(connectTarget)
	s.mu.Unlock()
}

// generateCA creates a fresh self-signed CA for one proxy, returned parsed (for
// goproxy's per-host leaf signing) and PEM-encoded (for the runtime to trust).
//
//nolint:exhaustruct_v5 // a throwaway session CA sets only the fields it needs; the rest stay zero
func generateCA() (tls.Certificate, string, error) {
	key, err := generateECKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(now.UnixNano()),
		Subject:               pkix.Name{CommonName: "agents-sandbox observation CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := createCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	leaf, err := parseCertificate(der)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, string(pemBytes), nil
}

// writeCAFile writes the CA PEM to a per-handle 0600 host file and returns its
// path.
func writeCAFile(caPEM string) (string, error) {
	file, err := os.CreateTemp("", "agents-sandbox-channel-ca-*.pem")
	if err != nil {
		return "", err
	}
	if _, err := writeCATempFile(file, caPEM); err != nil {
		_ = closeCATempFile(file)
		_ = os.Remove(file.Name())
		return "", err
	}
	if err := closeCATempFile(file); err != nil {
		_ = os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}

// observedCredentialHeaders returns the lower-case names of the request headers
// that can carry a credential. Only the names are read; no value is touched.
func observedCredentialHeaders(header http.Header) []string {
	var names []string
	for name := range header {
		lower := strings.ToLower(name)
		if credentialHeaderNames[lower] {
			names = append(names, lower)
		}
	}
	return names
}

// requestPort returns the port the request used, defaulting by scheme when the
// URL carries none.
func requestPort(u *url.URL) uint16 {
	if raw := u.Port(); raw != "" {
		if port, err := strconv.ParseUint(raw, 10, 16); err == nil {
			return uint16(port)
		}
	}
	if u.Scheme == "https" {
		return 443
	}
	return 80
}
