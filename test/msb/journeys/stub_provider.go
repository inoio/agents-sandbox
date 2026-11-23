package journeys

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// StubProvider is a host-side destination a journey's agent sends its turns to,
// so a journey needs no real model or egress. The config that points a harness at it is the harness
// adapter's (Harness.StubConfig).
type StubProvider interface {
	// Addr is the advertised origin the VM should target, scheme://host:port,
	// carrying the listener's own ephemeral port. The agent's provider baseURL
	// is Addr()+"/v1".
	Addr() string
	// Replies returns the main-model text replies the stub sent so far, in
	// order.
	Replies() []string
	// ScriptToolCall makes the stub answer the next main-model turn with call
	// instead of its text reply, once; the turn after it gets the text reply.
	ScriptToolCall(call ToolCall)
	// Requests returns the requests the stub served so far, in arrival order,
	// so a journey can assert that a turn reached it.
	Requests() []StubRequest
	// Stop closes the listener. It is idempotent.
	Stop()
}

// StubRequest is one request the stub served.
type StubRequest struct {
	Method string
	Path   string
	// Model is the request body's model, empty for a request without one.
	Model string
	// Side marks a side request (a title), not a turn (chatRequest.side).
	Side bool
	// DecodeError is why the request body did not decode, empty when it did.
	DecodeError string
}

// stubOriginEnv overrides the advertised destination origin (scheme://host) the
// provider config points the agent at. The stub always binds host loopback on an
// ephemeral port; only the *advertised* origin changes. Unset (or invalid) uses
// stubOriginDefault.
const stubOriginEnv = "AGENTS_SANDBOX_JOURNEYS_STUB_ORIGIN"

// stubOriginDefault is the advertised destination a run uses with no override:
// microsandbox's host gateway name, which a VM reaches under the `host` network
// profile and which lands on the host's loopback listener. Host-gateway traffic
// does not pass the learn-mode observation proxy, so a turn to it completes but
// is not observed.
const stubOriginDefault = "http://" + stubGatewayHost

// stubGatewayHost is microsandbox's name for the host machine inside a VM.
const stubGatewayHost = "host.microsandbox.internal"

// The synthetic provider the seeded config declares: an OpenAI-compatible
// provider whose baseURL is the stub, plus the model selection that uses it.
const (
	stubProviderID    = "stub"
	stubProviderModel = "stub"
	// stubTitleModel is the provider's small model, which opencode uses for
	// side requests such as the session title. Keeping it apart from
	// stubProviderModel lets a journey tell its turn from those requests.
	stubTitleModel = "stub-title"
	// stubProviderAPIKey is the provider's inline credential. The read-back
	// classifies a literal options.apiKey as a static secret and derives its
	// env-var name as derivedEnvVarName(stubProviderID); a journey asserts the
	// saved env.secret.yaml scopes that name to the provider's baseURL host. It
	// is a dummy value: the stub never checks it.
	stubProviderAPIKey = "sk-stub-not-a-real-key" //nolint:gosec // a dummy stub credential, never a real secret
	// stubCredentialEnvVar is the env-var name the read-back derives for
	// stubProviderAPIKey: <PROVIDERID>_API_KEY, i.e. derivedEnvVarName("stub").
	// Kept here beside the provider so a lifecycle change to the id cannot leave
	// a journey asserting a name the read-back no longer derives.
	stubCredentialEnvVar = "STUB_API_KEY" //nolint:gosec // an env-var name, not a secret value
)

// stubReadHeaderTimeout is a defensive ceiling on reading a request's headers;
// the value is not a contract and a local stub answers well within it.
const stubReadHeaderTimeout = 30 * time.Second

// StartStubProvider binds a host-side HTTP listener on an ephemeral loopback
// port and returns the StubProvider whose ProviderConfig points an agent at it.
// The advertised origin is stubOriginEnv (default stubOriginDefault); the port
// is always the listener's. The listener is closed by tb.Cleanup as well as by
// Stop.
//
// The stub serves plain HTTP so the agent's request traverses the terminating
// proxy with no CA or skip-upstream-verify wiring; the proxy records the
// destination by its advertised host. A host accept that switches the override
// to https must also enable network.observe.skip-upstream-verify, because the
// stub's certificate is not trusted.
func StartStubProvider(tb driver.TB) (StubProvider, error) {
	tb.Helper()

	//testenv:allow the stub is a real host-side listener the terminating proxy dials
	listener, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start stub provider listener: %w", err)
	}
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("resolve stub provider port: %w", err)
	}

	provider := &stubProvider{
		listener: listener,
		addr:     stubOrigin() + ":" + port,
	}
	provider.server = &http.Server{
		Handler: provider,
		// A defensive ceiling on reading a request's headers; the value is
		// not a contract.
		ReadHeaderTimeout: stubReadHeaderTimeout,
	}
	tb.Cleanup(provider.Stop)
	go func() { _ = provider.server.Serve(listener) }()
	return provider, nil
}

// stubProvider is the StubProvider a StartStubProvider call returns.
//
//exhaustruct:ignore
type stubProvider struct {
	server   *http.Server
	listener net.Listener
	addr     string
	once     sync.Once

	mu       sync.Mutex
	requests []StubRequest
	toolCall *ToolCall
	replies  []string
}

