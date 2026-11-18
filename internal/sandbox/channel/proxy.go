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

	mu      sync.Mutex
	stopped bool
}

// Start binds an observation proxy on a host-loopback port with a freshly
// generated CA and returns a handle. The proxy terminates TLS for every CONNECT
// and records each request's destination and credential-bearing header names.
// Stop tears the proxy down and returns the scrubbed artefact.
//
//deadcode:ignore reason="the channel's start entry point; S7 starts the observer before prepareSandbox"
func Start(bindHost string) (Handle, error) {
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
	state.server = &http.Server{
		Handler:           terminateProxy(ca, state),
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
// records success on a response, and records a denial when the upstream cannot
// be reached.
//
//deadcode:ignore reason="reached only from Start, whose invocation S7 wires"
func terminateProxy(ca tls.Certificate, state *proxyState) *goproxy.ProxyHttpServer {
	proxy := goproxy.NewProxyHttpServer()
	proxy.Verbose = false
	// The observation proxy is the sandbox's egress, so it dials upstream
	// directly instead of honouring a host proxy environment variable.
	proxy.Tr.Proxy = nil
	proxy.ConnectDial = nil

	mitm := &goproxy.ConnectAction{
		Action:    goproxy.ConnectMitm,
		Hijack:    nil,
		TLSConfig: goproxy.TLSConfigFromCA(&ca),
	}
	proxy.OnRequest().HandleConnect(goproxy.FuncHttpsHandler(
		func(host string, _ *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
			return mitm, host
		},
	))
	proxy.OnRequest().DoFunc(state.onRequest)
	proxy.OnResponse().DoFunc(state.onResponse)
	return proxy
}

// onRequest records one request's destination and credential-bearing header
// names, and installs a round-trip hook that records a denial when the upstream
// is unreachable. The request is returned unchanged.
//
//deadcode:ignore reason="the proxy's request hook; reached only from Start, whose invocation S7 wires"
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

	s.mu.Lock()
	destination := s.recorder.observe(host, ip, path, protocol, observedCredentialHeaders(req.Header))
	destination.Port = requestPort(req.URL)
	s.mu.Unlock()

	ctx.UserData = destination
	ctx.RoundTripper = goproxy.RoundTripperFunc(
		func(roundTripReq *http.Request, roundTripCtx *goproxy.ProxyCtx) (*http.Response, error) {
			response, err := roundTripCtx.Proxy.Tr.RoundTrip(roundTripReq)
			if err != nil {
				s.mu.Lock()
				s.recorder.deny(host, ip, path)
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
//
//deadcode:ignore reason="the proxy's response hook; reached only from Start, whose invocation S7 wires"
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
//
//deadcode:ignore reason="tears the channel down; S7 calls it at teardown"
func (h Handle) Stop() Observation {
	if h.state == nil {
		return Observation{Destinations: nil, Attribution: nil}
	}
	return h.state.stop()
}

// CAPEM returns the PEM-encoded CA certificate the runtime must trust as an
// upstream CA (R1). It is empty only on a zero Handle.
//
//deadcode:ignore reason="exposes the channel CA; S7 projects it into the network overlay"
func (h Handle) CAPEM() string { return h.caPEM }

// CAFile returns the path to the 0600 host file holding the CA PEM, because the
// runtime's TLSConfig.UpstreamCACerts takes paths, not PEM contents (R1). Stop
// removes the file.
//
//deadcode:ignore reason="exposes the channel CA file path; S7 projects it into the network overlay"
func (h Handle) CAFile() string { return h.caFile }

// stop closes the server, removes the CA file, and returns the artefact. It is
// idempotent.
//
//deadcode:ignore reason="reached only from Handle.Stop, whose invocation S7 wires"
func (s *proxyState) stop() Observation {
	s.mu.Lock()
	if !s.stopped {
		s.stopped = true
		_ = s.server.Close()
	}
	observation := s.recorder.result(Observation{Destinations: nil, Attribution: nil})
	s.mu.Unlock()

	_ = os.Remove(s.caFile)
	return observation
}

// generateCA creates a fresh self-signed CA for one proxy, returned parsed (for
// goproxy's per-host leaf signing) and PEM-encoded (for the runtime to trust).
//
//deadcode:ignore reason="reached only from Start, whose invocation S7 wires"
func generateCA() (tls.Certificate, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
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
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, string(pemBytes), nil
}

// writeCAFile writes the CA PEM to a per-handle 0600 host file and returns its
// path.
//
//deadcode:ignore reason="reached only from Start, whose invocation S7 wires"
func writeCAFile(caPEM string) (string, error) {
	file, err := os.CreateTemp("", "agents-sandbox-channel-ca-*.pem")
	if err != nil {
		return "", err
	}
	if _, err := file.WriteString(caPEM); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}

// observedCredentialHeaders returns the lower-case names of the request headers
// that can carry a credential. Only the names are read; no value is touched.
//
//deadcode:ignore reason="reached only from onRequest, reached only from Start, whose invocation S7 wires"
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
//
//deadcode:ignore reason="reached only from onRequest, reached only from Start, whose invocation S7 wires"
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
