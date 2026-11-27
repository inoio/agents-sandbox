package journeys

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// stubMCPPath is where the stub serves its MCP endpoint.
const stubMCPPath = "/mcp"

// stubMCPServerName is the name the journey's harness config gives the stub's
// MCP server.
const stubMCPServerName = "journeys"

// StubOAuthRequest is one request the stub OAuth server answered.
type StubOAuthRequest struct {
	Method        string
	Path          string
	Authorization string
}

// StubOAuthServer is a host-side OAuth 2.1 authorization server and an MCP
// server behind it, in one listener: an MCP client in the VM discovers the
// authorization server through the MCP endpoint's 401, registers a client,
// sends the user's browser to the authorize endpoint, exchanges the code (with
// its PKCE verifier) at the token endpoint, and calls the MCP endpoint with the
// issued token. No account is involved; the token proves the whole flow ran.
type StubOAuthServer struct {
	server   *http.Server
	listener net.Listener
	port     string
	vmOrigin string
	// tls is the test CA and certificate when the stub serves HTTPS, which an
	// MCP client that sends credentials only over TLS needs.
	tls *stubTLS

	mu         sync.Mutex
	requests   []StubOAuthRequest
	codes      int
	challenges map[string]string // authorization code -> PKCE S256 challenge
	issued     []string
	refreshes  map[string]bool // refresh tokens issued and not yet used
	authorized []string        // the tokens authorized MCP calls carried
}

// StartStubOAuthServer starts the stub on the host loopback; the VM reaches it
// through the host gateway (the `host` network profile).
func StartStubOAuthServer(tb driver.TB, useTLS bool) (*StubOAuthServer, error) {
	tb.Helper()
	//testenv:allow the stub authorization server is a real host-side listener
	listener, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start the stub OAuth server: %w", err)
	}
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("resolve the stub OAuth server's port: %w", err)
	}
	stub := &StubOAuthServer{
		listener:   listener,
		port:       port,
		vmOrigin:   stubOrigin() + ":" + port,
		challenges: map[string]string{},
		refreshes:  map[string]bool{},
	}
	stub.server = &http.Server{Handler: stub, ReadHeaderTimeout: stubReadHeaderTimeout}
	tb.Cleanup(func() { _ = stub.server.Close() })
	if !useTLS {
		go func() { _ = stub.server.Serve(listener) }()
		return stub, nil
	}
	certs, err := newStubTLS()
	// gomutants:disable-next-line BRANCH_IF reason="newStubTLS fails only on a broken system random source, which a test cannot provoke"
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	stub.tls = certs
	stub.vmOrigin = "https://" + stubGatewayHost + ":" + port
	stub.server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{certs.server}, MinVersion: tls.VersionTLS12}
	go func() { _ = stub.server.ServeTLS(listener, "", "") }()
	return stub, nil
}

// MCPURL is the MCP endpoint as the VM reaches it.
func (s *StubOAuthServer) MCPURL() string { return s.vmOrigin + stubMCPPath }

// LoopbackAddr is the listener's host address. The browser opens the authorize
// endpoint under the VM's name for the host (codex requires the authorize
// endpoint on the authorization server's origin), which the launcher's opener
// takes for a remote host; only the journey's fake browser resolves it, here.
func (s *StubOAuthServer) LoopbackAddr() string { return s.listener.Addr().String() }

// Requests returns every request the stub answered, in order.
func (s *StubOAuthServer) Requests() []StubOAuthRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]StubOAuthRequest(nil), s.requests...)
}

// Issued returns the access tokens the token endpoint issued.
func (s *StubOAuthServer) Issued() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.issued...)
}

// AuthorizedMCPCalls returns the token each MCP request carried that the stub
// had issued, since the last ResetAuthorizedMCPCalls.
func (s *StubOAuthServer) AuthorizedMCPCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.authorized...)
}

// ResetAuthorizedMCPCalls starts the record of authorized MCP calls afresh and
// returns how many there were, so a check sees only the calls after a point.
func (s *StubOAuthServer) ResetAuthorizedMCPCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := len(s.authorized)
	s.authorized = nil
	return before
}

// CAPEM is the test CA a client must trust to reach the stub over HTTPS, or
// nil for a plain-HTTP stub.
func (s *StubOAuthServer) CAPEM() []byte {
	if s.tls == nil {
		return nil
	}
	return s.tls.caPEM
}

// rootCAs is the pool a host-side client trusts the stub with, or nil.
func (s *StubOAuthServer) rootCAs() *x509.CertPool {
	if s.tls == nil {
		return nil
	}
	return s.tls.pool
}

// Origin is the origin every endpoint is advertised under, as the VM reaches
// it.
func (s *StubOAuthServer) Origin() string { return s.vmOrigin }

func (s *StubOAuthServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, StubOAuthRequest{
		Method:        request.Method,
		Path:          request.URL.Path,
		Authorization: request.Header.Get("Authorization"),
	})
	s.mu.Unlock()
	switch path := request.URL.Path; {
	case strings.HasPrefix(path, "/.well-known/oauth-protected-resource"):
		writeJSON(writer, http.StatusOK, map[string]any{
			"resource":              s.MCPURL(),
			"authorization_servers": []string{s.vmOrigin},
		})
	case strings.HasPrefix(path, "/.well-known/oauth-authorization-server"),
		strings.HasPrefix(path, "/.well-known/openid-configuration"):
		writeJSON(writer, http.StatusOK, s.metadata())
	case path == "/register":
		s.register(writer, request)
	case path == "/authorize":
		s.authorize(writer, request)
	case path == "/token":
		s.token(writer, request)
	case path == stubMCPPath:
		s.mcp(writer, request)
	default:
		http.NotFound(writer, request)
	}
}

