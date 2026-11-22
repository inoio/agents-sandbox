package journeys

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// StubProvider is a host-side destination a journey's agent sends to, so the
// learn-mode terminating proxy observes a credential->destination pair without
// any real egress. It is the opencode2 provider config that points the agent at
// the listener plus the listener itself.
type StubProvider interface {
	// Addr is the advertised origin the VM should target, scheme://host:port,
	// carrying the listener's own ephemeral port. The agent's provider baseURL
	// is Addr()+"/v1".
	Addr() string
	// ProviderConfig is the snippet files to seed into the agent's launcher
	// config dir (driver.SeedAgentSnippets' name->content map) so the agent's
	// merged config points at the stub.
	ProviderConfig() map[string]string
	// Stop closes the listener. It is idempotent.
	Stop()
}

// stubOriginEnv overrides the advertised destination origin (scheme://host) the
// provider config points the agent at. The stub always binds host loopback on an
// ephemeral port; only the *advertised* origin changes, because how a guest's
// target host resolves to this host-side listener under microsandbox's runtime
// interception is not yet pinned. A host accept sets it to a route that reaches
// the proxy and resolves to the listener; unset (or invalid) uses
// stubOriginDefault.
const stubOriginEnv = "AGENTS_SANDBOX_JOURNEYS_STUB_ORIGIN"

// stubOriginDefault is the advertised destination a run uses with no override.
// The reserved .invalid TLD cannot resolve, so the proxy records the destination
// (and its dial denial) but never reaches a real service; a host accept pins a
// working route through stubOriginEnv.
const stubOriginDefault = "http://agents-sandbox-stub.invalid"

// stubSnippetName is the seeded config file name; opencode2's merge reads it
// (its snippet pattern is opencode*.json*).
const stubSnippetName = "opencode-stub.json"

// The synthetic provider the seeded config declares: an OpenAI-compatible
// provider whose baseURL is the stub, plus the model selection that uses it.
const (
	stubProviderID    = "stub"
	stubProviderModel = "stub"
	// stubProviderNPM is the SDK package opencode loads for an
	// OpenAI-compatible provider, so the provider is real enough for the agent
	// to issue a request at the baseURL.
	stubProviderNPM = "@ai-sdk/openai-compatible"
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
		server: &http.Server{
			Handler: stubHandler{},
			// A defensive ceiling on reading a request's headers; the value is
			// not a contract.
			ReadHeaderTimeout: stubReadHeaderTimeout,
		},
		listener: listener,
		addr:     stubOrigin() + ":" + port,
		once:     sync.Once{},
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
}

// Addr returns the advertised origin, scheme://host:port.
func (p *stubProvider) Addr() string { return p.addr }

// ProviderConfig returns the one opencode snippet pointing at the stub.
func (p *stubProvider) ProviderConfig() map[string]string {
	return map[string]string{stubSnippetName: p.providerDocument()}
}

// providerDocument renders the opencode provider config that points the agent at
// the stub: an OpenAI-compatible provider whose options.baseURL is the stub's
// /v1, and the top-level model that selects it.
func (p *stubProvider) providerDocument() string {
	document := map[string]any{
		"provider": map[string]any{
			stubProviderID: map[string]any{
				"npm":     stubProviderNPM,
				"options": map[string]any{"baseURL": p.addr + "/v1"},
				"models":  map[string]any{stubProviderModel: map[string]any{}},
			},
		},
		"model": stubProviderID + "/" + stubProviderModel,
	}
	data, err := json.Marshal(document)
	if err != nil {
		// The document is all marshalable literals, so this is unreachable.
		return "{}"
	}
	return string(data)
}

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

// stubOriginFromValue parses an advertised-origin override: an http(s) URL with
// a host yields scheme://hostname (its port, userinfo, and path are dropped,
// because the listener's own port is always used); anything else yields
// stubOriginDefault. Kept pure so it is testable without the host.
func stubOriginFromValue(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Hostname() == "" {
		return stubOriginDefault
	}
	switch parsed.Scheme {
	case "http", "https":
		return parsed.Scheme + "://" + parsed.Hostname()
	default:
		return stubOriginDefault
	}
}

// stubHandler answers the stub's requests. A completed model turn is not
// required — the journeys assert what the proxy observed — so it returns a
// minimal OpenAI-compatible model list for /v1/models and an empty chat
// completion for anything else.
type stubHandler struct{}

func (stubHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	body := `{"id":"stub","object":"chat.completion","choices":[]}`
	if strings.HasSuffix(request.URL.Path, "/models") {
		body = `{"object":"list","data":[{"id":"` + stubProviderModel + `","object":"model"}]}`
	}
	_, _ = response.Write([]byte(body))
}