// Addr returns the advertised origin, scheme://host:port.
func (p *stubProvider) Addr() string { return p.addr }

// Stop closes the listener. It is idempotent, so tb.Cleanup may run it after a
// test already did.
func (p *stubProvider) Stop() {
	p.once.Do(func() { _ = p.server.Close() })
}

// stubOrigin returns the advertised destination origin, from stubOriginEnv when
// it names a usable http(s) origin, else stubOriginDefault.
func stubOrigin() string {
	//testenv:allow the stub provider reads its own advertised-route override
	return stubOriginFromValue(os.Getenv(stubOriginEnv))
}

// stubAdvertisedHost is the host part of the advertised origin the stub points
// agents at, so a journey scopes its credential assertion to the route the run
// actually used (the default, or a host accept's override). It reads the same
// override stubOrigin does, and an origin with no host yields "".
func stubAdvertisedHost() string {
	parsed, err := url.Parse(stubOrigin())
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	return parsed.Hostname()
}

// stubOriginFromValue parses an advertised-origin override: an http(s) URL with
// a host yields scheme://hostname (its port, userinfo, and path are dropped,
// because the listener's own port is always used); anything else yields
// stubOriginDefault. An IPv6 literal host is re-bracketed, so `http://[::1]`
// round-trips rather than re-emitting the invalid `http://::1`. Kept pure so it
// is testable without the host.
func stubOriginFromValue(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Hostname() == "" {
		return stubOriginDefault
	}
	switch parsed.Scheme {
	case "http", "https":
		return parsed.Scheme + "://" + bracketedHost(parsed.Hostname())
	default:
		return stubOriginDefault
	}
}

// bracketedHost wraps an IPv6 literal host in brackets, so an origin URL is
// valid; every other host is returned unchanged. url.Hostname strips the
// brackets, which must be restored when the host is emitted in a URL.
func bracketedHost(hostname string) string {
	if strings.Contains(hostname, ":") {
		return "[" + hostname + "]"
	}
	return hostname
}

// ServeHTTP records the request and answers it by its path: the model list, an
// Anthropic token count, an Anthropic Messages turn, an OpenAI Responses turn,
// or an OpenAI chat completion; anything else is not found. A turn whose body
// does not decode (a compressed or unexpected body) is answered 400 and recorded
// with its error, so a protocol mismatch shows in the request log instead of
// passing as a tool-less side request.
func (p *stubProvider) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	var completion chatRequest
	decodeErr := json.NewDecoder(request.Body).Decode(&completion)
	if errors.Is(decodeErr, io.EOF) {
		decodeErr = nil
	}
	decodeError := ""
	if decodeErr != nil {
		decodeError = decodeErr.Error()
	}
	recorded := StubRequest{
		Method: request.Method, Path: request.URL.Path, Model: completion.Model, Side: completion.side(),
		DecodeError: decodeError,
	}
	p.record(recorded)
	if decodeErr != nil && isTurnPath(request.URL.Path) {
		http.Error(response, "stub: undecodable request body: "+decodeErr.Error(), http.StatusBadRequest)
		return
	}
	switch path := request.URL.Path; {
	case strings.HasSuffix(path, "/models"):
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"object":"list","data":[{"id":"` + stubProviderModel + `","object":"model"}]}`))
	case strings.HasSuffix(path, stubCountTokenPath):
		serveTokenCount(response)
	case strings.HasSuffix(path, stubMessagesPath):
		call, reply := p.answerFor(completion.side())
		serveMessages(response, completion, call, reply)
	case strings.HasSuffix(path, stubResponsesPath):
		call, reply := p.answerFor(completion.side())
		serveResponses(response, call, reply)
	case strings.HasSuffix(path, stubCompletionPath):
		call, reply := p.answerFor(completion.side())
		serveChatCompletion(response, completion, call, reply)
	default:
		http.NotFound(response, request)
	}
}

// isTurnPath reports whether path is one of the protocols' turn paths.
func isTurnPath(path string) bool {
	return strings.HasSuffix(path, stubCompletionPath) ||
		strings.HasSuffix(path, stubMessagesPath) ||
		strings.HasSuffix(path, stubResponsesPath)
}

// answerFor decides a completion's answer: the title reply for a side
// request, else the armed tool call (disarming it), else the next numbered text
// reply, which it records.
func (p *stubProvider) answerFor(side bool) (*ToolCall, string) {
	if side {
		return nil, stubTitleReply
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if call := p.toolCall; call != nil {
		p.toolCall = nil
		return call, ""
	}
	reply := stubReply(len(p.replies) + 1)
	p.replies = append(p.replies, reply)
	return nil, reply
}

// Replies returns a copy of the main-model text replies sent so far.
func (p *stubProvider) Replies() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.replies...)
}

// ScriptToolCall arms call for the next main-model turn.
func (p *stubProvider) ScriptToolCall(call ToolCall) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.toolCall = &call
}

func (p *stubProvider) record(request StubRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, request)
}

// Requests returns a copy of the requests served so far.
func (p *stubProvider) Requests() []StubRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]StubRequest(nil), p.requests...)
}