// metadata is the RFC 8414 authorization server metadata, every endpoint on
// the origin the VM reaches.
func (s *StubOAuthServer) metadata() map[string]any {
	return map[string]any{
		"issuer":                                s.vmOrigin,
		"authorization_endpoint":                s.vmOrigin + "/authorize",
		"token_endpoint":                        s.vmOrigin + "/token",
		"registration_endpoint":                 s.vmOrigin + "/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
	}
}

// register answers RFC 7591 dynamic client registration with a public client.
func (s *StubOAuthServer) register(writer http.ResponseWriter, request *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(io.LimitReader(request.Body, 1<<20)).Decode(&body)
	response := map[string]any{"client_id": "journeys-client", "token_endpoint_auth_method": "none"}
	for _, key := range []string{"redirect_uris", "client_name", "grant_types", "response_types"} {
		if value, ok := body[key]; ok {
			response[key] = value
		}
	}
	writeJSON(writer, http.StatusCreated, response)
}

// authorize stands in for the user's sign-in and consent: it redirects the
// browser to the client's redirect_uri with a code bound to the PKCE challenge.
func (s *StubOAuthServer) authorize(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	redirectURI := query.Get("redirect_uri")
	if query.Get("code_challenge") == "" || !isValidRedirectURI(redirectURI) {
		http.Error(
			writer,
			"the authorize request needs an HTTP loopback redirect_uri with a port and a PKCE challenge",
			http.StatusBadRequest,
		)
		return
	}
	redirect, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(writer, "the authorize request has an invalid redirect_uri", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.codes++
	code := fmt.Sprintf("journeys-code-%d", s.codes)
	s.challenges[code] = query.Get("code_challenge")
	s.mu.Unlock()
	back := redirect.Query()
	back.Set("code", code)
	if state := query.Get("state"); state != "" {
		back.Set("state", state)
	}
	redirect.RawQuery = back.Encode()
	http.Redirect(writer, request, redirect.String(), http.StatusFound)
}

func isValidRedirectURI(value string) bool {
	// Browsers treat backslashes as URL separators, so reject them before parsing.
	if strings.Contains(value, "\\") {
		return false
	}
	redirect, err := url.Parse(value)
	return err == nil && redirect.Scheme == "http" && redirect.Hostname() != "" &&
		isLoopbackHost(redirect.Hostname()) && redirect.Port() != "" && redirect.User == nil &&
		redirect.Fragment == ""
}

// token exchanges an authorization code whose PKCE verifier matches, or a
// refresh token it issued (once), for an access token.
func (s *StubOAuthServer) token(writer http.ResponseWriter, request *http.Request) {
	if err := request.ParseForm(); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch request.PostForm.Get("grant_type") {
	case "authorization_code":
		challenge, ok := s.challenges[request.PostForm.Get("code")]
		if !ok || pkceChallenge(request.PostForm.Get("code_verifier")) != challenge {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
			return
		}
		delete(s.challenges, request.PostForm.Get("code"))
	case "refresh_token":
		if !s.refreshes[request.PostForm.Get("refresh_token")] {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
			return
		}
		delete(s.refreshes, request.PostForm.Get("refresh_token"))
	default:
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
		return
	}
	token := fmt.Sprintf("journeys-token-%d", len(s.issued)+1)
	s.issued = append(s.issued, token)
	s.refreshes[token+"-refresh"] = true
	writeJSON(writer, http.StatusOK, map[string]any{
		"access_token":  token,
		"token_type":    "Bearer",
		"expires_in":    3600,
		"refresh_token": token + "-refresh",
	})
}

// pkceChallenge is the S256 challenge for verifier (RFC 7636).
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// mcp answers the MCP endpoint: a 401 pointing at the protected resource
// metadata without an issued token, else a minimal JSON-RPC server with no
// tools.
func (s *StubOAuthServer) mcp(writer http.ResponseWriter, request *http.Request) {
	token, _ := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	known := token != "" && slices.Contains(s.issued, token)
	if known {
		s.authorized = append(s.authorized, token)
	}
	s.mu.Unlock()
	if !known {
		writer.Header().Set("WWW-Authenticate",
			`Bearer resource_metadata="`+s.vmOrigin+`/.well-known/oauth-protected-resource`+stubMCPPath+`"`)
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var call struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"params"`
	}
	if err := json.NewDecoder(io.LimitReader(request.Body, 1<<20)).Decode(&call); err != nil {
		http.Error(writer, "bad JSON-RPC", http.StatusBadRequest)
		return
	}
	if len(call.ID) == 0 {
		writer.WriteHeader(http.StatusAccepted)
		return
	}
	var result any = map[string]any{}
	switch call.Method {
	case "initialize":
		result = map[string]any{
			"protocolVersion": call.Params.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": "journeys-stub", "version": "1"},
		}
	case "tools/list":
		result = map[string]any{"tools": []any{}}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": call.ID, "result": result})
}

// writeJSON writes value as a JSON response with status.
func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
