package journeys

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// StubProvider is a host-side destination a journey's agent sends its turns to,
// so a journey needs no real model or egress. The config that points a harness at it is the harness
// adapter's (Harness.Stub.HomeFiles).
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
	stubProviderAPIKey = "sk-stub-not-a-real-key"
	// stubCredentialEnvVar is the env-var name the read-back derives for
	// stubProviderAPIKey: <PROVIDERID>_API_KEY, i.e. derivedEnvVarName("stub").
	// Kept here beside the provider so a lifecycle change to the id cannot leave
	// a journey asserting a name the read-back no longer derives.
	stubCredentialEnvVar = "STUB_API_KEY"
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
	path := request.URL.Path
	if protocol, ok := turnProtocolFor(path); ok {
		call, reply := p.answerFor(completion.side())
		protocol.serve(response, completion, call, reply)
		return
	}
	switch {
	case strings.HasSuffix(path, "/models"):
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"object":"list","data":[{"id":"` + stubProviderModel + `","object":"model"}]}`))
	case strings.HasSuffix(path, stubCountTokenPath):
		serveTokenCount(response)
	default:
		http.NotFound(response, request)
	}
}

// turnProtocol is one model protocol the stub speaks: the path suffix of its
// turn request and how it serves a turn's answer.
type turnProtocol struct {
	suffix string
	serve  func(response http.ResponseWriter, request chatRequest, call *ToolCall, reply string)
}

// turnProtocols are the protocols the stub answers turns in.
var turnProtocols = []turnProtocol{
	{suffix: stubCompletionPath, serve: serveChatCompletion},
	{suffix: stubMessagesPath, serve: serveMessages},
	{suffix: stubResponsesPath, serve: serveResponses},
}

// turnProtocolFor returns the protocol whose turn path path is, if any; an
// Anthropic token count ends in its own suffix, so it is not a Messages turn.
func turnProtocolFor(path string) (turnProtocol, bool) {
	for _, protocol := range turnProtocols {
		if strings.HasSuffix(path, protocol.suffix) {
			return protocol, true
		}
	}
	return turnProtocol{}, false
}

// isTurnPath reports whether path is one of the protocols' turn paths.
func isTurnPath(path string) bool {
	_, ok := turnProtocolFor(path)
	return ok
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
